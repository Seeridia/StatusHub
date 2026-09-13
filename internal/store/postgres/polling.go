package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const acquireDueSourcesSQL = `
WITH due AS (
    SELECT id
    FROM sources
    WHERE enabled
      AND next_poll_at <= statement_timestamp()
      AND (lease_until IS NULL OR lease_until <= statement_timestamp())
    ORDER BY next_poll_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $1
), leased AS (
    UPDATE sources AS source
    SET lease_owner = $2,
        lease_token = gen_random_uuid(),
        lease_until = statement_timestamp() + ($3::double precision * interval '1 microsecond'),
        last_attempt_at = statement_timestamp(),
        updated_at = statement_timestamp()
    FROM due
    WHERE source.id = due.id
    RETURNING source.id,
              source.tenant_id,
              source.vendor_id,
              source.requested_url,
              source.final_url,
              source.canonical_url,
              source.source_type,
              source.adapter_name,
              source.adapter_version,
              source.health_state,
              source.next_poll_at,
              source.lease_owner,
              source.lease_token,
              source.lease_until,
              source.failure_streak,
              source.last_attempt_at,
              source.last_success_at,
              source.last_checkpoint
)
SELECT id,
       tenant_id,
       vendor_id,
       requested_url,
       final_url,
       canonical_url,
       source_type,
       adapter_name,
       adapter_version,
       health_state,
       next_poll_at,
       lease_owner,
       lease_token,
       lease_until,
       failure_streak,
       last_attempt_at,
       last_success_at,
       last_checkpoint
FROM leased
ORDER BY next_poll_at, id`

const completePollSQL = `
UPDATE sources
SET next_poll_at = $3,
    lease_owner = NULL,
    lease_token = NULL,
    lease_until = NULL,
    failure_streak = 0,
    last_failure_code = NULL,
    last_success_at = COALESCE($4, statement_timestamp()),
    last_checkpoint = $5,
    health_state = COALESCE(NULLIF($6, ''), health_state),
    updated_at = statement_timestamp()
WHERE id = $1
  AND lease_token = $2
  AND lease_until > statement_timestamp()`

const failPollSQL = `
UPDATE sources
SET next_poll_at = $3,
    lease_owner = NULL,
    lease_token = NULL,
    lease_until = NULL,
    failure_streak = failure_streak + 1,
    last_failure_code = NULLIF($5,''),
    health_state = COALESCE(NULLIF($4, ''), health_state),
    updated_at = statement_timestamp()
WHERE id = $1
  AND lease_token = $2
  AND lease_until > statement_timestamp()`

const completeRegionalPollSQL = `
UPDATE sources
SET next_poll_at=$3,lease_owner=NULL,lease_token=NULL,lease_until=NULL,failure_streak=0,
    last_failure_code=NULL,
    last_success_at=COALESCE($4,statement_timestamp()),last_checkpoint=$5,
    health_state=COALESCE(NULLIF($6,''),health_state),updated_at=statement_timestamp()
WHERE id=$1 AND lease_token=$2 AND lease_until>statement_timestamp()
  AND EXISTS (
      SELECT 1 FROM source_ownership ownership
      WHERE ownership.source_id=sources.id AND ownership.active_region=$7 AND ownership.epoch=$8
  )`

const failRegionalPollSQL = `
UPDATE sources
SET next_poll_at=$3,lease_owner=NULL,lease_token=NULL,lease_until=NULL,
    failure_streak=failure_streak+1,health_state=COALESCE(NULLIF($4,''),health_state),
    last_failure_code=NULLIF($7,''),
    updated_at=statement_timestamp()
WHERE id=$1 AND lease_token=$2 AND lease_until>statement_timestamp()
  AND EXISTS (
      SELECT 1 FROM source_ownership ownership
      WHERE ownership.source_id=sources.id AND ownership.active_region=$5 AND ownership.epoch=$6
  )`

