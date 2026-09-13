package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegrationCollectionFailureLifecycle(t *testing.T) {
	db := openIntegrationDatabase(t)
	ctx := context.Background()
	db.insertSource(t, integrationSourceID, time.Now().Add(-time.Minute))
	leases, err := db.store.AcquireDueSources(ctx, "failure-test", 1, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease: %v %v", leases, err)
	}
	l := leases[0]
	p := FailPollParams{SourceID: l.ID, LeaseToken: l.LeaseToken, NextPollAt: time.Now().Add(-time.Second), FailureCode: "rate_limited"}
	if err = db.store.FailPoll(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Released tokens cannot replace the latest diagnostic.
	p.FailureCode = "network"
	if err = db.store.FailPoll(ctx, p); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale lease: %v", err)
	}
	var code *string
	if err = db.pool.QueryRow(ctx, `SELECT last_failure_code FROM sources WHERE id=$1`, l.ID).Scan(&code); err != nil || code == nil || *code != "rate_limited" {
		t.Fatalf("persisted: %v %v", code, err)
	}
	leases, err = db.store.AcquireDueSources(ctx, "failure-test", 1, time.Minute)
	if err != nil || len(leases) != 1 {
		t.Fatalf("reacquire: %v %v", leases, err)
	}
	if err = db.store.CompletePoll(ctx, CompletePollParams{SourceID: l.ID, LeaseToken: leases[0].LeaseToken, NextPollAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if err = db.pool.QueryRow(ctx, `SELECT last_failure_code FROM sources WHERE id=$1`, l.ID).Scan(&code); err != nil || code != nil {
		t.Fatalf("recovery: %v %v", code, err)
	}
}
