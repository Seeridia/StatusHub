package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var regionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type Region struct {
	ID              string          `json:"id"`
	Enabled         bool            `json:"enabled"`
	LastHeartbeatAt time.Time       `json:"last_heartbeat_at"`
	Metadata        json.RawMessage `json:"metadata"`
}

type SourceOwnership struct {
	SourceID     string     `json:"source_id"`
	HomeRegion   string     `json:"home_region"`
	ActiveRegion string     `json:"active_region"`
	Epoch        int64      `json:"epoch"`
	State        string     `json:"state"`
	ChangedBy    string     `json:"changed_by"`
	ChangeReason string     `json:"change_reason"`
	PromotedAt   *time.Time `json:"promoted_at,omitempty"`
}

type ChangeSourceRegionParams struct {
	SourceID        string
	ScopeTenantID   string
	TargetRegion    string
	ExpectedEpoch   int64
	HeartbeatMaxAge time.Duration
	ChangedBy       string
	Reason          string
}

func (s *Store) HeartbeatRegion(ctx context.Context, regionID string, metadata json.RawMessage) (Region, error) {
	if err := s.ready(); err != nil {
		return Region{}, err
	}
	if !validRegion(regionID) {
		return Region{}, invalid("region ID is invalid")
	}
	if len(metadata) == 0 {
		metadata = json.RawMessage(`{}`)
	}
	if !json.Valid(metadata) || len(metadata) > 64<<10 {
		return Region{}, invalid("region metadata must be valid JSON up to 64 KiB")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Region{}, fmt.Errorf("postgres store: heartbeat region: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var region Region
	err = tx.QueryRow(ctx, `
INSERT INTO regions(id,metadata) VALUES($1,$2)
ON CONFLICT (id) DO UPDATE SET last_heartbeat_at=statement_timestamp(),metadata=excluded.metadata,updated_at=statement_timestamp()
RETURNING id,enabled,last_heartbeat_at,metadata`, regionID, metadata).Scan(
		&region.ID, &region.Enabled, &region.LastHeartbeatAt, &region.Metadata)
	if err != nil {
		return Region{}, fmt.Errorf("postgres store: heartbeat region: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Region{}, fmt.Errorf("postgres store: heartbeat region: commit: %w", err)
	}
	return region, nil
}

// BootstrapSourceOwnership assigns only previously unowned sources. Existing
// ownership is never moved by a daemon coming online in another region.
func (s *Store) BootstrapSourceOwnership(ctx context.Context, regionID string) (int64, error) {
	if err := s.ready(); err != nil {
		return 0, err
	}
	if !validRegion(regionID) {
		return 0, invalid("region ID is invalid")
	}
	tag, err := s.db.Exec(ctx, `
INSERT INTO source_ownership(source_id,home_region,active_region)
SELECT src.id,$1,$1 FROM sources src
JOIN regions r ON r.id=$1 AND r.enabled
ON CONFLICT (source_id) DO NOTHING`, regionID)
	if err != nil {
		return 0, fmt.Errorf("postgres store: bootstrap source ownership: %w", err)
	}
	return tag.RowsAffected(), nil
}

const acquireDueSourcesInRegionSQL = `
WITH due AS (
    SELECT source.id,ownership.epoch,ownership.active_region
    FROM sources source
    JOIN source_ownership ownership ON ownership.source_id=source.id AND ownership.active_region=$4
    JOIN regions region ON region.id=ownership.active_region AND region.enabled
    WHERE source.enabled
      AND source.next_poll_at <= statement_timestamp()
      AND (source.lease_until IS NULL OR source.lease_until <= statement_timestamp())
    ORDER BY source.next_poll_at,source.id
    FOR UPDATE OF source,ownership SKIP LOCKED
    LIMIT $1
), leased AS (
    UPDATE sources AS source
    SET lease_owner=$2,lease_token=gen_random_uuid(),
        lease_until=statement_timestamp()+($3::double precision*interval '1 microsecond'),
        last_attempt_at=statement_timestamp(),updated_at=statement_timestamp()
    FROM due WHERE source.id=due.id
    RETURNING source.*,due.epoch,due.active_region
)
SELECT id,tenant_id,vendor_id,requested_url,final_url,canonical_url,source_type,
       adapter_name,adapter_version,health_state,next_poll_at,lease_owner,lease_token,
       lease_until,failure_streak,last_attempt_at,last_success_at,last_checkpoint,
       active_region,epoch
FROM leased ORDER BY next_poll_at,id`

func (s *Store) AcquireDueSourcesInRegion(ctx context.Context, regionID, owner string, limit int, leaseDuration time.Duration) ([]SourceLease, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if !validRegion(regionID) || strings.TrimSpace(owner) == "" || limit <= 0 || leaseDuration < time.Microsecond {
		return nil, invalid("regional source claim arguments are invalid")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres store: acquire regional sources: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, acquireDueSourcesInRegionSQL, limit, owner, leaseDuration.Microseconds(), regionID)
	if err != nil {
		return nil, fmt.Errorf("postgres store: acquire regional sources: %w", err)
	}
	defer rows.Close()
	leases := make([]SourceLease, 0, limit)
	for rows.Next() {
		var lease SourceLease
		if err := scanRegionalSourceLease(rows, &lease); err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres store: scan regional source leases: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("postgres store: acquire regional sources: commit: %w", err)
	}
	return leases, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanRegionalSourceLease(row rowScanner, lease *SourceLease) error {
	if err := row.Scan(&lease.ID, &lease.TenantID, &lease.VendorID, &lease.RequestedURL, &lease.FinalURL,
		&lease.CanonicalURL, &lease.SourceType, &lease.AdapterName, &lease.AdapterVersion, &lease.HealthState,
		&lease.NextPollAt, &lease.LeaseOwner, &lease.LeaseToken, &lease.LeaseUntil, &lease.FailureStreak,
		&lease.LastAttemptAt, &lease.LastSuccessAt, &lease.LastCheckpoint, &lease.ActiveRegion, &lease.OwnershipEpoch); err != nil {
		return fmt.Errorf("postgres store: scan regional source lease: %w", err)
	}
	return nil
}

func (s *Store) ChangeSourceRegion(ctx context.Context, params ChangeSourceRegionParams) (SourceOwnership, error) {
	if err := s.ready(); err != nil {
		return SourceOwnership{}, err
	}
	if _, err := uuid.Parse(params.SourceID); err != nil || !validRegion(params.TargetRegion) || params.ExpectedEpoch <= 0 ||
		params.HeartbeatMaxAge <= 0 || strings.TrimSpace(params.ChangedBy) == "" || strings.TrimSpace(params.Reason) == "" {
		return SourceOwnership{}, invalid("source region change arguments are invalid")
	}
	if params.ScopeTenantID != "" {
		if _, err := uuid.Parse(params.ScopeTenantID); err != nil {
			return SourceOwnership{}, invalid("source region tenant scope must be a UUID")
		}
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: change source region: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var ownership SourceOwnership
	var tenantID *string
	err = tx.QueryRow(ctx, `
SELECT ownership.source_id,ownership.home_region,ownership.active_region,ownership.epoch,
       ownership.state,ownership.changed_by,ownership.change_reason,ownership.promoted_at,source.tenant_id
FROM source_ownership ownership JOIN sources source ON source.id=ownership.source_id
WHERE ownership.source_id=$1 FOR UPDATE OF ownership,source`, params.SourceID).Scan(
		&ownership.SourceID, &ownership.HomeRegion, &ownership.ActiveRegion, &ownership.Epoch,
		&ownership.State, &ownership.ChangedBy, &ownership.ChangeReason, &ownership.PromotedAt, &tenantID)
	if err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: lock source ownership: %w", err)
	}
	if (tenantID == nil && params.ScopeTenantID != "") || (tenantID != nil && *tenantID != params.ScopeTenantID) {
		return SourceOwnership{}, authScopeError()
	}
	if ownership.Epoch != params.ExpectedEpoch {
		return SourceOwnership{}, fmt.Errorf("%w: source ownership epoch is %d", ErrLeaseLost, ownership.Epoch)
	}
	var healthy bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS(SELECT 1 FROM regions WHERE id=$1 AND enabled
              AND last_heartbeat_at >= statement_timestamp()-($2::double precision*interval '1 microsecond'))`,
		params.TargetRegion, params.HeartbeatMaxAge.Microseconds()).Scan(&healthy); err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: check target region health: %w", err)
	}
	if !healthy {
		return SourceOwnership{}, errors.New("postgres store: target region is not healthy")
	}
	state := "failover"
	if params.TargetRegion == ownership.HomeRegion {
		state = "home"
	}
	previousRegion, previousEpoch := ownership.ActiveRegion, ownership.Epoch
	var promotedAt *time.Time
	err = tx.QueryRow(ctx, `
UPDATE source_ownership SET active_region=$2,epoch=epoch+1,state=$3,changed_by=$4,
    change_reason=$5,promoted_at=statement_timestamp(),updated_at=statement_timestamp()
WHERE source_id=$1
RETURNING source_id,home_region,active_region,epoch,state,changed_by,change_reason,promoted_at`,
		params.SourceID, params.TargetRegion, state, params.ChangedBy, params.Reason).Scan(
		&ownership.SourceID, &ownership.HomeRegion, &ownership.ActiveRegion, &ownership.Epoch,
		&ownership.State, &ownership.ChangedBy, &ownership.ChangeReason, &promotedAt)
	if err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: update source ownership: %w", err)
	}
	ownership.PromotedAt = promotedAt
	if _, err := tx.Exec(ctx, `
UPDATE sources SET lease_owner=NULL,lease_token=NULL,lease_until=NULL,
    next_poll_at=LEAST(COALESCE(next_poll_at,statement_timestamp()),statement_timestamp()),updated_at=statement_timestamp()
WHERE id=$1`, params.SourceID); err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: fence previous source lease: %w", err)
	}
	if tenantID != nil {
		metadata, _ := json.Marshal(map[string]any{
			"from_region": previousRegion, "to_region": ownership.ActiveRegion,
			"previous_epoch": previousEpoch, "new_epoch": ownership.Epoch, "reason": params.Reason,
		})
		if _, err := appendAuditTx(ctx, tx, *tenantID, auditInput(AuditActor{Type: "user", ID: params.ChangedBy},
			"region.ownership.change", "source", ownership.SourceID, metadata)); err != nil {
			return SourceOwnership{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceOwnership{}, fmt.Errorf("postgres store: change source region: commit: %w", err)
	}
	return ownership, nil
}

func authScopeError() error {
	return errors.New("postgres store: source is outside the requested tenant scope")
}

func validRegion(regionID string) bool {
	return regionPattern.MatchString(strings.TrimSpace(regionID))
}
