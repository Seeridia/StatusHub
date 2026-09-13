// Package outbox moves transactionally committed outbox rows to the durable
// bus. Rows are claimed with a PostgreSQL lease, published outside the
// transaction, then completed with a fencing token.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type Store interface {
	ClaimOutbox(context.Context, string, int, time.Duration) ([]store.OutboxLease, error)
	MarkOutboxPublished(context.Context, string, string, time.Time) error
	FailOutbox(context.Context, store.FailOutboxParams) error
}

type Bus interface {
	Publish(context.Context, bus.Message) (bus.PublishResult, error)
}

type Observer interface {
	RecordOutboxPublish(context.Context, string, string, time.Duration)
}

type Config struct {
	Owner           string
	BatchSize       int
	MaxConcurrency  int
	LeaseDuration   time.Duration
	RetryBase       time.Duration
	RetryMaximum    time.Duration
	MaximumErrorLen int
}

func DefaultConfig(owner string) Config {
	return Config{
		Owner: owner, BatchSize: 128, MaxConcurrency: 16,
		LeaseDuration: 30 * time.Second,
		RetryBase:     250 * time.Millisecond, RetryMaximum: time.Minute,
		MaximumErrorLen: 1024,
	}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Owner) == "" {
		return errors.New("outbox publisher owner is required")
	}
	if c.BatchSize <= 0 || c.MaxConcurrency <= 0 || c.MaxConcurrency > c.BatchSize {
		return errors.New("outbox publisher batch/concurrency is invalid")
	}
	if c.LeaseDuration <= 0 || c.RetryBase <= 0 || c.RetryMaximum < c.RetryBase {
		return errors.New("outbox publisher durations are invalid")
	}
	if c.MaximumErrorLen <= 0 {
		return errors.New("outbox publisher maximum error length must be positive")
	}
	return nil
}

type random interface {
	Int63n(int64) int64
}

type Publisher struct {
	store    Store
	bus      Bus
	observer Observer
	config   Config
	random   random
	randomMu sync.Mutex
	now      func() time.Time
}

func New(repository Store, eventBus Bus, observer Observer, config Config) (*Publisher, error) {
	if repository == nil || eventBus == nil {
		return nil, errors.New("outbox publisher store and bus are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Publisher{
		store: repository, bus: eventBus, observer: observer, config: config,
		random: rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404 -- retry jitter is not cryptographic.
		now:    func() time.Time { return time.Now().UTC() },
	}, nil
}

type Stats struct {
	Claimed    int `json:"claimed"`
	Published  int `json:"published"`
	Duplicates int `json:"duplicates"`
	Failed     int `json:"failed"`
}

type processResult struct {
	duplicate bool
	err       error
}

func (p *Publisher) RunOnce(ctx context.Context) (Stats, error) {
	if p == nil || p.store == nil || p.bus == nil {
		return Stats{}, errors.New("outbox publisher is not initialized")
	}
	leases, err := p.store.ClaimOutbox(ctx, p.config.Owner, p.config.BatchSize, p.config.LeaseDuration)
	if err != nil {
		return Stats{}, fmt.Errorf("claim outbox: %w", err)
	}
	stats := Stats{Claimed: len(leases)}
	if len(leases) == 0 {
		return stats, nil
	}

	results := make(chan processResult, len(leases))
	semaphore := make(chan struct{}, p.config.MaxConcurrency)
	var workers sync.WaitGroup
	for _, lease := range leases {
		lease := lease
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- processResult{err: ctx.Err()}
				return
			}
			results <- p.publishOne(ctx, lease)
		}()
	}
	workers.Wait()
	close(results)

	var joined []error
	for result := range results {
		if result.err != nil {
			stats.Failed++
			joined = append(joined, result.err)
			continue
		}
		stats.Published++
		if result.duplicate {
			stats.Duplicates++
		}
	}
	return stats, errors.Join(joined...)
}

func (p *Publisher) publishOne(ctx context.Context, lease store.OutboxLease) processResult {
	age := p.now().Sub(lease.CreatedAt)
	if age < 0 {
		age = 0
	}
	ack, err := p.bus.Publish(ctx, bus.Message{
		ID: lease.ID, Subject: lease.Subject,
		ContentType: bus.ContentTypeJSON, Data: lease.Payload,
	})
	if err != nil {
		failure := p.store.FailOutbox(ctx, store.FailOutboxParams{
			ID: lease.ID, LeaseToken: lease.LeaseToken,
			AvailableAt: p.now().Add(p.retryDelay(lease.Attempts)),
			LastError:   truncate(err.Error(), p.config.MaximumErrorLen),
		})
		p.observe(ctx, lease.Subject, "error", age)
		return processResult{err: errors.Join(err, failure)}
	}
	if err := p.store.MarkOutboxPublished(ctx, lease.ID, lease.LeaseToken, p.now()); err != nil {
		// The broker accepted this message. Do not release its lease as failed:
		// expiration will cause a safe republish with the same stable message ID.
		p.observe(ctx, lease.Subject, "mark_error", age)
		return processResult{duplicate: ack.Duplicate, err: fmt.Errorf("mark outbox %s published: %w", lease.ID, err)}
	}
	resultClass := "success"
	if ack.Duplicate {
		resultClass = "duplicate"
	}
	p.observe(ctx, lease.Subject, resultClass, age)
	return processResult{duplicate: ack.Duplicate}
}

func (p *Publisher) observe(ctx context.Context, subject, result string, age time.Duration) {
	if p.observer != nil {
		p.observer.RecordOutboxPublish(ctx, subjectClass(subject), result, age)
	}
}

func (p *Publisher) retryDelay(attempt int) time.Duration {
	cap := p.config.RetryBase
	for value := 1; value < attempt && cap < p.config.RetryMaximum; value++ {
		if cap >= p.config.RetryMaximum/2 {
			cap = p.config.RetryMaximum
			break
		}
		cap *= 2
	}
	if cap > p.config.RetryMaximum {
		cap = p.config.RetryMaximum
	}
	p.randomMu.Lock()
	defer p.randomMu.Unlock()
	return time.Duration(p.random.Int63n(int64(cap) + 1))
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func subjectClass(subject string) string {
	tokens := strings.Split(subject, ".")
	if len(tokens) == 0 {
		return "unknown"
	}
	switch tokens[len(tokens)-1] {
	case "critical", "normal", "bulk":
		return tokens[len(tokens)-1]
	default:
		return "other"
	}
}
