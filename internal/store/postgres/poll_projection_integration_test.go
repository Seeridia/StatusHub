package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/reconcile"
)

func TestIntegrationPollProjectsBaselineAndFencesReadModels(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	db.insertSource(t, integrationSourceID, now.Add(-time.Minute))
	state := reconcile.State{SourceID: integrationSourceID, BaselineEstablished: true,
		Components: map[string]reconcile.ComponentState{"api": {Component: domain.Component{ID: "api", Name: "API", Status: domain.ComponentStatus("degraded")}, Revision: 1, Watermark: reconcile.Watermark{ObservedAt: now}}},
		Incidents:  map[string]reconcile.IncidentState{"incident": {Incident: domain.Incident{ID: "incident", Name: "Upstream outage", Phase: domain.IncidentPhaseInvestigating, Impact: domain.Impact("major"), Updates: []domain.IncidentUpdate{{ID: "update", Body: "Investigating errors", Phase: domain.IncidentPhaseInvestigating}}}, Revision: 1, Watermark: reconcile.Watermark{ObservedAt: now}}}}
	acquire := func() SourceLease {
		t.Helper()
		if _, err := db.pool.Exec(ctx, `UPDATE sources SET next_poll_at=now()-interval '1 second' WHERE id=$1`, integrationSourceID); err != nil {
			t.Fatal(err)
		}
		leases, err := db.store.AcquireDueSources(ctx, "projection-test", 1, time.Minute)
		if err != nil || len(leases) != 1 {
			t.Fatalf("lease: %v %v", leases, err)
		}
		return leases[0]
	}
	commit := func(lease SourceLease) error {
		_, err := db.store.CommitPoll(ctx, CommitPollParams{SourceID: integrationSourceID, LeaseToken: lease.LeaseToken, NextPollAt: now.Add(time.Minute), Checkpoint: `{"version":1}`, HealthState: "healthy", SuccessfulAt: now, State: &state})
		return err
	}
	first := acquire()
	if err := commit(first); err != nil {
		t.Fatal(err)
	}
	// Baselines have no canonical events, yet the console must show their state.
	for table, want := range map[string]int{"components": 1, "incidents": 1, "incident_updates": 1, "canonical_events": 0} {
		var count int
		if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	if err := commit(acquire()); err != nil {
		t.Fatal(err)
	}
	var updates int
	_ = db.pool.QueryRow(ctx, `SELECT count(*) FROM incident_updates`).Scan(&updates)
	if updates != 1 {
		t.Fatalf("duplicate timeline: %d", updates)
	}
	aggregate := state.Incidents["incident"]
	aggregate.Incident.Phase = domain.IncidentPhaseResolved
	aggregate.Revision = 2
	state.Incidents["incident"] = aggregate
	if err := commit(first); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale lease: %v", err)
	}
	var phase string
	_ = db.pool.QueryRow(ctx, `SELECT canonical_phase FROM incidents`).Scan(&phase)
	if phase != "investigating" {
		t.Fatalf("stale writer changed phase: %s", phase)
	}
	if err := commit(acquire()); err != nil {
		t.Fatal(err)
	}
	_ = db.pool.QueryRow(ctx, `SELECT canonical_phase FROM incidents`).Scan(&phase)
	if phase != "resolved" {
		t.Fatalf("recovery not projected: %s", phase)
	}
}
