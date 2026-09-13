package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/bus"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type fakeStore struct {
	leases    []store.OutboxLease
	marked    []string
	failed    []store.FailOutboxParams
	markError error
}

func (f *fakeStore) ClaimOutbox(context.Context, string, int, time.Duration) ([]store.OutboxLease, error) {
	return append([]store.OutboxLease(nil), f.leases...), nil
}
func (f *fakeStore) MarkOutboxPublished(_ context.Context, id, _ string, _ time.Time) error {
	f.marked = append(f.marked, id)
	return f.markError
}
func (f *fakeStore) FailOutbox(_ context.Context, params store.FailOutboxParams) error {
	f.failed = append(f.failed, params)
	return nil
}

type fakeBus struct {
	results map[string]bus.PublishResult
	errors  map[string]error
}

func (f *fakeBus) Publish(_ context.Context, message bus.Message) (bus.PublishResult, error) {
	return f.results[message.ID], f.errors[message.ID]
}

type zeroRandom struct{}

func (zeroRandom) Int63n(int64) int64 { return 0 }

func TestRunOncePublishesAndReschedulesFailures(t *testing.T) {
	now := time.Date(2026, 9, 10, 1, 2, 3, 0, time.UTC)
	repository := &fakeStore{leases: []store.OutboxLease{
		{ID: "ok", Subject: "statushub.events.normal", Payload: []byte(`{}`), LeaseToken: "token-1", CreatedAt: now.Add(-time.Second), Attempts: 1},
		{ID: "bad", Subject: "statushub.events.critical", Payload: []byte(`{}`), LeaseToken: "token-2", CreatedAt: now.Add(-time.Second), Attempts: 2},
	}}
	eventBus := &fakeBus{
		results: map[string]bus.PublishResult{"ok": {Duplicate: true}},
		errors:  map[string]error{"bad": errors.New("broker unavailable")},
	}
	config := DefaultConfig("publisher-a")
	config.MaxConcurrency = 1
	publisher, err := New(repository, eventBus, nil, config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	publisher.now = func() time.Time { return now }
	publisher.random = zeroRandom{}

	stats, err := publisher.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce() error = nil, want publish failure")
	}
	if stats != (Stats{Claimed: 2, Published: 1, Duplicates: 1, Failed: 1}) {
		t.Fatalf("RunOnce() stats = %+v", stats)
	}
	if len(repository.marked) != 1 || repository.marked[0] != "ok" {
		t.Fatalf("marked = %v", repository.marked)
	}
	if len(repository.failed) != 1 || repository.failed[0].ID != "bad" {
		t.Fatalf("failed = %+v", repository.failed)
	}
	if !repository.failed[0].AvailableAt.Equal(now) {
		t.Fatalf("retry time = %s, want zero-jitter %s", repository.failed[0].AvailableAt, now)
	}
}

func TestPublishAcceptedButMarkFailedKeepsLease(t *testing.T) {
	repository := &fakeStore{
		leases:    []store.OutboxLease{{ID: "outbox-1", Subject: "statushub.events.normal", Payload: []byte(`{}`), LeaseToken: "token", CreatedAt: time.Now(), Attempts: 1}},
		markError: errors.New("simulated database crash"),
	}
	publisher, err := New(repository, &fakeBus{results: map[string]bus.PublishResult{}, errors: map[string]error{}}, nil, DefaultConfig("publisher-a"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	stats, err := publisher.RunOnce(context.Background())
	if err == nil || stats.Failed != 1 {
		t.Fatalf("RunOnce() = (%+v, %v), want mark failure", stats, err)
	}
	if len(repository.failed) != 0 {
		t.Fatalf("published message was incorrectly released as failed: %+v", repository.failed)
	}
}
