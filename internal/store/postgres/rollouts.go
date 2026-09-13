package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/adapter/shadow"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AdapterRollout struct {
	shadow.Rollout
	BaselineAdapterName    string     `json:"baseline_adapter_name"`
	BaselineAdapterVersion string     `json:"baseline_adapter_version"`
	State                  string     `json:"state"`
	CreatedBy              string     `json:"created_by"`
	DecisionReason         string     `json:"decision_reason,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	DecidedAt              *time.Time `json:"decided_at,omitempty"`
}

type CreateAdapterRolloutParams struct {
	ID                      string
	SourceID                string
	ScopeTenantID           string
	CandidateAdapterName    string
	CandidateAdapterVersion string
	SampleRate              float64
	MinimumSamples          int
	MaximumMismatchRate     float64
	MaximumErrorRate        float64
	CreatedBy               string
}

type AdapterRolloutStats struct {
	Total              int64   `json:"total"`
	Equivalent         int64   `json:"equivalent"`
	Mismatches         int64   `json:"mismatches"`
	Errors             int64   `json:"errors"`
	MismatchRate       float64 `json:"mismatch_rate"`
	ErrorRate          float64 `json:"error_rate"`
	PrimaryP95Millis   float64 `json:"primary_p95_ms"`
	CandidateP95Millis float64 `json:"candidate_p95_ms"`
}

type AdapterRolloutView struct {
	Rollout    AdapterRollout      `json:"rollout"`
	Statistics AdapterRolloutStats `json:"statistics"`
}

type DecideAdapterRolloutParams struct {
	RolloutID     string
	ScopeTenantID string
	ChangedBy     string
	Reason        string
}

func (s *Store) CreateAdapterRollout(ctx context.Context, params CreateAdapterRolloutParams) (AdapterRollout, error) {
	if err := s.ready(); err != nil {
		return AdapterRollout{}, err
	}
	if params.ID == "" {
		params.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(params.ID); err != nil {
		return AdapterRollout{}, invalid("adapter rollout ID must be a UUID")
	}
	if _, err := uuid.Parse(params.SourceID); err != nil || strings.TrimSpace(params.CandidateAdapterName) == "" ||
		strings.TrimSpace(params.CandidateAdapterVersion) == "" || strings.TrimSpace(params.CreatedBy) == "" ||
		params.SampleRate <= 0 || params.SampleRate > 1 || params.MinimumSamples <= 0 ||
		params.MaximumMismatchRate < 0 || params.MaximumMismatchRate > 1 || params.MaximumErrorRate < 0 || params.MaximumErrorRate > 1 {
		return AdapterRollout{}, invalid("adapter rollout arguments are invalid")
	}
	if params.ScopeTenantID != "" {
		if _, err := uuid.Parse(params.ScopeTenantID); err != nil {
			return AdapterRollout{}, invalid("adapter rollout tenant scope must be a UUID")
		}
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AdapterRollout{}, fmt.Errorf("postgres store: create adapter rollout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var baselineName, baselineVersion string
	var tenantID *string
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(adapter_name,''),COALESCE(adapter_version,''),tenant_id
FROM sources WHERE id=$1 FOR UPDATE`, params.SourceID).Scan(&baselineName, &baselineVersion, &tenantID); err != nil {
		return AdapterRollout{}, fmt.Errorf("postgres store: read rollout source: %w", err)
	}
	if baselineName == "" {
		return AdapterRollout{}, errors.New("postgres store: source has no baseline adapter")
	}
	if !tenantScopeMatches(tenantID, params.ScopeTenantID) {
		return AdapterRollout{}, authScopeError()
	}
	rollout := AdapterRollout{Rollout: shadow.Rollout{ID: params.ID, SourceID: params.SourceID,
		CandidateAdapterName: strings.TrimSpace(params.CandidateAdapterName), CandidateAdapterVersion: strings.TrimSpace(params.CandidateAdapterVersion),
		SampleRate: params.SampleRate, MinimumSamples: params.MinimumSamples,
		MaximumMismatchRate: params.MaximumMismatchRate, MaximumErrorRate: params.MaximumErrorRate},
		BaselineAdapterName: baselineName, BaselineAdapterVersion: baselineVersion, State: "shadow", CreatedBy: params.CreatedBy}
	err = tx.QueryRow(ctx, `
INSERT INTO adapter_rollouts(id,source_id,baseline_adapter_name,baseline_adapter_version,
    candidate_adapter_name,candidate_adapter_version,sample_rate,minimum_samples,
    maximum_mismatch_rate,maximum_error_rate,created_by)
VALUES($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT DO NOTHING
RETURNING created_at`, rollout.ID, rollout.SourceID, rollout.BaselineAdapterName, rollout.BaselineAdapterVersion,
		rollout.CandidateAdapterName, rollout.CandidateAdapterVersion, rollout.SampleRate, rollout.MinimumSamples,
		rollout.MaximumMismatchRate, rollout.MaximumErrorRate, rollout.CreatedBy).Scan(&rollout.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
SELECT rollout.id,rollout.source_id,rollout.baseline_adapter_name,COALESCE(rollout.baseline_adapter_version,''),
       rollout.candidate_adapter_name,rollout.candidate_adapter_version,rollout.state,rollout.sample_rate,
       rollout.minimum_samples,rollout.maximum_mismatch_rate,rollout.maximum_error_rate,rollout.created_by,
       COALESCE(rollout.decision_reason,''),rollout.created_at,rollout.decided_at
FROM adapter_rollouts rollout JOIN sources source ON source.id=rollout.source_id
WHERE rollout.id=$1 AND rollout.source_id=$2
  AND (($3='' AND source.tenant_id IS NULL) OR source.tenant_id::text=$3)`, params.ID, params.SourceID, params.ScopeTenantID).Scan(
			&rollout.ID, &rollout.SourceID, &rollout.BaselineAdapterName, &rollout.BaselineAdapterVersion,
			&rollout.CandidateAdapterName, &rollout.CandidateAdapterVersion, &rollout.State, &rollout.SampleRate,
			&rollout.MinimumSamples, &rollout.MaximumMismatchRate, &rollout.MaximumErrorRate,
			&rollout.CreatedBy, &rollout.DecisionReason, &rollout.CreatedAt, &rollout.DecidedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return AdapterRollout{}, ErrConflict
		}
		if err != nil {
			return AdapterRollout{}, fmt.Errorf("postgres store: read existing adapter rollout: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return AdapterRollout{}, fmt.Errorf("postgres store: existing adapter rollout: commit: %w", err)
		}
		return rollout, nil
	}
	if err != nil {
		return AdapterRollout{}, fmt.Errorf("postgres store: create adapter rollout: %w", err)
	}
	if tenantID != nil {
		metadata, _ := json.Marshal(map[string]any{"candidate": rollout.CandidateAdapterName, "version": rollout.CandidateAdapterVersion})
		if _, err := appendAuditTx(ctx, tx, *tenantID, auditInput(AuditActor{Type: "user", ID: params.CreatedBy},
			"adapter.rollout.create", "adapter_rollout", rollout.ID, metadata)); err != nil {
			return AdapterRollout{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AdapterRollout{}, fmt.Errorf("postgres store: create adapter rollout: commit: %w", err)
	}
	return rollout, nil
}

func (s *Store) ActiveAdapterRollout(ctx context.Context, sourceID string) (shadow.Rollout, bool, error) {
	if err := s.ready(); err != nil {
		return shadow.Rollout{}, false, err
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		return shadow.Rollout{}, false, invalid("adapter rollout source ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return shadow.Rollout{}, false, fmt.Errorf("postgres store: read active adapter rollout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var rollout shadow.Rollout
	err = tx.QueryRow(ctx, `
SELECT id,source_id,candidate_adapter_name,candidate_adapter_version,sample_rate,
       minimum_samples,maximum_mismatch_rate,maximum_error_rate
FROM adapter_rollouts WHERE source_id=$1 AND state='shadow'`, sourceID).Scan(
		&rollout.ID, &rollout.SourceID, &rollout.CandidateAdapterName, &rollout.CandidateAdapterVersion,
		&rollout.SampleRate, &rollout.MinimumSamples, &rollout.MaximumMismatchRate, &rollout.MaximumErrorRate)
	if errors.Is(err, pgx.ErrNoRows) {
		return shadow.Rollout{}, false, nil
	}
	if err != nil {
		return shadow.Rollout{}, false, fmt.Errorf("postgres store: read active adapter rollout: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return shadow.Rollout{}, false, fmt.Errorf("postgres store: read active adapter rollout: commit: %w", err)
	}
	return rollout, true, nil
}

func (s *Store) RecordAdapterComparison(ctx context.Context, comparison shadow.Comparison) error {
	if err := s.ready(); err != nil {
		return err
	}
	if comparison.ID == "" {
		comparison.ID = uuid.NewString()
	}
	if _, err := uuid.Parse(comparison.ID); err != nil {
		return invalid("adapter comparison ID must be a UUID")
	}
	if _, err := uuid.Parse(comparison.RolloutID); err != nil {
		return invalid("adapter comparison rollout ID must be a UUID")
	}
	if _, err := uuid.Parse(comparison.SourceID); err != nil || comparison.ResourceKind == "" || comparison.PrimaryDuration < 0 || comparison.CandidateDuration < 0 {
		return invalid("adapter comparison arguments are invalid")
	}
	if len(comparison.PrimaryDigest) != 32 || (len(comparison.CandidateDigest) != 0 && len(comparison.CandidateDigest) != 32) {
		return invalid("adapter comparison digests are invalid")
	}
	if comparison.ObservedAt.IsZero() {
		comparison.ObservedAt = time.Now().UTC()
	}
	if len(comparison.Difference) == 0 {
		comparison.Difference = json.RawMessage(`{}`)
	}
	if !json.Valid(comparison.Difference) || len(comparison.Difference) > 64<<10 {
		return invalid("adapter comparison difference must be valid JSON up to 64 KiB")
	}
	tag, err := s.db.Exec(ctx, `
INSERT INTO adapter_shadow_comparisons(id,rollout_id,source_id,resource_kind,observed_at,
    primary_digest,candidate_digest,equivalent,candidate_error,primary_duration_ms,candidate_duration_ms,difference)
SELECT $1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12
FROM adapter_rollouts rollout
WHERE rollout.id=$2 AND rollout.source_id=$3 AND rollout.state='shadow'`, comparison.ID,
		comparison.RolloutID, comparison.SourceID, string(comparison.ResourceKind), comparison.ObservedAt,
		comparison.PrimaryDigest, nullableBytes(comparison.CandidateDigest), comparison.Equivalent,
		truncateStore(comparison.CandidateError, 1024), comparison.PrimaryDuration.Milliseconds(),
		comparison.CandidateDuration.Milliseconds(), comparison.Difference)
	if err != nil {
		return fmt.Errorf("postgres store: record adapter comparison: %w", err)
	}
	// A late shadow result racing with promote/rollback is intentionally dropped.
	_ = tag
	return nil
}

func (s *Store) AdapterRolloutStatistics(ctx context.Context, rolloutID string) (AdapterRolloutStats, error) {
	if err := s.ready(); err != nil {
		return AdapterRolloutStats{}, err
	}
	if _, err := uuid.Parse(rolloutID); err != nil {
		return AdapterRolloutStats{}, invalid("adapter rollout ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: adapter rollout statistics: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stats, err := rolloutStatistics(ctx, tx, rolloutID)
	if err != nil {
		return AdapterRolloutStats{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: adapter rollout statistics: commit: %w", err)
	}
	return stats, nil
}

func (s *Store) TenantAdapterRollout(ctx context.Context, tenantID, rolloutID string) (AdapterRolloutView, error) {
	if err := s.ready(); err != nil {
		return AdapterRolloutView{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return AdapterRolloutView{}, invalid("adapter rollout tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(rolloutID); err != nil {
		return AdapterRolloutView{}, invalid("adapter rollout ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: tenant adapter rollout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var view AdapterRolloutView
	err = tx.QueryRow(ctx, `
SELECT rollout.id,rollout.source_id,rollout.baseline_adapter_name,COALESCE(rollout.baseline_adapter_version,''),
       rollout.candidate_adapter_name,rollout.candidate_adapter_version,rollout.state,rollout.sample_rate,
       rollout.minimum_samples,rollout.maximum_mismatch_rate,rollout.maximum_error_rate,rollout.created_by,
       COALESCE(rollout.decision_reason,''),rollout.created_at,rollout.decided_at
FROM adapter_rollouts rollout JOIN sources source ON source.id=rollout.source_id
WHERE rollout.id=$2 AND source.tenant_id=$1`, tenantID, rolloutID).Scan(
		&view.Rollout.ID, &view.Rollout.SourceID, &view.Rollout.BaselineAdapterName,
		&view.Rollout.BaselineAdapterVersion, &view.Rollout.CandidateAdapterName,
		&view.Rollout.CandidateAdapterVersion, &view.Rollout.State, &view.Rollout.SampleRate,
		&view.Rollout.MinimumSamples, &view.Rollout.MaximumMismatchRate, &view.Rollout.MaximumErrorRate,
		&view.Rollout.CreatedBy, &view.Rollout.DecisionReason, &view.Rollout.CreatedAt, &view.Rollout.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdapterRolloutView{}, ErrNotFound
	}
	if err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: tenant adapter rollout: %w", err)
	}
	view.Statistics, err = rolloutStatistics(ctx, tx, rolloutID)
	if err != nil {
		return AdapterRolloutView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: tenant adapter rollout: commit: %w", err)
	}
	return view, nil
}

func (s *Store) TenantLatestAdapterRollout(ctx context.Context, tenantID, sourceID string) (AdapterRolloutView, error) {
	if err := s.ready(); err != nil {
		return AdapterRolloutView{}, err
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		return AdapterRolloutView{}, invalid("adapter rollout tenant ID must be a UUID")
	}
	if _, err := uuid.Parse(sourceID); err != nil {
		return AdapterRolloutView{}, invalid("adapter rollout source ID must be a UUID")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: latest tenant adapter rollout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var view AdapterRolloutView
	err = tx.QueryRow(ctx, `
SELECT rollout.id,rollout.source_id,rollout.baseline_adapter_name,COALESCE(rollout.baseline_adapter_version,''),
       rollout.candidate_adapter_name,rollout.candidate_adapter_version,rollout.state,rollout.sample_rate,
       rollout.minimum_samples,rollout.maximum_mismatch_rate,rollout.maximum_error_rate,rollout.created_by,
       COALESCE(rollout.decision_reason,''),rollout.created_at,rollout.decided_at
FROM adapter_rollouts rollout JOIN sources source ON source.id=rollout.source_id
WHERE source.id=$2 AND source.tenant_id=$1
ORDER BY rollout.created_at DESC,rollout.id DESC LIMIT 1`, tenantID, sourceID).Scan(
		&view.Rollout.ID, &view.Rollout.SourceID, &view.Rollout.BaselineAdapterName,
		&view.Rollout.BaselineAdapterVersion, &view.Rollout.CandidateAdapterName,
		&view.Rollout.CandidateAdapterVersion, &view.Rollout.State, &view.Rollout.SampleRate,
		&view.Rollout.MinimumSamples, &view.Rollout.MaximumMismatchRate, &view.Rollout.MaximumErrorRate,
		&view.Rollout.CreatedBy, &view.Rollout.DecisionReason, &view.Rollout.CreatedAt, &view.Rollout.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdapterRolloutView{}, ErrNotFound
	}
	if err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: latest tenant adapter rollout: %w", err)
	}
	view.Statistics, err = rolloutStatistics(ctx, tx, view.Rollout.ID)
	if err != nil {
		return AdapterRolloutView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdapterRolloutView{}, fmt.Errorf("postgres store: latest tenant adapter rollout: commit: %w", err)
	}
	return view, nil
}

func rolloutStatistics(ctx context.Context, tx pgx.Tx, rolloutID string) (AdapterRolloutStats, error) {
	var stats AdapterRolloutStats
	err := tx.QueryRow(ctx, `
	SELECT count(comparison.id),count(comparison.id) FILTER (WHERE comparison.equivalent AND comparison.candidate_error IS NULL),
	       count(comparison.id) FILTER (WHERE NOT comparison.equivalent AND comparison.candidate_error IS NULL),
	       count(comparison.id) FILTER (WHERE comparison.candidate_error IS NOT NULL),
	       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY comparison.primary_duration_ms) FILTER (WHERE comparison.id IS NOT NULL),0),
	       COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY comparison.candidate_duration_ms) FILTER (WHERE comparison.id IS NOT NULL),0)
	FROM adapter_rollouts rollout
	LEFT JOIN adapter_shadow_comparisons comparison ON comparison.rollout_id=rollout.id
	WHERE rollout.id=$1 GROUP BY rollout.id`, rolloutID).Scan(
		&stats.Total, &stats.Equivalent, &stats.Mismatches, &stats.Errors,
		&stats.PrimaryP95Millis, &stats.CandidateP95Millis)
	if err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: adapter rollout statistics: %w", err)
	}
	if stats.Total > 0 {
		stats.ErrorRate = float64(stats.Errors) / float64(stats.Total)
	}
	successful := stats.Total - stats.Errors
	if successful > 0 {
		stats.MismatchRate = float64(stats.Mismatches) / float64(successful)
	}
	return stats, nil
}

func (s *Store) PromoteAdapterRollout(ctx context.Context, params DecideAdapterRolloutParams) (AdapterRolloutStats, error) {
	return s.decideAdapterRollout(ctx, params, true)
}

func (s *Store) RollbackAdapterRollout(ctx context.Context, params DecideAdapterRolloutParams) (AdapterRolloutStats, error) {
	return s.decideAdapterRollout(ctx, params, false)
}

func (s *Store) decideAdapterRollout(ctx context.Context, params DecideAdapterRolloutParams, promote bool) (AdapterRolloutStats, error) {
	if err := s.ready(); err != nil {
		return AdapterRolloutStats{}, err
	}
	if _, err := uuid.Parse(params.RolloutID); err != nil || strings.TrimSpace(params.ChangedBy) == "" || strings.TrimSpace(params.Reason) == "" {
		return AdapterRolloutStats{}, invalid("adapter rollout decision arguments are invalid")
	}
	if params.ScopeTenantID != "" {
		if _, err := uuid.Parse(params.ScopeTenantID); err != nil {
			return AdapterRolloutStats{}, invalid("adapter rollout tenant scope must be a UUID")
		}
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: decide adapter rollout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var rollout AdapterRollout
	var tenantID *string
	err = tx.QueryRow(ctx, `
