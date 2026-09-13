// Package collector coordinates source leases, endpoint fetches,
// reconciliation, and the atomic state/event/outbox commit.
package collector

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/reconcile"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/scheduler"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type Repository interface {
	AcquireDueSources(context.Context, string, int, time.Duration) ([]store.SourceLease, error)
	CommitPoll(context.Context, store.CommitPollParams) (store.CommitPollResult, error)
	FailPoll(context.Context, store.FailPollParams) error
}

type regionalRepository interface {
	AcquireDueSourcesInRegion(context.Context, string, string, int, time.Duration) ([]store.SourceLease, error)
}

type StatusAdapter interface {
	adapter.Adapter
}

type EventPreparer interface {
	PrepareAll([]domain.CanonicalEvent) ([]store.EventWrite, error)
}

type Observer interface {
	RecordPoll(context.Context, string, string, time.Duration, int64, bool)
	RecordReconcile(context.Context, string, int)
}

type freshnessObserver interface {
	RecordSourceFreshness(context.Context, string, time.Duration)
	RecordSchemaDrift(context.Context, string, string)
}

type Config struct {
	Owner          string
	Region         string
	BatchSize      int
	MaxConcurrency int
	LeaseDuration  time.Duration
}

func DefaultConfig(owner string) Config {
	return Config{Owner: owner, BatchSize: 32, MaxConcurrency: 8, LeaseDuration: 90 * time.Second}
}

func (config Config) validate() error {
	if strings.TrimSpace(config.Owner) == "" {
		return errors.New("collector owner is required")
	}
	if config.BatchSize <= 0 || config.MaxConcurrency <= 0 || config.MaxConcurrency > config.BatchSize {
		return errors.New("collector batch/concurrency is invalid")
	}
	if config.LeaseDuration <= 0 {
		return errors.New("collector lease duration must be positive")
	}
	return nil
}

type Worker struct {
	repository      Repository
	adapter         StatusAdapter
	preparer        EventPreparer
	cadence         *scheduler.Policy
	resourcePlanner *scheduler.ResourcePlanner
	observer        Observer
	config          Config
	now             func() time.Time
}

func New(
	repository Repository,
	statusAdapter StatusAdapter,
	preparer EventPreparer,
	cadence *scheduler.Policy,
	resourcePlanner *scheduler.ResourcePlanner,
	observer Observer,
	config Config,
) (*Worker, error) {
	if repository == nil || statusAdapter == nil || preparer == nil || cadence == nil || resourcePlanner == nil {
		return nil, errors.New("collector dependencies are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Worker{
		repository: repository, adapter: statusAdapter, preparer: preparer,
		cadence: cadence, resourcePlanner: resourcePlanner, observer: observer,
		config: config, now: func() time.Time { return time.Now().UTC() },
	}, nil
}

type Stats struct {
	Claimed        int `json:"claimed"`
	Completed      int `json:"completed"`
	Failed         int `json:"failed"`
	Fetched        int `json:"fetched"`
	InsertedEvents int `json:"inserted_events"`
}

func (worker *Worker) RunOnce(ctx context.Context) (Stats, error) {
	if worker == nil {
		return Stats{}, errors.New("collector is not initialized")
	}
	var leases []store.SourceLease
	var err error
	if worker.config.Region != "" {
		regional, ok := worker.repository.(regionalRepository)
		if !ok {
			return Stats{}, errors.New("collector repository does not support regional source ownership")
		}
		leases, err = regional.AcquireDueSourcesInRegion(ctx, worker.config.Region, worker.config.Owner, worker.config.BatchSize, worker.config.LeaseDuration)
	} else {
		leases, err = worker.repository.AcquireDueSources(ctx, worker.config.Owner, worker.config.BatchSize, worker.config.LeaseDuration)
	}
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Claimed: len(leases)}
	type sourceResult struct {
		fetched  int
		inserted int
		err      error
	}
	results := make(chan sourceResult, len(leases))
	semaphore := make(chan struct{}, worker.config.MaxConcurrency)
	var group sync.WaitGroup
	for _, lease := range leases {
		lease := lease
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- sourceResult{err: ctx.Err()}
				return
			}
			fetched, inserted, err := worker.process(ctx, lease)
			results <- sourceResult{fetched: fetched, inserted: inserted, err: err}
		}()
	}
	group.Wait()
	close(results)
	var failures []error
	for result := range results {
		stats.Fetched += result.fetched
		stats.InsertedEvents += result.inserted
		if result.err != nil {
			stats.Failed++
			failures = append(failures, result.err)
		} else {
			stats.Completed++
		}
	}
	return stats, errors.Join(failures...)
}

