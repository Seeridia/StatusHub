package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/reconcile"
)

// Project the entire accepted state, including the initial baseline and HTTP
// 304 polls. Change events alone cannot populate these read models: baseline
// observations intentionally do not emit notifications.
func projectReconciledState(ctx context.Context, tx pgx.Tx, state reconcile.State) error {
	batch := &pgx.Batch{}
	source := uuid.MustParse(state.SourceID)
	for key, aggregate := range state.Components {
		c := aggregate.Component
		upstream := c.UpstreamID
		if upstream == "" {
			upstream = key
		}
		id := uuid.NewSHA1(source, []byte("component/"+upstream)).String()
		tags := make([]string, 0, len(c.Tags))
		for k, v := range c.Tags {
			tags = append(tags, k+"="+v)
		}
		sort.Strings(tags)
		batch.Queue(`INSERT INTO components(id,source_id,upstream_id,name,canonical_status,raw_status,group_upstream_id,tags,revision,source_updated_at,observed_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT(source_id,upstream_id) DO UPDATE SET name=EXCLUDED.name,canonical_status=EXCLUDED.canonical_status,
raw_status=EXCLUDED.raw_status,group_upstream_id=EXCLUDED.group_upstream_id,tags=EXCLUDED.tags,revision=EXCLUDED.revision,
source_updated_at=EXCLUDED.source_updated_at,observed_at=EXCLUDED.observed_at,updated_at=now()
WHERE components.revision <= EXCLUDED.revision`, id, state.SourceID, upstream, c.Name, c.Status, c.RawStatus, c.GroupID, tags, int64(aggregate.Revision), c.SourceUpdatedAt, aggregate.Watermark.ObservedAt)
	}
	for key, aggregate := range state.Incidents {
		i := aggregate.Incident
		upstream := i.UpstreamID
		if upstream == "" {
			upstream = key
		}
		id := uuid.NewSHA1(source, []byte("incident/"+upstream)).String()
		batch.Queue(`INSERT INTO incidents(id,source_id,upstream_id,name,canonical_phase,raw_phase,canonical_impact,raw_impact,started_at,resolved_at,revision,source_updated_at,observed_at,updated_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,COALESCE($12::timestamptz,$13::timestamptz))
ON CONFLICT(source_id,upstream_id) DO UPDATE SET name=EXCLUDED.name,canonical_phase=EXCLUDED.canonical_phase,raw_phase=EXCLUDED.raw_phase,
canonical_impact=EXCLUDED.canonical_impact,raw_impact=EXCLUDED.raw_impact,started_at=EXCLUDED.started_at,resolved_at=EXCLUDED.resolved_at,
revision=EXCLUDED.revision,source_updated_at=EXCLUDED.source_updated_at,observed_at=EXCLUDED.observed_at,
updated_at=CASE WHEN incidents.revision < EXCLUDED.revision THEN EXCLUDED.updated_at ELSE incidents.updated_at END
WHERE incidents.revision <= EXCLUDED.revision`, id, state.SourceID, upstream, i.Name, i.Phase, i.RawPhase, i.Impact, i.RawImpact, i.StartedAt, i.ResolvedAt, int64(aggregate.Revision), i.SourceUpdatedAt, aggregate.Watermark.ObservedAt)
		for _, u := range i.Updates {
			if u.ID == "" && u.Phase == "" && u.Body == "" {
				continue
			}
			encoded, err := json.Marshal(u)
			if err != nil {
				return err
			}
			eventKey := fmt.Sprintf("%x", sha256.Sum256(encoded))
			updateID := uuid.NewSHA1(source, []byte("update/"+upstream+"/"+eventKey)).String()
			// Select the stored incident ID to support databases populated before this projection.
			batch.Queue(`INSERT INTO incident_updates(id,incident_id,upstream_id,source_event_key,canonical_phase,raw_phase,body,source_updated_at,observed_at)
SELECT $1,id,$4,$5,$6,$7,$8,$9,$10 FROM incidents WHERE source_id=$2 AND upstream_id=$3
ON CONFLICT(incident_id,source_event_key) DO NOTHING`, updateID, state.SourceID, upstream, u.UpstreamID, eventKey, u.Phase, u.RawPhase, u.Body, u.SourceUpdatedAt, aggregate.Watermark.ObservedAt)
		}
	}
	if batch.Len() == 0 {
		return nil
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("postgres store: project poll state: %w", err)
	}
	return nil
}
