package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/scheduler"
)

// A deadline is the successful resource's scheduled next check plus a bounded
// execution allowance. A failed attempt must never extend that deadline.
const collectionGrace = 2 * time.Minute

type CollectionStatus struct {
	FailureCode string               `json:"failure_code,omitempty"`
	State       string               `json:"state"`
	Reason      string               `json:"reason"`
	Mode        string               `json:"mode,omitempty"`
	FreshUntil  *time.Time           `json:"fresh_until,omitempty"`
	NextPollAt  *time.Time           `json:"next_poll_at,omitempty"`
	EvaluatedAt time.Time            `json:"evaluated_at"`
	Resources   []CollectionResource `json:"resources,omitempty"`
}

type CollectionResource struct {
	Kind           string     `json:"kind"`
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty"`
	NextPollAt     *time.Time `json:"next_poll_at,omitempty"`
	FreshUntil     *time.Time `json:"fresh_until,omitempty"`
	ScheduleReason string     `json:"schedule_reason"`
	State          string     `json:"state"`
}

type collectionCheckpoint struct {
	Version int `json:"version"`
	Cadence struct {
		Mode string `json:"mode"`
	} `json:"cadence"`
	Capabilities struct {
		Endpoints map[string]json.RawMessage `json:"endpoints"`
	} `json:"capabilities"`
	Resources map[string]struct {
		LastSuccessAt  *time.Time `json:"last_success_at"`
		NextPollAt     *time.Time `json:"next_poll_at"`
		ScheduleReason string     `json:"schedule_reason"`
	} `json:"resources"`
}

func sourceCollection(source SourceView, checkpoint *string, now time.Time) CollectionStatus {
	out := CollectionStatus{State: "unknown", Reason: "awaiting_first_success", EvaluatedAt: now, NextPollAt: source.NextPollAt}
	if !source.Enabled {
		out.State, out.Reason, out.NextPollAt = "disabled", "disabled", nil
		return out
	}
	var state collectionCheckpoint
	if checkpoint == nil || json.Unmarshal([]byte(*checkpoint), &state) != nil || state.Version != 1 {
		out.Reason = "awaiting_resource_checkpoint"
	} else {
		out.Mode = state.Cadence.Mode
		out.State, out.Reason = "fresh", "on_schedule"
		for _, profile := range scheduler.StatuspageCadences() {
			kind := string(profile.Resource)
			if _, available := state.Capabilities.Endpoints[kind]; !available {
				continue
			}
			r := state.Resources[kind]
			// Only scheduled collector resources participate (push-only capabilities
			// and unsupported resources must not invent polling deadlines).
			item := CollectionResource{Kind: kind, LastSuccessAt: r.LastSuccessAt, NextPollAt: r.NextPollAt, ScheduleReason: r.ScheduleReason, State: "unknown"}
			if item.ScheduleReason == "" {
				item.ScheduleReason = "unknown"
			}
			if r.LastSuccessAt != nil && r.NextPollAt != nil {
				deadline := r.NextPollAt.Add(collectionGrace)
				item.FreshUntil, item.State = &deadline, "fresh"
				if !now.Before(deadline) {
					item.State = "stale"
				}
				if out.FreshUntil == nil || deadline.Before(*out.FreshUntil) {
					out.FreshUntil = &deadline
				}
			}
			if item.State == "stale" {
				out.State, out.Reason = "stale", "resource_overdue"
			} else if item.State == "unknown" && out.State != "stale" {
				out.State, out.Reason = "unknown", "awaiting_resource_checkpoint"
			}
			out.Resources = append(out.Resources, item)
		}
		sort.Slice(out.Resources, func(i, j int) bool { return out.Resources[i].Kind < out.Resources[j].Kind })
		if len(out.Resources) == 0 {
			out.State, out.Reason = "unknown", "awaiting_resource_checkpoint"
		}
	}
	if source.LastSuccessAt == nil {
		out.State, out.Reason = "unknown", "awaiting_first_success"
	}
	if source.FailureStreak > 0 {
		out.Mode, out.Reason = "backoff", "failure_backoff"
	}
	if source.HealthState == "degraded" {
		out.State, out.Reason = "stale", "collection_degraded"
	}
	return out
}

type collectionRow struct {
	source     SourceView
	checkpoint *string
}

// One tenant-filtered query per list, never one query per vendor/source.
func readCollections(ctx context.Context, tx pgx.Tx, tenantID string, ids []string) (map[string]collectionRow, error) {
	rows, err := tx.Query(ctx, `SELECT id,vendor_id,enabled,health_state,failure_streak,last_success_at,next_poll_at,COALESCE(last_failure_code,''),
 CASE WHEN last_checkpoint IS NULL OR NOT pg_input_is_valid(last_checkpoint,'jsonb') THEN NULL ELSE jsonb_build_object(
 'version',last_checkpoint::jsonb->'version', 'cadence',last_checkpoint::jsonb->'cadence',
 'resources',last_checkpoint::jsonb->'resources',
 'capabilities',jsonb_build_object('endpoints',last_checkpoint::jsonb->'capabilities'->'endpoints'))::text END
 FROM sources WHERE (tenant_id IS NULL OR tenant_id=$1) AND ($2::uuid[] IS NULL OR id=ANY($2::uuid[]))`, tenantID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]collectionRow)
	now := time.Now().UTC()
	for rows.Next() {
		var row collectionRow
		var failure string
		s := &row.source
		if err := rows.Scan(&s.ID, &s.VendorID, &s.Enabled, &s.HealthState, &s.FailureStreak, &s.LastSuccessAt, &s.NextPollAt, &failure, &row.checkpoint); err != nil {
			return nil, err
		}
		s.Collection = sourceCollection(*s, row.checkpoint, now)
		if s.FailureStreak > 0 {
			s.Collection.FailureCode = failure
		}
		result[s.ID] = row
	}
	return result, rows.Err()
}

func vendorCollection(rows []SourceView, now time.Time) CollectionStatus {
	out := CollectionStatus{State: "disabled", Reason: "no_enabled_sources", EvaluatedAt: now}
	rank := map[string]int{"disabled": 0, "fresh": 1, "unknown": 2, "stale": 3}
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		c := row.Collection
		if rank[c.State] > rank[out.State] {
			out.State, out.Reason = c.State, c.Reason
		}
		if c.FreshUntil != nil && (out.FreshUntil == nil || c.FreshUntil.Before(*out.FreshUntil)) {
			out.FreshUntil = c.FreshUntil
		}
		if c.NextPollAt != nil && (out.NextPollAt == nil || c.NextPollAt.Before(*out.NextPollAt)) {
			out.NextPollAt = c.NextPollAt
		}
	}
	return out
}
