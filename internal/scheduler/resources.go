package scheduler

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

// ResourceCadence lets a small active-incidents endpoint run faster than a
// larger summary reconciliation endpoint while retaining one source identity.
type ResourceCadence struct {
	Resource domain.ResourceKind
	Active   Interval
	Hot      Interval
	Warm     Interval
	Stable   Interval
}

// StatuspageCadences is shared by the supported adapter families. Keep lightweight
// incident detection ahead of reconciliation without sub-minute polling.
func StatuspageCadences() []ResourceCadence {
	incident := DefaultConfig()
	var profiles []ResourceCadence
	for _, resource := range []domain.ResourceKind{
		domain.ResourceStatus, domain.ResourceComponents, domain.ResourceIncidents,
		domain.ResourceUnresolvedIncidents, domain.ResourceSummary, domain.ResourceScheduledMaintenances,
	} {
		profile := ResourceCadence{
			Resource: resource, Active: incident.Active, Hot: incident.Hot,
			Warm: incident.Warm, Stable: incident.Stable,
		}
		switch resource {
		case domain.ResourceStatus:
			profile.Active = Interval{Min: 90 * time.Second, Max: 2 * time.Minute}
		case domain.ResourceComponents, domain.ResourceSummary:
			profile.Active = Interval{Min: 2 * time.Minute, Max: 3 * time.Minute}
			profile.Hot = incident.Warm
		case domain.ResourceScheduledMaintenances:
			profile.Active = Interval{Min: 5 * time.Minute, Max: 10 * time.Minute}
			profile.Hot, profile.Warm = profile.Active, profile.Active
			profile.Stable = Interval{Min: 10 * time.Minute, Max: 15 * time.Minute}
		}
		profiles = append(profiles, profile)
	}
	return profiles
}

type ResourceState struct {
	Resource   domain.ResourceKind `json:"resource"`
	NextPollAt time.Time           `json:"next_poll_at"`
}

type ResourcePlanner struct {
	profiles map[domain.ResourceKind]ResourceCadence
	random   Random
	mu       sync.Mutex
}

func NewResourcePlanner(profiles []ResourceCadence, random Random) (*ResourcePlanner, error) {
	if len(profiles) == 0 {
		return nil, errors.New("scheduler: resource cadence profiles are required")
	}
	if random == nil {
		random = &lockedSource{value: uint64(time.Now().UnixNano())}
	}
	result := &ResourcePlanner{profiles: make(map[domain.ResourceKind]ResourceCadence, len(profiles)), random: random}
	for _, profile := range profiles {
		if !profile.Resource.Valid() {
			return nil, fmt.Errorf("scheduler: invalid cadence resource %q", profile.Resource)
		}
		if _, duplicate := result.profiles[profile.Resource]; duplicate {
			return nil, fmt.Errorf("scheduler: duplicate cadence resource %q", profile.Resource)
		}
		for name, interval := range map[string]Interval{
			"active": profile.Active, "hot": profile.Hot, "warm": profile.Warm, "stable": profile.Stable,
		} {
			if interval.Min <= 0 || interval.Max < interval.Min {
				return nil, fmt.Errorf("scheduler: invalid %s cadence for %q", name, profile.Resource)
			}
		}
		result.profiles[profile.Resource] = profile
	}
	return result, nil
}

// Due returns every endpoint whose independent deadline has elapsed. The
// order is stable so competing workers make the same choice.
func (p *ResourcePlanner) Due(states []ResourceState, now time.Time) []domain.ResourceKind {
	if p == nil || now.IsZero() {
		return nil
	}
	deadlines := make(map[domain.ResourceKind]time.Time, len(states))
	for _, state := range states {
		deadlines[state.Resource] = state.NextPollAt
	}
	due := make([]domain.ResourceKind, 0, len(p.profiles))
	for resource := range p.profiles {
		deadline, found := deadlines[resource]
		if !found || deadline.IsZero() || !deadline.After(now) {
			due = append(due, resource)
		}
	}
	slices.Sort(due)
	return due
}

// Next schedules a single endpoint. minimumDelay reflects Cache-Control/CDN
// freshness and notBefore reflects Retry-After; both override local jitter.
func (p *ResourcePlanner) Next(resource domain.ResourceKind, mode Mode, at time.Time, minimumDelay time.Duration, notBefore time.Time) (ResourceState, error) {
	if p == nil {
		return ResourceState{}, errors.New("scheduler: nil resource planner")
	}
	profile, found := p.profiles[resource]
	if !found {
		return ResourceState{}, fmt.Errorf("scheduler: no cadence profile for %q", resource)
	}
	if !mode.Valid() || mode == ModeBackoff {
		return ResourceState{}, fmt.Errorf("scheduler: resource cadence requires a successful mode, got %q", mode)
	}
	if at.IsZero() || minimumDelay < 0 {
		return ResourceState{}, errors.New("scheduler: invalid resource scheduling time or minimum delay")
	}
	interval := profile.Stable
	switch mode {
	case ModeActive:
		interval = profile.Active
	case ModeHot:
		interval = profile.Hot
	case ModeWarm:
		interval = profile.Warm
	case ModeStable:
		interval = profile.Stable
	}
	delay := interval.Min
	if interval.Max > interval.Min {
		p.mu.Lock()
		delay += time.Duration(p.random.Int63n(int64(interval.Max-interval.Min) + 1))
		p.mu.Unlock()
	}
	if minimumDelay > delay {
		delay = minimumDelay
	}
	if wait := notBefore.Sub(at); !notBefore.IsZero() && wait > delay {
		delay = wait
	}
	return ResourceState{Resource: resource, NextPollAt: at.Add(delay)}, nil
}

// lockedSource is a tiny deterministic xorshift source used only when callers
// do not inject math/rand. ResourcePlanner serializes access to it.
type lockedSource struct{ value uint64 }

func (source *lockedSource) Int63n(n int64) int64 {
	if n <= 0 {
		panic("scheduler: Int63n called with non-positive bound")
	}
	value := source.value
	value ^= value << 13
	value ^= value >> 7
	value ^= value << 17
	source.value = value
	return int64(value&((1<<63)-1)) % n
}
