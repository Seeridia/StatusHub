package collector

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/scheduler"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type fixedRandom struct{}

func (fixedRandom) Int63n(int64) int64 { return 0 }

type fakeRepository struct {
	commits  []store.CommitPollParams
	failures []store.FailPollParams
}

func (*fakeRepository) AcquireDueSources(context.Context, string, int, time.Duration) ([]store.SourceLease, error) {
	return nil, nil
}

func (repository *fakeRepository) CommitPoll(_ context.Context, params store.CommitPollParams) (store.CommitPollResult, error) {
	repository.commits = append(repository.commits, params)
	return store.CommitPollResult{InsertedEvents: len(params.Writes)}, nil
}

func (repository *fakeRepository) FailPoll(_ context.Context, params store.FailPollParams) error {
	repository.failures = append(repository.failures, params)
	return nil
}

type fakeAdapter struct {
	mu          sync.Mutex
	snapshots   map[domain.ResourceKind]domain.Snapshot
	failures    map[domain.ResourceKind]error
	fetches     []domain.ResourceKind
	retryAfter  time.Duration
	notModified bool
}

func (*fakeAdapter) Probe(_ context.Context, target domain.Target) (domain.Capabilities, error) {
	return domain.Capabilities{
		Engine: "atlassian-statuspage", Confidence: 1,
		Endpoints: map[domain.ResourceKind]domain.EndpointCapability{
			domain.ResourceUnresolvedIncidents: {
				Resource: domain.ResourceUnresolvedIncidents, Path: "/api/v2/incidents/unresolved.json",
				Cache: domain.CacheCapability{DefaultTTL: time.Second},
			},
			domain.ResourceSummary: {
				Resource: domain.ResourceSummary, Path: "/api/v2/summary.json",
				Cache: domain.CacheCapability{DefaultTTL: time.Second},
			},
		},
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	}, nil
}

func (adapter *fakeAdapter) Fetch(_ context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	adapter.fetches = append(adapter.fetches, request.ResourceKind)
	if err := adapter.failures[request.ResourceKind]; err != nil {
		return domain.Snapshot{}, domain.FetchMeta{RetryAfter: adapter.retryAfter}, err
	}
	snapshot := adapter.snapshots[request.ResourceKind]
	return snapshot, domain.FetchMeta{
		NotModified: adapter.notModified,
		Endpoint:    request.Endpoint.Path, StatusCode: 200,
		ETag: "etag-" + string(request.ResourceKind), ObservedAt: snapshot.ObservedAt,
		BodyBytes: 100,
	}, nil
}

func (*fakeAdapter) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, errors.New("not implemented")
}

type fakePreparer struct{}

func (fakePreparer) PrepareAll(events []domain.CanonicalEvent) ([]store.EventWrite, error) {
	writes := make([]store.EventWrite, 0, len(events))
	for index, event := range events {
		event.ID = domain.CanonicalEventID("00000000-0000-0000-0000-000000000001")
		writes = append(writes, store.EventWrite{
			Event: event,
			Outbox: store.OutboxMessage{
				ID: "00000000-0000-0000-0000-000000000002", Subject: "statushub.events.normal", Payload: []byte(`{}`),
			},
		})
		_ = index
	}
	return writes, nil
}

