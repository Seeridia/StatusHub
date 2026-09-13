package ecosystem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const EngineCState = "cstate-v2"

type cstatePayload struct {
	Is            string         `json:"is"`
	CStateVersion string         `json:"cStateVersion"`
	APIVersion    string         `json:"apiVersion"`
	Title         string         `json:"title"`
	BaseURL       string         `json:"baseURL"`
	SummaryStatus string         `json:"summaryStatus"`
	BuildDate     string         `json:"buildDate"`
	Systems       []cstateSystem `json:"systems"`
}

type cstateSystem struct {
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	Category         string        `json:"category"`
	Status           string        `json:"status"`
	UnresolvedIssues []cstateIssue `json:"unresolvedIssues"`
}

type cstateIssue struct {
	Is            string   `json:"is"`
	Title         string   `json:"title"`
	CreatedAt     string   `json:"createdAt"`
	LastMod       string   `json:"lastMod"`
	Permalink     string   `json:"permalink"`
	Filename      string   `json:"filename"`
	Severity      string   `json:"severity"`
	Status        string   `json:"status"`
	Resolved      bool     `json:"resolved"`
	Informational bool     `json:"informational"`
	Affected      []string `json:"affected"`
}

func definitionCState() engineDefinition {
	return engineDefinition{name: EngineCState, version: "api-v2", confidence: 1, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{{resource: domain.ResourceSummary, path: "/index.json", required: true,
			completeness:     domain.CompletenessComplete,
			authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents},
			decode:           decodeCState}}, nil
	}}
}

func decodeCState(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields["systems"] == nil || fields["summaryStatus"] == nil {
		return domain.Snapshot{}, fmt.Errorf("%w: cState systems and summaryStatus are required", ErrInvalid)
	}
	var payload cstatePayload
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: cState: %v", ErrInvalid, err)
	}
	if payload.Is != "index" || payload.Title == "" || payload.SummaryStatus == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: cState index identity, title and status are required", ErrInvalid)
	}
	components := make([]domain.Component, 0, len(payload.Systems))
	incidentByID := make(map[string]*domain.Incident)
	for index, system := range payload.Systems {
		if strings.TrimSpace(system.Name) == "" || strings.TrimSpace(system.Status) == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: cState system[%d] requires name and status", ErrInvalid, index)
		}
		componentID := stableID("cstate-system", system.Category, system.Name)
		components = append(components, domain.Component{ID: componentID, Name: system.Name, Description: system.Description,
			GroupID: system.Category, Status: normalizeComponentStatus(system.Status), RawStatus: system.Status})
		for _, issue := range system.UnresolvedIssues {
			if strings.TrimSpace(issue.Title) == "" {
				continue
			}
			key := firstNonEmpty(issue.Filename, issue.Permalink, issue.Title)
			incidentID := stableID("cstate-issue", key)
			incident := incidentByID[incidentID]
			if incident == nil {
				kind := domain.IncidentKindIncident
				if strings.Contains(strings.ToLower(issue.Title), "maintenance") || strings.Contains(strings.ToLower(issue.Status), "maintenance") {
					kind = domain.IncidentKindMaintenance
				}
				impact := normalizeImpact(issue.Severity)
				if issue.Informational && impact == domain.ImpactUnknown {
					impact = domain.ImpactNone
				}
				value := domain.Incident{ID: incidentID, UpstreamID: issue.Filename, Kind: kind, Name: issue.Title,
					URL: validateAbsoluteHTTP(issue.Permalink), Phase: normalizePhase(issue.Status, issue.Resolved), RawPhase: issue.Status,
					Impact: impact, RawImpact: issue.Severity, StartedAt: parseTime(issue.CreatedAt),
					ResolvedAt: nil, SourceCreatedAt: parseTime(issue.CreatedAt), SourceUpdatedAt: parseTime(issue.LastMod)}
				if value.Phase == domain.IncidentPhaseUnknown {
					value.Phase = domain.IncidentPhaseInvestigating
				}
				incidentByID[incidentID] = &value
				incident = &value
			}
			incident.ComponentIDs = append(incident.ComponentIDs, componentID)
		}
	}
	incidents := make([]domain.Incident, 0, len(incidentByID))
	for _, incident := range incidentByID {
		incident.ComponentIDs = dedupeStrings(incident.ComponentIDs)
		incidents = append(incidents, *incident)
	}
	source = sourceForEngine(source, EngineCState)
	if source.CanonicalURL == nil && validateAbsoluteHTTP(payload.BaseURL) != "" {
		source.CanonicalURL = incidentIOURL(payload.BaseURL)
	}
	overall := normalizeComponentStatus(payload.SummaryStatus)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall,
		RawOverallStatus: payload.SummaryStatus, ComputedStatus: computedStatus(components), Components: components,
		Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents},
		SourceUpdatedAt:  parseTime(payload.BuildDate), ObservedAt: observedAt,
		SchemaVersion: firstNonEmpty(payload.APIVersion, "2"), AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}
