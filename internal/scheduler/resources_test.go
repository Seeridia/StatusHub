package scheduler

import (
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

func TestStatuspageResourcesUseIndependentCadences(t *testing.T) {
	planner, err := NewResourcePlanner(StatuspageCadences(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("NewResourcePlanner() error = %v", err)
	}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	unresolved, err := planner.Next(domain.ResourceUnresolvedIncidents, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule unresolved: %v", err)
	}
	incidents, err := planner.Next(domain.ResourceIncidents, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule incidents: %v", err)
	}
	summary, err := planner.Next(domain.ResourceSummary, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule summary: %v", err)
	}
	if got := unresolved.NextPollAt.Sub(now); got != 60*time.Second {
		t.Fatalf("unresolved delay = %s, want 60s", got)
	}
	if got := summary.NextPollAt.Sub(now); got != 2*time.Minute {
		t.Fatalf("summary delay = %s, want 2m", got)
	}
	if !unresolved.NextPollAt.Before(summary.NextPollAt) {
		t.Fatal("active incidents endpoint must poll before summary reconciliation")
	}
	status, err := planner.Next(domain.ResourceStatus, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule status: %v", err)
	}
	components, err := planner.Next(domain.ResourceComponents, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule components: %v", err)
	}
	maintenance, err := planner.Next(domain.ResourceScheduledMaintenances, ModeActive, now, 0, time.Time{})
	if err != nil {
		t.Fatalf("schedule maintenance: %v", err)
	}

	due := planner.Due([]ResourceState{incidents, unresolved, summary, status, components, maintenance}, now.Add(61*time.Second))
	if len(due) != 2 || due[0] != domain.ResourceIncidents || due[1] != domain.ResourceUnresolvedIncidents {
		t.Fatalf("Due() = %v, want incidents and unresolved incidents", due)
	}
}

func TestResourceCadenceHonorsUpstreamBounds(t *testing.T) {
	planner, err := NewResourcePlanner(StatuspageCadences(), fractionRandom{numerator: 0, denominator: 1})
	if err != nil {
		t.Fatalf("NewResourcePlanner() error = %v", err)
	}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	notBefore := now.Add(8 * time.Minute)
	state, err := planner.Next(domain.ResourceUnresolvedIncidents, ModeStable, now, 90*time.Second, notBefore)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !state.NextPollAt.Equal(notBefore) {
		t.Fatalf("NextPollAt = %s, want %s", state.NextPollAt, notBefore)
	}
}