func TestNotModifiedUpdatesResourceSuccess(t *testing.T) {
	start := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	a := &fakeAdapter{notModified: true, snapshots: map[domain.ResourceKind]domain.Snapshot{}, failures: map[domain.ResourceKind]error{}}
	worker := newTestWorker(t, repository, a, start)
	if _, _, err := worker.process(context.Background(), testLease(nil, 0)); err != nil {
		t.Fatal(err)
	}
	c, err := DecodeCheckpoint(&repository.commits[0].Checkpoint, testLease(nil, 0).ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.ResourceKind{domain.ResourceSummary, domain.ResourceUnresolvedIncidents} {
		if r := c.Resources[kind]; r.LastSuccessAt == nil || !r.LastSuccessAt.Equal(start) {
			t.Fatalf("304 lost successful check: %+v", r)
		}
	}
	if len(repository.commits[0].Writes) != 0 {
		t.Fatal("304 emitted events")
	}
}

func TestWorkerEstablishesBaselineThenEmitsNewIncident(t *testing.T) {
	start := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	statusAdapter := &fakeAdapter{snapshots: map[domain.ResourceKind]domain.Snapshot{
		domain.ResourceUnresolvedIncidents: collectorSnapshot(domain.ResourceUnresolvedIncidents, start, nil),
		domain.ResourceSummary:             collectorSnapshot(domain.ResourceSummary, start.Add(time.Millisecond), nil),
	}, failures: map[domain.ResourceKind]error{}}
	worker := newTestWorker(t, repository, statusAdapter, start)
	lease := testLease(nil, 0)

	fetched, inserted, err := worker.process(context.Background(), lease)
	if err != nil || fetched != 2 || inserted != 0 {
		t.Fatalf("baseline process = fetched %d inserted %d err %v", fetched, inserted, err)
	}
	if len(repository.commits) != 1 || len(repository.commits[0].Writes) != 0 {
		t.Fatalf("baseline commits = %#v", repository.commits)
	}
	checkpoint, err := DecodeCheckpoint(&repository.commits[0].Checkpoint, lease.ID)
	if err != nil {
		t.Fatalf("decode baseline checkpoint: %v", err)
	}
	if !checkpoint.Reconcile.BaselineEstablished {
		t.Fatal("baseline was not durably established")
	}
	for _, resource := range []domain.ResourceKind{domain.ResourceUnresolvedIncidents, domain.ResourceSummary} {
		state := checkpoint.Resources[resource]
		if state.LastSuccessAt == nil || !state.LastSuccessAt.Equal(start) || state.ScheduleReason != "cadence" {
			t.Fatalf("resource success metadata: %+v", state)
		}
	}
	if got := checkpoint.Resources[domain.ResourceUnresolvedIncidents].NextPollAt.Sub(start); got != 2*time.Minute {
		t.Fatalf("unresolved hot delay = %s, want 2m", got)
	}
	if got := checkpoint.Resources[domain.ResourceSummary].NextPollAt.Sub(start); got != 3*time.Minute {
		t.Fatalf("summary hot delay = %s, want 3m", got)
	}

	secondAt := start.Add(121 * time.Second)
	worker.now = func() time.Time { return secondAt }
	statusAdapter.snapshots[domain.ResourceUnresolvedIncidents] = collectorSnapshot(
		domain.ResourceUnresolvedIncidents,
		secondAt,
		[]domain.Incident{{
			ID: "upstream-incident", Kind: domain.IncidentKindIncident,
			Name: "API disruption", Phase: domain.IncidentPhaseInvestigating,
			Impact: domain.ImpactMajor,
		}},
	)
	encoded := repository.commits[0].Checkpoint
	lease = testLease(&encoded, 0)
	fetched, inserted, err = worker.process(context.Background(), lease)
	if err != nil || fetched != 1 || inserted != 1 {
		t.Fatalf("changed process = fetched %d inserted %d err %v", fetched, inserted, err)
	}
	lastCommit := repository.commits[len(repository.commits)-1]
	if len(lastCommit.Writes) != 1 || lastCommit.Writes[0].Event.Kind != domain.EventKindIncidentCreated {
		t.Fatalf("new incident writes = %#v", lastCommit.Writes)
	}
}

func TestWorkerFailureUsesBackoffAndDoesNotCommitCheckpoint(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	statusAdapter := &fakeAdapter{
		snapshots: map[domain.ResourceKind]domain.Snapshot{
			domain.ResourceSummary: collectorSnapshot(domain.ResourceSummary, now, nil),
		},
		failures: map[domain.ResourceKind]error{domain.ResourceUnresolvedIncidents: errors.New("upstream timeout")},
	}
	worker := newTestWorker(t, repository, statusAdapter, now)
	_, _, err := worker.process(context.Background(), testLease(nil, 2))
	if err == nil {
		t.Fatal("process error = nil, want upstream failure")
	}
	if len(repository.commits) != 0 || len(repository.failures) != 1 {
		t.Fatalf("commits=%d failures=%d", len(repository.commits), len(repository.failures))
	}
	// The third consecutive failure must use the durable lease count, not
	// restart from the successful checkpoint's zero failure streak.
	if got := repository.failures[0].NextPollAt.Sub(now); got != 2*time.Minute {
		t.Fatalf("third failure delay = %s, want 2m", got)
	}
	if repository.failures[0].HealthState != "degraded" {
		t.Fatalf("failure health = %q", repository.failures[0].HealthState)
	}
}

func newTestWorker(t *testing.T, repository Repository, statusAdapter StatusAdapter, now time.Time) *Worker {
	t.Helper()
	cadence, err := scheduler.NewPolicy(scheduler.DefaultConfig(), fixedRandom{})
	if err != nil {
		t.Fatalf("new cadence: %v", err)
	}
	resources, err := scheduler.NewResourcePlanner(scheduler.StatuspageCadences(), fixedRandom{})
	if err != nil {
		t.Fatalf("new resource planner: %v", err)
	}
	worker, err := New(repository, statusAdapter, fakePreparer{}, cadence, resources, nil, DefaultConfig("collector-test"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worker.now = func() time.Time { return now }
	return worker
}

func collectorSnapshot(resource domain.ResourceKind, observedAt time.Time, incidents []domain.Incident) domain.Snapshot {
	return domain.Snapshot{
		Source:       domain.Source{ID: "20000000-0000-0000-0000-000000000001", Provider: "atlassian-statuspage"},
		ResourceKind: resource, Incidents: incidents,
		Completeness:     domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceUnresolvedIncidents},
		ObservedAt:       observedAt, SchemaVersion: "v2",
		AdapterVersion: "statuspage-v2/1", NormalizerVersion: "statuspage-v2/1",
	}
}

func testLease(checkpoint *string, failures int) store.SourceLease {
	parsed, _ := url.Parse("https://status.example.test")
	return store.SourceLease{
		ID:           "20000000-0000-0000-0000-000000000001",
		CanonicalURL: parsed.String(), HealthState: "unknown",
		LeaseToken:    "00000000-0000-0000-0000-000000000010",
		FailureStreak: failures, LastCheckpoint: checkpoint,
	}
}

func TestFailedFetchHonorsUpstreamRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	repository := &fakeRepository{}
	adapter := &fakeAdapter{failures: map[domain.ResourceKind]error{
		domain.ResourceUnresolvedIncidents: errors.New("rate limited"),
		domain.ResourceSummary:             errors.New("rate limited"),
	}, retryAfter: 20 * time.Minute}
	worker := newTestWorker(t, repository, adapter, now)
	if _, _, err := worker.process(context.Background(), testLease(nil, 0)); err == nil {
		t.Fatal("expected fetch failure")
	}
	if len(repository.failures) != 1 || !repository.failures[0].NextPollAt.Equal(now.Add(20*time.Minute)) {
		t.Fatalf("Retry-After ignored: %+v", repository.failures)
	}
}