SELECT rollout.id,rollout.source_id,rollout.baseline_adapter_name,COALESCE(rollout.baseline_adapter_version,''),
       rollout.candidate_adapter_name,rollout.candidate_adapter_version,rollout.state,
       rollout.sample_rate,rollout.minimum_samples,rollout.maximum_mismatch_rate,rollout.maximum_error_rate,
       source.tenant_id
FROM adapter_rollouts rollout JOIN sources source ON source.id=rollout.source_id
WHERE rollout.id=$1 FOR UPDATE OF rollout,source`, params.RolloutID).Scan(
		&rollout.ID, &rollout.SourceID, &rollout.BaselineAdapterName, &rollout.BaselineAdapterVersion,
		&rollout.CandidateAdapterName, &rollout.CandidateAdapterVersion, &rollout.State,
		&rollout.SampleRate, &rollout.MinimumSamples, &rollout.MaximumMismatchRate,
		&rollout.MaximumErrorRate, &tenantID)
	if err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: lock adapter rollout: %w", err)
	}
	if !tenantScopeMatches(tenantID, params.ScopeTenantID) {
		return AdapterRolloutStats{}, authScopeError()
	}
	stats, err := rolloutStatistics(ctx, tx, params.RolloutID)
	if err != nil {
		return AdapterRolloutStats{}, err
	}
	if promote {
		if rollout.State != "shadow" {
			return AdapterRolloutStats{}, fmt.Errorf("%w: only a shadow rollout can be promoted", ErrConflict)
		}
		if stats.Total < int64(rollout.MinimumSamples) || stats.MismatchRate > rollout.MaximumMismatchRate || stats.ErrorRate > rollout.MaximumErrorRate {
			return AdapterRolloutStats{}, fmt.Errorf("%w: rollout quality gate failed: samples=%d mismatch_rate=%.6f error_rate=%.6f", ErrConflict, stats.Total, stats.MismatchRate, stats.ErrorRate)
		}
		if _, err := tx.Exec(ctx, `UPDATE sources SET adapter_name=$2,adapter_version=$3,updated_at=statement_timestamp() WHERE id=$1`,
			rollout.SourceID, rollout.CandidateAdapterName, rollout.CandidateAdapterVersion); err != nil {
			return AdapterRolloutStats{}, fmt.Errorf("postgres store: promote source adapter: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE adapter_rollouts SET state='promoted',decision_reason=$2,decided_at=statement_timestamp(),updated_at=statement_timestamp() WHERE id=$1`,
			rollout.ID, params.Reason); err != nil {
			return AdapterRolloutStats{}, fmt.Errorf("postgres store: mark adapter rollout promoted: %w", err)
		}
	} else {
		if rollout.State == "rolled_back" {
			return AdapterRolloutStats{}, fmt.Errorf("%w: adapter rollout is already rolled back", ErrConflict)
		}
		if rollout.State == "promoted" {
			if _, err := tx.Exec(ctx, `UPDATE sources SET adapter_name=$2,adapter_version=NULLIF($3,''),updated_at=statement_timestamp() WHERE id=$1`,
				rollout.SourceID, rollout.BaselineAdapterName, rollout.BaselineAdapterVersion); err != nil {
				return AdapterRolloutStats{}, fmt.Errorf("postgres store: restore baseline adapter: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE adapter_rollouts SET state='rolled_back',decision_reason=$2,decided_at=statement_timestamp(),updated_at=statement_timestamp() WHERE id=$1`,
			rollout.ID, params.Reason); err != nil {
			return AdapterRolloutStats{}, fmt.Errorf("postgres store: mark adapter rollout rolled back: %w", err)
		}
	}
	if tenantID != nil {
		action := "adapter.rollout.rollback"
		if promote {
			action = "adapter.rollout.promote"
		}
		metadata, _ := json.Marshal(map[string]any{"reason": params.Reason, "statistics": stats})
		if _, err := appendAuditTx(ctx, tx, *tenantID, auditInput(AuditActor{Type: "user", ID: params.ChangedBy}, action, "adapter_rollout", rollout.ID, metadata)); err != nil {
			return AdapterRolloutStats{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return AdapterRolloutStats{}, fmt.Errorf("postgres store: decide adapter rollout: commit: %w", err)
	}
	return stats, nil
}

func tenantScopeMatches(tenantID *string, scope string) bool {
	if tenantID == nil {
		return scope == ""
	}
	return *tenantID == scope
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func truncateStore(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}
