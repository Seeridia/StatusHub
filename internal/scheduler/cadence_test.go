package scheduler

import (
	"sync"
	"testing"
	"time"
)

var schedulerEpoch = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

type fractionRandom struct {
	numerator   int64
	denominator int64
}

func (random fractionRandom) Int63n(n int64) int64 {
	return n * random.numerator / random.denominator
}

func TestAdaptiveCadenceTransitions(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	policy, err := NewPolicy(config, fractionRandom{numerator: 1, denominator: 2})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	active, err := policy.Next(State{}, Signal{
		At:                schedulerEpoch,
		Outcome:           OutcomeSuccess,
		HasActiveIncident: true,
	})
	if err != nil {
		t.Fatalf("active: %v", err)
	}
	if active.State.Mode != ModeActive || active.Delay < config.Active.Min || active.Delay > config.Active.Max {
		t.Fatalf("active decision = %#v", active)
	}

	resolved, err := policy.Next(active.State, Signal{
		At:      schedulerEpoch.Add(time.Minute),
		Outcome: OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("resolved: %v", err)
	}
	if resolved.State.Mode != ModeHot || !resolved.State.LastChangeAt.Equal(schedulerEpoch.Add(time.Minute)) {
		t.Fatalf("active -> hot = %#v", resolved)
	}

	stillHot, err := policy.Next(resolved.State, Signal{
		At:      resolved.State.LastChangeAt.Add(config.HotFor - time.Nanosecond),
		Outcome: OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("still hot: %v", err)
	}
	if stillHot.State.Mode != ModeHot {
		t.Fatalf("mode before hot window = %q", stillHot.State.Mode)
	}

	warm, err := policy.Next(stillHot.State, Signal{
		At:      resolved.State.LastChangeAt.Add(config.HotFor),
		Outcome: OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("warm: %v", err)
	}
	if warm.State.Mode != ModeWarm || warm.Delay < config.Warm.Min || warm.Delay > config.Warm.Max {
		t.Fatalf("warm decision = %#v", warm)
	}

	stable, err := policy.Next(warm.State, Signal{
		At:      resolved.State.LastChangeAt.Add(config.StableAfter),
		Outcome: OutcomeSuccess,
	})
	if err != nil {
		t.Fatalf("stable: %v", err)
	}
	if stable.State.Mode != ModeStable || stable.Delay < config.Stable.Min || stable.Delay > config.Stable.Max {
		t.Fatalf("stable decision = %#v", stable)
	}

	changed, err := policy.Next(stable.State, Signal{
		At:      schedulerEpoch.Add(time.Hour),
		Outcome: OutcomeSuccess,
		Changed: true,
	})
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if changed.State.Mode != ModeHot || !changed.State.LastChangeAt.Equal(schedulerEpoch.Add(time.Hour)) {
		t.Fatalf("stable -> hot = %#v", changed)
	}
}

func TestInitialUnchangedSuccessStartsStable(t *testing.T) {
	t.Parallel()

	policy, err := NewPolicy(DefaultConfig(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	decision, err := policy.Next(State{}, Signal{At: schedulerEpoch, Outcome: OutcomeSuccess})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if decision.State.Mode != ModeStable || decision.Delay != DefaultConfig().Stable.Min {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestBackoffUsesCappedEqualJitterAndRecoversHot(t *testing.T) {
	t.Parallel()

	config := DefaultConfig()
	config.BackoffBase = 10 * time.Second
	config.BackoffMax = 40 * time.Second
	policy, err := NewPolicy(config, fractionRandom{numerator: 1, denominator: 2})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	state := State{Mode: ModeStable}
	wantCaps := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 40 * time.Second}
	for index, cap := range wantCaps {
		decision, nextErr := policy.Next(state, Signal{
			At:      schedulerEpoch.Add(time.Duration(index) * time.Minute),
			Outcome: OutcomeFailure,
		})
		if nextErr != nil {
			t.Fatalf("failure %d: %v", index+1, nextErr)
		}
		if decision.State.Mode != ModeBackoff || decision.State.FailureStreak != uint32(index+1) {
			t.Fatalf("failure %d state = %#v", index+1, decision.State)
		}
		// The injected source returns the midpoint of [cap/2, cap] (nanosecond
		// precision makes the inclusive upper bound differ by at most 1ns).
		want := cap * 3 / 4
		if difference := decision.Delay - want; difference < 0 || difference > time.Nanosecond {
			t.Fatalf("failure %d delay = %s, want ~%s", index+1, decision.Delay, want)
		}
		state = decision.State
	}

	recoveredAt := schedulerEpoch.Add(5 * time.Minute)
	recovered, err := policy.Next(state, Signal{At: recoveredAt, Outcome: OutcomeSuccess})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if recovered.State.Mode != ModeHot || recovered.State.FailureStreak != 0 || !recovered.State.LastChangeAt.Equal(recoveredAt) {
		t.Fatalf("recovered state = %#v", recovered.State)
	}
}

func TestUpstreamAndCacheBoundsOverrideJitter(t *testing.T) {
	t.Parallel()

	policy, err := NewPolicy(DefaultConfig(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	decision, err := policy.Next(State{Mode: ModeStable}, Signal{
		At:           schedulerEpoch,
		Outcome:      OutcomeFailure,
		NotBefore:    schedulerEpoch.Add(2 * time.Minute),
		MinimumDelay: time.Minute,
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if decision.Delay != 2*time.Minute || !decision.NextPollAt.Equal(schedulerEpoch.Add(2*time.Minute)) {
		t.Fatalf("bounded decision = %#v", decision)
	}
}

func TestChangeAndActiveIncidentPreferActiveMode(t *testing.T) {
	t.Parallel()

	policy, err := NewPolicy(DefaultConfig(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	decision, err := policy.Next(State{Mode: ModeStable}, Signal{
		At:                schedulerEpoch,
		Outcome:           OutcomeSuccess,
		Changed:           true,
		HasActiveIncident: true,
	})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if decision.State.Mode != ModeActive || !decision.State.LastChangeAt.Equal(schedulerEpoch) {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestClockCorrectionDoesNotPrematurelyCool(t *testing.T) {
	t.Parallel()

	policy, err := NewPolicy(DefaultConfig(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}
	decision, err := policy.Next(State{
		Mode:         ModeWarm,
		LastChangeAt: schedulerEpoch.Add(time.Hour),
	}, Signal{At: schedulerEpoch, Outcome: OutcomeSuccess})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if decision.State.Mode != ModeHot {
		t.Fatalf("clock correction cooled to %q", decision.State.Mode)
	}
}

func TestInvalidConfigurationAndSignalsAreRejected(t *testing.T) {
	t.Parallel()

	badConfig := DefaultConfig()
	badConfig.Active.Max = 0
	if _, err := NewPolicy(badConfig, fractionRandom{numerator: 0, denominator: 1}); err == nil {
		t.Fatal("invalid interval was accepted")
	}

	policy := NewDefaultPolicy()
	tests := []struct {
		name   string
		state  State
		signal Signal
	}{
		{name: "missing time", signal: Signal{Outcome: OutcomeSuccess}},
		{name: "missing outcome", signal: Signal{At: schedulerEpoch}},
		{name: "bad mode", state: State{Mode: "surprise"}, signal: Signal{At: schedulerEpoch, Outcome: OutcomeSuccess}},
		{name: "negative minimum", signal: Signal{At: schedulerEpoch, Outcome: OutcomeSuccess, MinimumDelay: -1}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.Next(test.state, test.signal); err == nil {
				t.Fatal("invalid signal was accepted")
			}
		})
	}

	var nilPolicy *Policy
	if _, err := nilPolicy.Next(State{}, Signal{At: schedulerEpoch, Outcome: OutcomeSuccess}); err == nil {
		t.Fatal("nil policy was accepted")
	}
}

func TestPolicySerializesInjectedRandomForConcurrentUse(t *testing.T) {
	policy, err := NewPolicy(DefaultConfig(), &detectConcurrentRandom{})
	if err != nil {
		t.Fatalf("new policy: %v", err)
	}

	var wait sync.WaitGroup
	for index := 0; index < 64; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, nextErr := policy.Next(State{}, Signal{At: schedulerEpoch, Outcome: OutcomeSuccess}); nextErr != nil {
				t.Errorf("next: %v", nextErr)
			}
		}()
	}
	wait.Wait()
}

type detectConcurrentRandom struct {
	mu     sync.Mutex
	active bool
}

func (random *detectConcurrentRandom) Int63n(n int64) int64 {
	random.mu.Lock()
	if random.active {
		random.mu.Unlock()
		panic("concurrent random access")
	}
	random.active = true
	random.mu.Unlock()

	time.Sleep(time.Microsecond)

	random.mu.Lock()
	random.active = false
	random.mu.Unlock()
	return n / 2
}

func TestUnchangedUnresolvedIncidentCoolsAndReactivatesOnChange(t *testing.T) {
	policy := NewDefaultPolicy()
	active, err := policy.Next(State{}, Signal{At: schedulerEpoch, Outcome: OutcomeSuccess, HasActiveIncident: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, elapsed := range []time.Duration{30 * time.Minute, time.Hour} {
		cooled, err := policy.Next(active.State, Signal{At: schedulerEpoch.Add(elapsed), Outcome: OutcomeSuccess, HasActiveIncident: true})
		if err != nil || cooled.State.Mode != ModeStable {
			t.Fatalf("unchanged incident did not cool: %+v %v", cooled, err)
		}
		active = cooled
	}
	changed, err := policy.Next(active.State, Signal{At: schedulerEpoch.Add(2 * time.Hour), Outcome: OutcomeSuccess, HasActiveIncident: true, Changed: true})
	if err != nil || changed.State.Mode != ModeActive {
		t.Fatalf("new update did not reactivate: %+v %v", changed, err)
	}
}
