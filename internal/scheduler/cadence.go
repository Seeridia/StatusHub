// Package scheduler contains the database-independent adaptive polling state
// machine. Leasing and compare-and-swap persistence live in the store layer;
// this package only decides the next cadence state and deadline.
package scheduler

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

type Mode string

const (
	ModeActive  Mode = "active"
	ModeHot     Mode = "hot"
	ModeWarm    Mode = "warm"
	ModeStable  Mode = "stable"
	ModeBackoff Mode = "backoff"
)

func (mode Mode) Valid() bool {
	switch mode {
	case ModeActive, ModeHot, ModeWarm, ModeStable, ModeBackoff:
		return true
	default:
		return false
	}
}

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

type Interval struct {
	Min time.Duration
	Max time.Duration
}

// Config defines cadence ranges and transition windows. Each successful mode
// draws uniformly from its inclusive interval. Backoff uses equal jitter in
// [cap/2, cap], where cap = min(BackoffBase*2^(failure-1), BackoffMax).
type Config struct {
	Active      Interval
	Hot         Interval
	Warm        Interval
	Stable      Interval
	HotFor      time.Duration
	StableAfter time.Duration
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

func DefaultConfig() Config {
	return Config{
		Active:      Interval{Min: 60 * time.Second, Max: 90 * time.Second},
		Hot:         Interval{Min: 2 * time.Minute, Max: 3 * time.Minute},
		Warm:        Interval{Min: 3 * time.Minute, Max: 4 * time.Minute},
		Stable:      Interval{Min: 4 * time.Minute, Max: 5 * time.Minute},
		HotFor:      10 * time.Minute,
		StableAfter: 30 * time.Minute,
		BackoffBase: time.Minute,
		BackoffMax:  15 * time.Minute,
	}
}

func (config Config) Validate() error {
	for name, interval := range map[string]Interval{
		"active": config.Active,
		"hot":    config.Hot,
		"warm":   config.Warm,
		"stable": config.Stable,
	} {
		if interval.Min <= 0 || interval.Max <= 0 || interval.Max < interval.Min {
			return fmt.Errorf("scheduler: invalid %s interval [%s,%s]", name, interval.Min, interval.Max)
		}
	}
	if config.HotFor <= 0 {
		return errors.New("scheduler: hot duration must be positive")
	}
	if config.StableAfter <= config.HotFor {
		return errors.New("scheduler: stable-after duration must exceed hot duration")
	}
	if config.BackoffBase <= 0 || config.BackoffMax < config.BackoffBase {
		return errors.New("scheduler: invalid backoff range")
	}
	return nil
}

type State struct {
	Mode          Mode      `json:"mode"`
	FailureStreak uint32    `json:"failure_streak"`
	LastChangeAt  time.Time `json:"last_change_at,omitempty"`
}

// Signal describes one completed poll. NotBefore is an absolute upstream
// constraint (for example Retry-After); MinimumDelay represents cache/CDN
// freshness. Both are lower bounds applied after jitter.
type Signal struct {
	At                time.Time
	Outcome           Outcome
	Changed           bool
	HasActiveIncident bool
	NotBefore         time.Time
	MinimumDelay      time.Duration
}

type Decision struct {
	State      State
	Delay      time.Duration
	NextPollAt time.Time
}

// Random is the minimal injectable random source needed for jitter.
// Int63n must follow math/rand.Rand's contract and return a value in [0,n).
type Random interface {
	Int63n(n int64) int64
}

// Policy is safe for concurrent callers, including when the supplied Random
// itself is not concurrency-safe (as is the case for math/rand.Rand).
type Policy struct {
	config Config
	random Random
	mu     sync.Mutex
}

func NewPolicy(config Config, random Random) (*Policy, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if random == nil {
		random = rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404 -- scheduling jitter is not cryptographic.
	}
	return &Policy{config: config, random: random}, nil
}

func NewDefaultPolicy() *Policy {
	policy, err := NewPolicy(DefaultConfig(), nil)
	if err != nil {
		panic(err) // DefaultConfig is a package invariant.
	}
	return policy
}

// Next advances the state machine exactly once and chooses a jittered delay.
func (policy *Policy) Next(previous State, signal Signal) (Decision, error) {
	if policy == nil {
		return Decision{}, errors.New("scheduler: nil policy")
	}
	if signal.At.IsZero() {
		return Decision{}, errors.New("scheduler: signal time is required")
	}
	if signal.Outcome != OutcomeSuccess && signal.Outcome != OutcomeFailure {
		return Decision{}, fmt.Errorf("scheduler: invalid outcome %q", signal.Outcome)
	}
	if previous.Mode != "" && !previous.Mode.Valid() {
		return Decision{}, fmt.Errorf("scheduler: invalid previous mode %q", previous.Mode)
	}
	if signal.MinimumDelay < 0 {
		return Decision{}, errors.New("scheduler: minimum delay cannot be negative")
	}

	next := previous
	if signal.Outcome == OutcomeFailure {
		next.Mode = ModeBackoff
		if next.FailureStreak < ^uint32(0) {
			next.FailureStreak++
		}
		cap := policy.backoffCap(next.FailureStreak)
		delay := policy.intervalJitter(Interval{Min: cap / 2, Max: cap})
		return decisionWithBounds(next, signal, delay), nil
	}

	next.FailureStreak = 0
	next.Mode, next.LastChangeAt = policy.successfulMode(previous, signal)
	delay := policy.intervalJitter(policy.interval(next.Mode))
	return decisionWithBounds(next, signal, delay), nil
}

func (policy *Policy) successfulMode(previous State, signal Signal) (Mode, time.Time) {
	lastChangeAt := previous.LastChangeAt
	if signal.Changed {
		lastChangeAt = signal.At
	}
	if signal.HasActiveIncident {
		if lastChangeAt.IsZero() {
			lastChangeAt = signal.At
		}
		// Old unresolved incidents must not keep a source hot indefinitely.
		if signal.At.Sub(lastChangeAt) < policy.config.StableAfter {
			return ModeActive, lastChangeAt
		}
		return ModeStable, lastChangeAt
	}
	if signal.Changed {
		return ModeHot, lastChangeAt
	}
	if previous.Mode == ModeActive || previous.Mode == ModeBackoff {
		return ModeHot, signal.At
	}
	if lastChangeAt.IsZero() {
		return ModeStable, lastChangeAt
	}
	stableFor := signal.At.Sub(lastChangeAt)
	if stableFor < 0 {
		// A clock correction must not prematurely cool a source.
		return ModeHot, lastChangeAt
	}
	if stableFor < policy.config.HotFor {
		return ModeHot, lastChangeAt
	}
	if stableFor < policy.config.StableAfter {
		return ModeWarm, lastChangeAt
	}
	return ModeStable, lastChangeAt
}

func (policy *Policy) interval(mode Mode) Interval {
	switch mode {
	case ModeActive:
		return policy.config.Active
	case ModeHot:
		return policy.config.Hot
	case ModeWarm:
		return policy.config.Warm
	case ModeStable:
		return policy.config.Stable
	default:
		panic("scheduler: interval requested for non-success mode")
	}
}

func (policy *Policy) intervalJitter(interval Interval) time.Duration {
	if interval.Min == interval.Max {
		return interval.Min
	}
	return interval.Min + policy.draw(interval.Max-interval.Min+1)
}

func (policy *Policy) draw(upperExclusive time.Duration) time.Duration {
	if upperExclusive <= 1 {
		return 0
	}
	policy.mu.Lock()
	defer policy.mu.Unlock()
	return time.Duration(policy.random.Int63n(int64(upperExclusive)))
}

func (policy *Policy) backoffCap(streak uint32) time.Duration {
	cap := policy.config.BackoffBase
	for attempt := uint32(1); attempt < streak; attempt++ {
		if cap >= policy.config.BackoffMax/2 {
			return policy.config.BackoffMax
		}
		cap *= 2
	}
	if cap > policy.config.BackoffMax {
		return policy.config.BackoffMax
	}
	return cap
}

func decisionWithBounds(state State, signal Signal, delay time.Duration) Decision {
	if signal.MinimumDelay > delay {
		delay = signal.MinimumDelay
	}
	if !signal.NotBefore.IsZero() {
		until := signal.NotBefore.Sub(signal.At)
		if until > delay {
			delay = until
		}
	}
	if delay < 0 {
		delay = 0
	}
	return Decision{
		State:      state,
		Delay:      delay,
		NextPollAt: signal.At.Add(delay),
	}
}