type fetchResult struct {
	resource domain.ResourceKind
	snapshot domain.Snapshot
	meta     domain.FetchMeta
	duration time.Duration
	err      error
}

func (worker *Worker) process(ctx context.Context, lease store.SourceLease) (int, int, error) {
	checkpoint, err := DecodeCheckpoint(lease.LastCheckpoint, lease.ID)
	if err != nil {
		return 0, 0, worker.fail(ctx, lease, checkpoint.Cadence, err)
	}
	// Failed polls deliberately preserve the last successful checkpoint. The
	// lease carries the authoritative failure count across worker restarts.
	checkpoint.Cadence.FailureStreak = uint32(lease.FailureStreak)
	if lease.FailureStreak > 0 {
		checkpoint.Cadence.Mode = scheduler.ModeBackoff
	}
	now := worker.now()
	targetURL, err := url.Parse(lease.CanonicalURL)
	if err != nil || targetURL.Scheme == "" || targetURL.Host == "" {
		return 0, 0, worker.fail(ctx, lease, checkpoint.Cadence, errors.New("collector: source canonical URL is invalid"))
	}
	target := domain.Target{URL: targetURL, SourceID: lease.ID}
	if lease.AdapterName != nil {
		target.Provider = *lease.AdapterName
	}
	if len(checkpoint.Capabilities.Endpoints) == 0 || !checkpoint.Capabilities.ExpiresAt.After(now) {
		checkpoint.Capabilities, err = worker.adapter.Probe(ctx, target)
		if err != nil {
			return 0, 0, worker.fail(ctx, lease, checkpoint.Cadence, fmt.Errorf("probe source: %w", err))
		}
	}

	due := worker.resourcePlanner.Due(resourceStates(checkpoint), now)
	availableDue := due[:0]
	for _, resource := range due {
		if _, found := checkpoint.Capabilities.Endpoints[resource]; found {
			availableDue = append(availableDue, resource)
			continue
		}
		checkpoint.Resources[resource] = ResourceCheckpoint{NextPollAt: checkpoint.Capabilities.ExpiresAt}
	}
	due = availableDue
	if len(due) == 0 {
		return worker.commit(ctx, lease, checkpoint, nil, now)
	}

	results := make(chan fetchResult, len(due))
	for _, resource := range due {
		resource := resource
		go func() {
			started := worker.now()
			state := checkpoint.Resources[resource]
			snapshot, meta, fetchErr := worker.adapter.Fetch(ctx, domain.FetchRequest{
				Target: target,
				Source: domain.Source{
					ID: lease.ID, Provider: checkpoint.Capabilities.Engine,
					Kind: domain.SourceKindStatusPage, RequestedURL: targetURL, CanonicalURL: targetURL,
				},
				ResourceKind: resource,
				Endpoint:     checkpoint.Capabilities.Endpoints[resource],
				ETag:         state.ETag, LastModified: state.LastModified,
			})
			results <- fetchResult{resource: resource, snapshot: snapshot, meta: meta, duration: worker.now().Sub(started), err: fetchErr}
		}()
	}
	fetched := make([]fetchResult, 0, len(due))
	var fetchErrors []error
	var retryAfter time.Duration
	for range due {
		result := <-results
		if result.meta.RetryAfter > retryAfter {
			retryAfter = result.meta.RetryAfter
		}
		resultClass := "success"
		if result.err != nil {
			resultClass = "error"
		}
		worker.recordPoll(ctx, checkpoint.Capabilities.Engine, resultClass, result.duration, result.meta.BodyBytes, result.meta.NotModified)
		if result.err != nil {
			if extended, ok := worker.observer.(freshnessObserver); ok {
				previous := checkpoint.Resources[result.resource].SchemaHash
				if previous != "" && result.meta.SchemaHash != "" && previous != result.meta.SchemaHash {
					extended.RecordSchemaDrift(ctx, checkpoint.Capabilities.Engine, string(result.resource))
				}
			}
			fetchErrors = append(fetchErrors, fmt.Errorf("fetch %s: %w", result.resource, &fetchFailure{err: result.err, status: result.meta.StatusCode}))
			continue
		}
		fetched = append(fetched, result)
	}
	if len(fetchErrors) > 0 {
		return len(fetched), 0, worker.fail(ctx, lease, checkpoint.Cadence, errors.Join(fetchErrors...), retryAfter)
	}
	slices.SortFunc(fetched, func(left, right fetchResult) int {
		if left.meta.ObservedAt.Before(right.meta.ObservedAt) {
			return -1
		}
		if left.meta.ObservedAt.After(right.meta.ObservedAt) {
			return 1
		}
		return strings.Compare(string(left.resource), string(right.resource))
	})

	changed := false
	events := make([]domain.CanonicalEvent, 0)
	for _, fetchedResource := range fetched {
		state := checkpoint.Resources[fetchedResource.resource]
		if extended, ok := worker.observer.(freshnessObserver); ok {
			freshness := fetchedResource.duration
			if !state.NextPollAt.IsZero() {
				freshness = fetchedResource.meta.ObservedAt.Sub(state.NextPollAt)
			}
			if freshness >= 0 {
				extended.RecordSourceFreshness(ctx, checkpoint.Capabilities.Engine, freshness)
			}
			if state.SchemaHash != "" && fetchedResource.meta.SchemaHash != "" && state.SchemaHash != fetchedResource.meta.SchemaHash {
				extended.RecordSchemaDrift(ctx, checkpoint.Capabilities.Engine, string(fetchedResource.resource))
			}
		}
		if fetchedResource.meta.ETag != "" {
			state.ETag = fetchedResource.meta.ETag
		}
		if fetchedResource.meta.LastModified != nil {
			state.LastModified = fetchedResource.meta.LastModified
		}
		if fetchedResource.meta.SchemaHash != "" {
			state.SchemaHash = fetchedResource.meta.SchemaHash
		}
		checkpoint.Resources[fetchedResource.resource] = state
		if fetchedResource.meta.NotModified {
			continue
		}
		result, applyErr := reconcile.Apply(checkpoint.Reconcile, fetchedResource.snapshot)
		if applyErr != nil {
			worker.recordReconcile(ctx, "error", 0)
			return len(fetched), 0, worker.fail(ctx, lease, checkpoint.Cadence, applyErr)
		}
		checkpoint.Reconcile = result.State
		changed = changed || result.Changed
		events = append(events, result.Events...)
		resultClass := "unchanged"
		if result.BaselineEstablished {
			resultClass = "baseline"
		} else if result.Changed {
			resultClass = "changed"
		} else if result.Duplicate {
			resultClass = "duplicate"
		}
		worker.recordReconcile(ctx, resultClass, len(result.Events))
	}

	completedAt := worker.now()
	decision, err := worker.cadence.Next(checkpoint.Cadence, scheduler.Signal{
		At: completedAt, Outcome: scheduler.OutcomeSuccess, Changed: changed,
		HasActiveIncident: hasActiveIncident(checkpoint.Reconcile),
	})
	if err != nil {
		return len(fetched), 0, worker.fail(ctx, lease, checkpoint.Cadence, err)
	}
	checkpoint.Cadence = decision.State
	for _, fetchedResource := range fetched {
		minimumDelay := checkpoint.Capabilities.Endpoints[fetchedResource.resource].Cache.DefaultTTL
		next, scheduleErr := worker.resourcePlanner.Next(
			fetchedResource.resource, decision.State.Mode, completedAt,
			minimumDelay, completedAt.Add(fetchedResource.meta.RetryAfter),
		)
		if scheduleErr != nil {
			return len(fetched), 0, worker.fail(ctx, lease, checkpoint.Cadence, scheduleErr)
		}
		state := checkpoint.Resources[fetchedResource.resource]
		state.NextPollAt = next.NextPollAt
		state.LastSuccessAt = &completedAt
		state.ScheduleReason = "cadence"
		if minimumDelay > 0 && next.NextPollAt.Equal(completedAt.Add(minimumDelay)) {
			state.ScheduleReason = "cache_policy"
		}
		if fetchedResource.meta.RetryAfter > 0 && next.NextPollAt.Equal(completedAt.Add(fetchedResource.meta.RetryAfter)) {
			state.ScheduleReason = "retry_after"
		}
		checkpoint.Resources[fetchedResource.resource] = state
	}
	writes, err := worker.preparer.PrepareAll(events)
	if err != nil {
		return len(fetched), 0, worker.fail(ctx, lease, checkpoint.Cadence, err)
	}
	_, inserted, commitErr := worker.commit(ctx, lease, checkpoint, writes, completedAt)
	return len(fetched), inserted, commitErr
}