// AcquireDueSources claims up to limit sources in one short transaction. The
// caller must commit no network work inside this method; all returned leases
// have already been committed before the method returns.
func (s *Store) AcquireDueSources(
	ctx context.Context,
	owner string,
	limit int,
	leaseDuration time.Duration,
) ([]SourceLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if err := required(owner, "lease owner"); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, invalid("limit must be greater than zero")
	}
	if leaseDuration < time.Microsecond {
		return nil, invalid("lease duration must be at least one microsecond")
	}

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: acquire due sources: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, acquireDueSourcesSQL, limit, owner, leaseDuration.Microseconds())
	if err != nil {
		return nil, fmt.Errorf("postgres store: acquire due sources: query: %w", err)
	}
	defer rows.Close()

	leases := make([]SourceLease, 0, limit)
	for rows.Next() {
		var lease SourceLease
		if err := rows.Scan(
			&lease.ID,
			&lease.TenantID,
			&lease.VendorID,
			&lease.RequestedURL,
			&lease.FinalURL,
			&lease.CanonicalURL,
			&lease.SourceType,
			&lease.AdapterName,
			&lease.AdapterVersion,
			&lease.HealthState,
			&lease.NextPollAt,
			&lease.LeaseOwner,
			&lease.LeaseToken,
			&lease.LeaseUntil,
			&lease.FailureStreak,
			&lease.LastAttemptAt,
			&lease.LastSuccessAt,
			&lease.LastCheckpoint,
		); err != nil {
			return nil, fmt.Errorf("postgres store: acquire due sources: scan: %w", err)
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: acquire due sources: rows: %w", err)
	}
	rows.Close()

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: acquire due sources: commit: %w", err)
	}
	return leases, nil
}

// CompletePoll records a successful poll only if LeaseToken still owns the
// source. A stale worker receives ErrLeaseLost and cannot overwrite a newer
// worker's scheduling state.
func (s *Store) CompletePoll(ctx context.Context, params CompletePollParams) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := validatePollCAS(params.SourceID, params.LeaseToken, params.NextPollAt); err != nil {
		return err
	}

	query := completePollSQL
	arguments := []any{params.SourceID, params.LeaseToken, params.NextPollAt, params.SuccessfulAt, params.Checkpoint, params.HealthState}
	if params.ActiveRegion != "" || params.OwnershipEpoch != 0 {
		if !validRegion(params.ActiveRegion) || params.OwnershipEpoch <= 0 {
			return invalid("regional completion requires a valid region and ownership epoch")
		}
		query = completeRegionalPollSQL
		arguments = append(arguments, params.ActiveRegion, params.OwnershipEpoch)
	}
	tag, err := s.db.Exec(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("postgres store: complete poll: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: complete source %s", ErrLeaseLost, params.SourceID)
	}
	return nil
}

// FailPoll records a failed attempt and releases the source only if the lease
// token still matches. Failure policy chooses NextPollAt and optional health
// state; the store only performs the fenced state transition.
func (s *Store) FailPoll(ctx context.Context, params FailPollParams) error {
	switch params.FailureCode {
	case "", "rate_limited", "access_denied", "upstream_server", "upstream_http", "timeout", "network", "invalid_payload", "unsupported_source", "cancelled", "unclassified":
	default:
		return invalid("unsupported collection failure code")
	}
	if err := s.ready(); err != nil {
		return err
	}
	if err := validatePollCAS(params.SourceID, params.LeaseToken, params.NextPollAt); err != nil {
		return err
	}

	query := failPollSQL
	arguments := []any{params.SourceID, params.LeaseToken, params.NextPollAt, params.HealthState}
	if params.ActiveRegion != "" || params.OwnershipEpoch != 0 {
		if !validRegion(params.ActiveRegion) || params.OwnershipEpoch <= 0 {
			return invalid("regional failure requires a valid region and ownership epoch")
		}
		query = failRegionalPollSQL
		arguments = append(arguments, params.ActiveRegion, params.OwnershipEpoch)
	}
	arguments = append(arguments, params.FailureCode)
	tag, err := s.db.Exec(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("postgres store: fail poll: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("%w: fail source %s", ErrLeaseLost, params.SourceID)
	}
	return nil
}

func validatePollCAS(sourceID, leaseToken string, nextPollAt time.Time) error {
	if err := required(sourceID, "source ID"); err != nil {
		return err
	}
	if err := required(leaseToken, "lease token"); err != nil {
		return err
	}
	if nextPollAt.IsZero() {
		return invalid("next poll time is required")
	}
	return nil
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
