package postgres

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestIntegrationCollectionProjectionIsolation(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	_, err := db.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'a','A'),($2,'b','B')`, m5TenantA, m5TenantB)
	if err != nil {
		t.Fatal(err)
	}
	db.insertSource(t, integrationSourceID, time.Now().Add(time.Minute))
	checkpoint := `{"version":1,"capabilities":{"endpoints":{"summary":{}}},"resources":{}}`
	_, err = db.pool.Exec(ctx, `UPDATE sources SET tenant_id=$1,last_checkpoint=$2,failure_streak=1,last_failure_code='rate_limited' WHERE id=$3`, m5TenantA, checkpoint, integrationSourceID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.store.ListSources(ctx, m5TenantA, nil, 30)
	if err != nil || len(a) != 1 || a[0].Collection.State != "unknown" || len(a[0].Collection.Resources) != 1 {
		t.Fatalf("projection: %+v %v", a, err)
	}
	b, err := db.store.ListSources(ctx, m5TenantB, nil, 30)
	if a[0].Collection.FailureCode != "rate_limited" {
		t.Fatalf("failure diagnostic missing: %+v", a[0].Collection)
	}
	if err != nil || len(b) != 0 {
		t.Fatalf("private source leaked: %+v %v", b, err)
	}
	vendors, err := db.store.ListVendorStatuses(ctx, m5TenantB)
	if err != nil {
		t.Fatal(err)
	}
	for _, vendor := range vendors {
		if vendor.ID == integrationVendorID && vendor.Collection.State != "disabled" {
			t.Fatalf("private freshness leaked: %+v", vendor)
		}
	}
	_, err = db.pool.Exec(ctx, `UPDATE sources SET last_checkpoint='invalid legacy data' WHERE id=$1`, integrationSourceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.store.Source(ctx, m5TenantA, integrationSourceID); err != nil {
		t.Fatalf("invalid checkpoint broke UI: %v", err)
	}
}

func TestCollectionDeadlinesAndBackoff(t *testing.T) {
	now := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	success, next := now.Add(-10*time.Minute), now.Add(5*time.Minute)
	cp := func(last *time.Time, due time.Time) *string {
		b, _ := json.Marshal(map[string]any{"version": 1, "capabilities": map[string]any{"endpoints": map[string]any{"scheduled_maintenances": map[string]any{}}}, "resources": map[string]any{"scheduled_maintenances": map[string]any{"last_success_at": last, "next_poll_at": due}}})
		s := string(b)
		return &s
	}
	source := SourceView{Enabled: true, LastSuccessAt: &success, NextPollAt: &next, HealthState: "healthy"}
	c := sourceCollection(source, cp(&success, next), now)
	if c.State != "fresh" || !c.FreshUntil.Equal(next.Add(2*time.Minute)) {
		t.Fatalf("long cadence: %+v", c)
	}
	overdue := now.Add(-3 * time.Minute)
	source.FailureStreak = 1
	c = sourceCollection(source, cp(&success, overdue), now)
	if c.State != "stale" || c.Mode != "backoff" || c.Reason != "failure_backoff" {
		t.Fatalf("retry hid overdue resource: %+v", c)
	}
	c = sourceCollection(source, cp(nil, next), now)
	if c.State != "unknown" {
		t.Fatalf("legacy checkpoint invented success: %+v", c)
	}
	source.Enabled = false
	c = sourceCollection(source, cp(&success, overdue), now)
	if c.State != "disabled" || c.NextPollAt != nil {
		t.Fatalf("disabled: %+v", c)
	}
}

func TestVendorCollectionUsesWorstEnabledSource(t *testing.T) {
	now := time.Now()
	sources := []SourceView{
		{Enabled: true, Collection: CollectionStatus{State: "fresh", Reason: "on_schedule"}},
		{Enabled: true, Collection: CollectionStatus{State: "stale", Reason: "resource_overdue"}},
	}
	if c := vendorCollection(sources, now); c.State != "stale" {
		t.Fatalf("fresh source hid stale source: %+v", c)
	}
	sources[1].Enabled = false
	if c := vendorCollection(sources, now); c.State != "fresh" {
		t.Fatalf("disabled source affects vendor: %+v", c)
	}
	sources[0].Collection.State = "unknown"
	if c := vendorCollection(sources, now); c.State != "unknown" {
		t.Fatal(c)
	}
}