func (worker *Worker) commit(ctx context.Context, lease store.SourceLease, checkpoint Checkpoint, writes []store.EventWrite, now time.Time) (int, int, error) {
	encoded, err := EncodeCheckpoint(checkpoint)
	if err != nil {
		return 0, 0, worker.fail(ctx, lease, checkpoint.Cadence, err)
	}
	next := nextResourceDeadline(checkpoint.Resources, now.Add(time.Minute))
	result, err := worker.repository.CommitPoll(ctx, store.CommitPollParams{
		State:    &checkpoint.Reconcile,
		SourceID: lease.ID, LeaseToken: lease.LeaseToken,
		NextPollAt: next, Checkpoint: encoded, HealthState: "healthy",
		SuccessfulAt: now, Writes: writes, ActiveRegion: lease.ActiveRegion, OwnershipEpoch: lease.OwnershipEpoch,
	})
	if err != nil {
		return 0, 0, err
	}
	return 0, result.InsertedEvents, nil
}

func (worker *Worker) fail(ctx context.Context, lease store.SourceLease, state scheduler.State, cause error, minimumDelays ...time.Duration) error {
	now := worker.now()
	state.FailureStreak = uint32(lease.FailureStreak)
	var minimumDelay time.Duration
	for _, delay := range minimumDelays {
		if delay > minimumDelay {
			minimumDelay = delay
		}
	}
	decision, scheduleErr := worker.cadence.Next(state, scheduler.Signal{At: now, Outcome: scheduler.OutcomeFailure, MinimumDelay: minimumDelay})
	if scheduleErr != nil {
		return errors.Join(cause, scheduleErr)
	}
	health := lease.HealthState
	if lease.FailureStreak+1 >= 3 {
		health = "degraded"
	}
	persistErr := worker.repository.FailPoll(ctx, store.FailPollParams{
		FailureCode: failureCode(cause),
		SourceID:    lease.ID, LeaseToken: lease.LeaseToken,
		NextPollAt: decision.NextPollAt, HealthState: health,
		ActiveRegion: lease.ActiveRegion, OwnershipEpoch: lease.OwnershipEpoch,
	})
	return errors.Join(cause, persistErr)
}

func hasActiveIncident(state reconcile.State) bool {
	for _, incident := range state.Incidents {
		if incident.Incident.Phase != domain.IncidentPhaseResolved {
			return true
		}
	}
	return false
}

func (worker *Worker) recordPoll(ctx context.Context, engine, result string, duration time.Duration, bytes int64, notModified bool) {
	if worker.observer != nil {
		worker.observer.RecordPoll(ctx, engine, result, duration, bytes, notModified)
	}
}

func (worker *Worker) recordReconcile(ctx context.Context, result string, events int) {
	if worker.observer != nil {
		worker.observer.RecordReconcile(ctx, result, events)
	}
}
