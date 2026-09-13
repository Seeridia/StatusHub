package shadow

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	adaptercontract "github.com/Seeridia/StatusHub/internal/adapter"
	"github.com/Seeridia/StatusHub/internal/domain"
)

type repositoryStub struct {
	rollout  Rollout
	loaded   chan struct{}
	once     sync.Once
	recorded chan Comparison
}

func (repository *repositoryStub) ActiveAdapterRollout(context.Context, string) (Rollout, bool, error) {
	repository.once.Do(func() { close(repository.loaded) })
	return repository.rollout, true, nil
}

func (repository *repositoryStub) RecordAdapterComparison(_ context.Context, comparison Comparison) error {
	repository.recorded <- comparison
	return nil
}

type adapterStub struct {
	snapshot domain.Snapshot
	metadata domain.FetchMeta
	err      error
	wait     <-chan struct{}
}

func (adapter *adapterStub) Probe(context.Context, domain.Target) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}

func (adapter *adapterStub) Fetch(ctx context.Context, _ domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	if adapter.wait != nil {
		select {
		case <-adapter.wait:
		case <-ctx.Done():
			return domain.Snapshot{}, domain.FetchMeta{}, ctx.Err()
		}
	}
	return adapter.snapshot, adapter.metadata, adapter.err
}

func (*adapterStub) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, errors.New("not implemented")
}

func TestCandidateRunsOffPrimaryResponsePathAndRecordsSemanticComparison(t *testing.T) {
	now := time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC)
	primarySnapshot := domain.Snapshot{ResourceKind: domain.ResourceSummary, OverallStatus: domain.ComponentStatusDegraded,
		ComputedStatus: domain.ComponentStatusDegraded, Completeness: domain.CompletenessComplete,
		Components: []domain.Component{{ID: "b", Name: "B", Status: domain.ComponentStatusOperational}, {ID: "a", Name: "A", Status: domain.ComponentStatusDegraded}}}
	candidateSnapshot := primarySnapshot
	candidateSnapshot.Components = []domain.Component{primarySnapshot.Components[1], primarySnapshot.Components[0]}
	releaseCandidate := make(chan struct{})
	repository := &repositoryStub{rollout: Rollout{ID: "rollout-1", SourceID: "source-1",
		CandidateAdapterName: "candidate", CandidateAdapterVersion: "v2", SampleRate: 1},
		loaded: make(chan struct{}), recorded: make(chan Comparison, 1)}
	primary := &adapterStub{snapshot: primarySnapshot, metadata: domain.FetchMeta{ObservedAt: now}}
	candidate := &adapterStub{snapshot: candidateSnapshot, metadata: domain.FetchMeta{ObservedAt: now}, wait: releaseCandidate}
	adapter, err := New(primary, map[string]adaptercontract.Adapter{"candidate@v2": candidate}, repository,
		Config{Workers: 1, QueueSize: 2, FetchTimeout: time.Second, ControlTTL: time.Minute, RecordTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = adapter.Close(closeContext)
	})
	request := domain.FetchRequest{Source: domain.Source{ID: "source-1"}, ResourceKind: domain.ResourceSummary}
	if _, _, err := adapter.Fetch(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-repository.loaded:
	case <-time.After(time.Second):
		t.Fatal("rollout cache was not refreshed")
	}
	returned := make(chan error, 1)
	go func() {
		_, _, fetchErr := adapter.Fetch(context.Background(), request)
		returned <- fetchErr
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("primary Fetch waited for the shadow candidate")
	}
	close(releaseCandidate)
	select {
	case comparison := <-repository.recorded:
		if !comparison.Equivalent || len(comparison.PrimaryDigest) != 32 || len(comparison.CandidateDigest) != 32 {
			t.Fatalf("comparison=%#v", comparison)
		}
	case <-time.After(time.Second):
		t.Fatal("shadow comparison was not recorded")
	}
}
