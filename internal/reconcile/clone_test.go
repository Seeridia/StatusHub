package reconcile

import (
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"testing"
	"time"
)

func TestCloneIncidentPreservesIndependentTimeline(t *testing.T) {
	now := time.Now().UTC()
	original := domain.Incident{ID: "incident", Updates: []domain.IncidentUpdate{{ID: "update", Body: "Official update", Phase: domain.IncidentPhaseMonitoring, ComponentIDs: []string{"api"}, SourceUpdatedAt: &now}}}
	cloned := cloneIncident(original)
	if cloned.Updates[0].Body != "Official update" || cloned.Updates[0].ID != "update" || cloned.Updates[0].Phase != domain.IncidentPhaseMonitoring {
		t.Fatalf("lost timeline: %+v", cloned.Updates)
	}
	cloned.Updates[0].Body = "changed"
	cloned.Updates[0].ComponentIDs[0] = "changed"
	*cloned.Updates[0].SourceUpdatedAt = now.Add(time.Hour)
	if original.Updates[0].Body != "Official update" || original.Updates[0].ComponentIDs[0] != "api" || !original.Updates[0].SourceUpdatedAt.Equal(now) {
		t.Fatal("cloned timeline aliases original")
	}
}
