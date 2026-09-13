// Package fanout turns canonical events into idempotent per-endpoint
// deliveries. Work is split into leased, resumable shards so a large tenant
// cannot hold one database transaction open for the entire fanout.
package fanout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/subscription"
)

type Store interface {
	EnsureFanoutPlan(context.Context, string, int) (string, bool, error)
	ClaimFanoutShards(context.Context, string, int, time.Duration) ([]store.FanoutShardLease, error)
	LoadFanoutEvent(context.Context, string) (store.FanoutEvent, error)
	LoadFanoutCandidates(context.Context, store.FanoutShardLease, int) ([]store.FanoutCandidate, *string, bool, error)
	CommitFanoutShard(context.Context, store.CommitFanoutShardParams) (int64, error)
}

type Observer interface {
	RecordFanout(context.Context, string, int64, time.Duration)
}

type Config struct {
	Owner           string
	ShardCount      int
	ClaimBatchSize  int
	PageSize        int
	MaxConcurrency  int
	LeaseDuration   time.Duration
	TemplateVersion int
}

func DefaultConfig(owner string) Config {
	return Config{
		Owner: owner, ShardCount: 16, ClaimBatchSize: 16, PageSize: 500,
		MaxConcurrency: 8, LeaseDuration: 30 * time.Second, TemplateVersion: 1,
	}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Owner) == "" || c.ShardCount <= 0 || c.ShardCount > 256 {
		return errors.New("fanout: owner and a shard count in [1,256] are required")
	}
	if c.ClaimBatchSize <= 0 || c.PageSize <= 0 || c.MaxConcurrency <= 0 || c.MaxConcurrency > c.ClaimBatchSize {
		return errors.New("fanout: invalid batch, page, or concurrency limit")
	}
	if c.LeaseDuration <= 0 || c.TemplateVersion <= 0 {
		return errors.New("fanout: lease duration and template version must be positive")
	}
	return nil
}

type Worker struct {
	store    Store
	observer Observer
	config   Config
	now      func() time.Time
	newID    func() string
}

func New(repository Store, observer Observer, config Config) (*Worker, error) {
	if repository == nil {
		return nil, errors.New("fanout: store is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Worker{store: repository, observer: observer, config: config,
		now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString}, nil
}

func (w *Worker) HandleMessage(ctx context.Context, data []byte) error {
	envelope, err := bus.DecodeEventEnvelope(data)
	if err != nil {
		return fmt.Errorf("fanout: decode event envelope: %w", err)
	}
	_, _, err = w.store.EnsureFanoutPlan(ctx, string(envelope.EventID), w.config.ShardCount)
	return err
}

type Stats struct {
	Claimed      int   `json:"claimed"`
	Completed    int   `json:"completed"`
	Inserted     int64 `json:"inserted"`
	InvalidRules int   `json:"invalid_rules"`
	Failed       int   `json:"failed"`
}

type shardResult struct {
	completed    bool
	inserted     int64
	invalidRules int
	err          error
}

func (w *Worker) RunOnce(ctx context.Context) (Stats, error) {
	if w == nil || w.store == nil {
		return Stats{}, errors.New("fanout: worker is not initialized")
	}
	leases, err := w.store.ClaimFanoutShards(ctx, w.config.Owner, w.config.ClaimBatchSize, w.config.LeaseDuration)
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Claimed: len(leases)}
	results := make(chan shardResult, len(leases))
	semaphore := make(chan struct{}, w.config.MaxConcurrency)
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
				results <- shardResult{err: ctx.Err()}
				return
			}
			results <- w.processShard(ctx, lease)
		}()
	}
	group.Wait()
	close(results)
	var failures []error
	for result := range results {
		stats.Inserted += result.inserted
		stats.InvalidRules += result.invalidRules
		if result.completed {
			stats.Completed++
		}
		if result.err != nil {
			stats.Failed++
			failures = append(failures, result.err)
		}
	}
	return stats, errors.Join(failures...)
}

func (w *Worker) processShard(ctx context.Context, lease store.FanoutShardLease) shardResult {
	started := w.now()
	event, err := w.store.LoadFanoutEvent(ctx, lease.EventID)
	if err != nil {
		w.observe(ctx, "error", 0, started)
		return shardResult{err: err}
	}
	candidates, cursor, completed, err := w.store.LoadFanoutCandidates(ctx, lease, w.config.PageSize)
	if err != nil {
		w.observe(ctx, "error", 0, started)
		return shardResult{err: err}
	}
	deliveries := make([]store.FanoutDelivery, 0, len(candidates))
	invalidRules := 0
	for _, candidate := range candidates {
		decision, evaluateErr := subscription.Evaluate(candidate.Rule, subscription.Event{
			Kind: event.Kind, Payload: event.Payload, ObservedAt: event.ObservedAt,
		}, w.now())
		if evaluateErr != nil {
			invalidRules++
			continue
		}
		if !decision.Matched {
			continue
		}
		deliveries = append(deliveries, store.FanoutDelivery{
			ID: w.newID(), SubscriptionID: candidate.SubscriptionID, EndpointID: candidate.EndpointID,
			RuleVersion: candidate.RuleVersion, TemplateVersion: w.config.TemplateVersion,
			Priority: decision.Priority, EligibleAt: decision.EligibleAt,
		})
	}
	inserted, err := w.store.CommitFanoutShard(ctx, store.CommitFanoutShardParams{
		PlanID: lease.PlanID, ShardNumber: lease.ShardNumber, LeaseToken: lease.LeaseToken,
		CursorSubscriptionID: cursor, Completed: completed, Deliveries: deliveries,
	})
	if err != nil {
		w.observe(ctx, "error", 0, started)
		return shardResult{invalidRules: invalidRules, err: err}
	}
	result := "checkpoint"
	if completed {
		result = "completed"
	}
	w.observe(ctx, result, inserted, started)
	return shardResult{completed: completed, inserted: inserted, invalidRules: invalidRules}
}

func (w *Worker) observe(ctx context.Context, result string, inserted int64, started time.Time) {
	if w.observer != nil {
		w.observer.RecordFanout(ctx, result, inserted, w.now().Sub(started))
	}
}
