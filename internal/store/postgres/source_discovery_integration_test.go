package postgres

import (
	"context"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"testing"
	"time"
)

func TestIntegrationCreateDiscoveredSourceAtomically(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	if _, err := db.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'discovery','Discovery')`, m5TenantA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pool.Exec(ctx, `INSERT INTO regions(id) VALUES('local')`); err != nil {
		t.Fatal(err)
	}
	params := CreateSourceParams{ID: m5PrivateSource, TenantID: m5TenantA, VendorID: integrationUUID(997), AutoVendorSlug: "site-example", AutoVendorName: "status.example.com", RequestedURL: "https://status.example.com", CanonicalURL: "https://status.example.com", SourceType: "status_page", AdapterName: "atlassian-statuspage", AdapterVersion: "1", ActiveRegion: "local", Capabilities: domain.Capabilities{Engine: "atlassian-statuspage", ExpiresAt: time.Now().Add(time.Hour), Endpoints: map[domain.ResourceKind]domain.EndpointCapability{domain.ResourceSummary: {Path: "/api/v2/summary.json", Completeness: domain.CompletenessComplete}}}, Actor: AuditActor{Type: "system", ID: "test"}}
	source, err := db.store.CreateSource(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if source.VendorName != "status.example.com" {
		t.Fatalf("wrong identity: %+v", source)
	}
	params.ID = integrationUUID(998)
	params.CanonicalURL = "https://status.example.com/other"
	params.AutoVendorName = "Must not overwrite"
	if _, err := db.store.CreateSource(ctx, params); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := db.pool.QueryRow(ctx, `SELECT name FROM vendors WHERE id=$1`, params.VendorID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "status.example.com" {
		t.Fatal("overwrote existing identity")
	}
	params.ID = integrationUUID(995)
	params.VendorID = integrationUUID(996)
	params.AutoVendorSlug = "rollback-site"
	params.ActiveRegion = "missing"
	if _, err := db.store.CreateSource(ctx, params); err == nil {
		t.Fatal("accepted nonexistent region")
	}
	var count int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM vendors WHERE id=$1`, params.VendorID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("orphan vendor after failed source creation")
	}
}
