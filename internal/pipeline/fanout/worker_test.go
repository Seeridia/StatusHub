package fanout

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/bus"
	"github.com/Seeridia/StatusHub/internal/domain"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type fakeStore struct {
	ensured    string
	leases     []store.FanoutShardLease
	event      store.FanoutEvent
	candidates []store.FanoutCandidate
	commit     store.CommitFanoutShardParams
}

func (f *fakeStore) EnsureFanoutPlan(_ context.Context, eventID string, _ int) (string, bool, error) {
	f.ensured = eventID
	return "plan", true, nil
}
func (f *fakeStore) ClaimFanoutShards(context.Context, string, int, time.Duration) ([]store.FanoutShardLease, error) {
	return f.leases, nil
}
func (f *fakeStore) LoadFanoutEvent(context.Context, string) (store.FanoutEvent, error) {
	return f.event, nil
}
func (f *fakeStore) LoadFanoutCandidates(context.Context, store.FanoutShardLease, int) ([]store.FanoutCandidate, *string, bool, error) {
	value := "40000000-0000-0000-0000-000000000001"
	return f.candidates, &value, true, nil
}
func (f *fakeStore) CommitFanoutShard(_ context.Context, p store.CommitFanoutShardParams) (int64, error) {
	f.commit = p
	return int64(len(p.Deliveries)), nil
}

func TestWorkerCreatesPlanAndFanout(t *testing.T) {
	repository := &fakeStore{
		leases:     []store.FanoutShardLease{{PlanID: "plan", EventID: "10000000-0000-0000-0000-000000000001", ShardCount: 1, LeaseToken: "token"}},
		event:      store.FanoutEvent{ID: "10000000-0000-0000-0000-000000000001", Kind: domain.EventKindIncidentCreated, Payload: json.RawMessage(`{"current":{"impact":"critical"}}`)},
		candidates: []store.FanoutCandidate{{SubscriptionID: "40000000-0000-0000-0000-000000000001", EndpointID: "50000000-0000-0000-0000-000000000001", RuleVersion: 2, Rule: json.RawMessage(`{}`)}},
	}
	worker, err := New(repository, nil, DefaultConfig("test"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, _ := (bus.EventEnvelope{Version: bus.EventEnvelopeVersion, EventID: domain.CanonicalEventID(repository.event.ID), SourceID: "source", Kind: domain.EventKindIncidentCreated, EntityKind: domain.EntityIncident, EntityID: "incident", AggregateRevision: 1, SchemaVersion: "v1"}).Marshal()
	if err := worker.HandleMessage(context.Background(), envelope); err != nil {
		t.Fatal(err)
	}
	if repository.ensured != repository.event.ID {
		t.Fatalf("ensured %q", repository.ensured)
	}
	stats, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Inserted != 1 || len(repository.commit.Deliveries) != 1 || repository.commit.Deliveries[0].Priority != 100 {
		t.Fatalf("stats=%#v commit=%#v", stats, repository.commit)
	}
}
