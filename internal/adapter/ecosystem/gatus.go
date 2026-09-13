package ecosystem

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const EngineGatus = "gatus-synthetic"

type gatusEndpoint struct {
	Name    string        `json:"name"`
	Group   string        `json:"group"`
	Key     string        `json:"key"`
	Results []gatusResult `json:"results"`
}

type gatusResult struct {
	Status           int    `json:"status"`
	Hostname         string `json:"hostname"`
	Success          bool   `json:"success"`
	Timestamp        string `json:"timestamp"`
	ConditionResults []struct {
		Condition string `json:"condition"`
		Success   bool   `json:"success"`
	} `json:"conditionResults"`
}

func definitionGatus() engineDefinition {
	return engineDefinition{name: EngineGatus, version: "v1", confidence: .99, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{{resource: domain.ResourceSummary, path: "/api/v1/endpoints/statuses", required: true,
			completeness:     domain.CompletenessComplete,
			authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceComponents, domain.ResourceUnresolvedIncidents},
			decode:           decodeGatus}}, nil
	}}
}

func decodeGatus(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var endpoints []gatusEndpoint
	if err := decodeStrict(body, &endpoints); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Gatus endpoint array: %v", ErrInvalid, err)
	}
	components := make([]domain.Component, 0, len(endpoints))
	incidents := make([]domain.Incident, 0)
	for index, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.Name) == "" || strings.TrimSpace(endpoint.Key) == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Gatus endpoint[%d] requires name and key", ErrInvalid, index)
		}
		failed, latest := gatusFailureQuorum(endpoint.Results)
		status := domain.ComponentStatusOperational
		rawStatus := "quorum_passing"
		if failed {
			status, rawStatus = domain.ComponentStatusMajorOutage, "quorum_failed"
		} else if len(endpoint.Results) < 2 {
			status, rawStatus = domain.ComponentStatusUnknown, "insufficient_samples"
		}
		component := domain.Component{ID: endpoint.Key, UpstreamID: endpoint.Key, Name: endpoint.Name,
			GroupID: endpoint.Group, Status: status, RawStatus: rawStatus,
			Tags: map[string]string{"origin": "synthetic", "engine": "gatus", "quorum": "2_of_latest_3"}}
		if latest != nil {
			component.SourceUpdatedAt = parseTime(latest.Timestamp)
		}
		components = append(components, component)
		if !failed {
			continue
		}
		startedAt := component.SourceUpdatedAt
		message := "Gatus recent-result quorum failed"
		if latest != nil {
			message = fmt.Sprintf("Gatus recent-result quorum failed; latest HTTP status=%d host=%s", latest.Status, latest.Hostname)
		}
		incidentID := stableID("gatus-synthetic-incident", endpoint.Key)
		incidents = append(incidents, domain.Incident{ID: incidentID, Kind: domain.IncidentKindIncident,
			Name:  "Synthetic check failed: " + endpoint.Group + "/" + endpoint.Name,
			Phase: domain.IncidentPhaseInvestigating, RawPhase: "synthetic_quorum_failed",
			Impact: domain.ImpactCritical, RawImpact: "synthetic", ComponentIDs: []string{endpoint.Key},
			Updates: []domain.IncidentUpdate{{ID: stableID("gatus-synthetic-update", endpoint.Key, message), IncidentID: incidentID,
				Body: message, Phase: domain.IncidentPhaseInvestigating, RawPhase: "synthetic_quorum_failed", Impact: domain.ImpactCritical,
				SourceCreatedAt: startedAt}}, StartedAt: startedAt, SourceCreatedAt: startedAt, SourceUpdatedAt: startedAt, Synthetic: true})
	}
	source = sourceForEngine(source, EngineGatus)
	source.Kind = domain.SourceKindSynthetic
	overall := computedStatus(components)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall, RawOverallStatus: "synthetic_quorum",
		ComputedStatus: overall, Components: components, Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceComponents, domain.ResourceUnresolvedIncidents},
		ObservedAt:       observedAt, SchemaVersion: "v1", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func gatusFailureQuorum(results []gatusResult) (bool, *gatusResult) {
	if len(results) == 0 {
		return false, nil
	}
	copyOfResults := append([]gatusResult(nil), results...)
	sort.SliceStable(copyOfResults, func(left, right int) bool {
		leftTime, rightTime := parseTime(copyOfResults[left].Timestamp), parseTime(copyOfResults[right].Timestamp)
		if leftTime == nil {
			return false
		}
		if rightTime == nil {
			return true
		}
		return leftTime.After(*rightTime)
	})
	window := copyOfResults
	if len(window) > 3 {
		window = window[:3]
	}
	failures := 0
	for _, result := range window {
		if !result.Success {
			failures++
		}
	}
	return len(window) >= 2 && failures >= 2, &copyOfResults[0]
}

func marshalGatus(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
