package ecosystem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const EngineInstatus = "instatus-v3"

type instatusSummary struct {
	Page struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Status string `json:"status"`
	} `json:"page"`
	Incidents    []instatusIncident `json:"incidents"`
	Maintenances []instatusIncident `json:"maintenances"`
}

type instatusComponents struct {
	Components []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Status      string `json:"status"`
		Group       *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"group"`
	} `json:"components"`
}

type instatusIncident struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Impact      string `json:"impact"`
	URL         string `json:"url"`
	Started     string `json:"started"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	ResolvedAt  string `json:"resolvedAt"`
	ScheduledAt string `json:"scheduledAt"`
	Duration    int64  `json:"duration"`
	Components  []struct {
		ID string `json:"id"`
	} `json:"components"`
	Updates []struct {
		ID        string `json:"id"`
		Text      string `json:"text"`
		Status    string `json:"status"`
		CreatedAt string `json:"createdAt"`
	} `json:"updates"`
}

func definitionInstatus() engineDefinition {
	return engineDefinition{name: EngineInstatus, version: "v3", confidence: .98, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{
			{resource: domain.ResourceSummary, path: "/v3/summary.json", required: true, completeness: domain.CompletenessComplete,
				authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus}, decode: decodeInstatusSummary},
			{resource: domain.ResourceComponents, path: "/v3/components.json", completeness: domain.CompletenessComplete,
				authoritativeFor: []domain.ResourceKind{domain.ResourceComponents}, decode: decodeInstatusComponents},
		}, nil
	}}
}

func decodeInstatusSummary(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields["page"] == nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Instatus summary requires page", ErrInvalid)
	}
	var payload instatusSummary
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Instatus summary: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(payload.Page.Name) == "" || strings.TrimSpace(payload.Page.Status) == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: Instatus page name and status are required", ErrInvalid)
	}
	incidents := make([]domain.Incident, 0, len(payload.Incidents)+len(payload.Maintenances))
	for _, raw := range payload.Incidents {
		incident, err := normalizeInstatusIncident(raw, domain.IncidentKindIncident)
		if err != nil {
			return domain.Snapshot{}, err
		}
		incidents = append(incidents, incident)
	}
	for _, raw := range payload.Maintenances {
		incident, err := normalizeInstatusIncident(raw, domain.IncidentKindMaintenance)
		if err != nil {
			return domain.Snapshot{}, err
		}
		incidents = append(incidents, incident)
	}
	source = sourceForEngine(source, EngineInstatus)
	if source.CanonicalURL == nil && validateAbsoluteHTTP(payload.Page.URL) != "" {
		source.CanonicalURL = incidentIOURL(payload.Page.URL)
	}
	overall := normalizeComponentStatus(payload.Page.Status)
	authoritative := []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus}
	if _, present := fields["incidents"]; present {
		authoritative = append(authoritative, domain.ResourceUnresolvedIncidents)
	}
	if _, present := fields["maintenances"]; present {
		authoritative = append(authoritative, domain.ResourceScheduledMaintenances)
	}
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall, RawOverallStatus: payload.Page.Status,
		ComputedStatus: overall, Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: authoritative,
		ObservedAt:       observedAt, SchemaVersion: "v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func decodeInstatusComponents(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields["components"] == nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Instatus components are required", ErrInvalid)
	}
	var payload instatusComponents
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Instatus components: %v", ErrInvalid, err)
	}
	components := make([]domain.Component, 0, len(payload.Components))
	for index, raw := range payload.Components {
		if strings.TrimSpace(raw.ID) == "" || strings.TrimSpace(raw.Name) == "" || strings.TrimSpace(raw.Status) == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Instatus component[%d] requires id, name and status", ErrInvalid, index)
		}
		groupID := ""
		if raw.Group != nil {
			groupID = raw.Group.ID
		}
		components = append(components, domain.Component{ID: raw.ID, UpstreamID: raw.ID, Name: raw.Name,
			Description: raw.Description, GroupID: groupID, Status: normalizeComponentStatus(raw.Status), RawStatus: raw.Status})
	}
	source = sourceForEngine(source, EngineInstatus)
	computed := computedStatus(components)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: computed, ComputedStatus: computed,
		Components: components, Completeness: domain.CompletenessComplete, AuthoritativeFor: []domain.ResourceKind{domain.ResourceComponents},
		ObservedAt: observedAt, SchemaVersion: "v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func normalizeInstatusIncident(raw instatusIncident, kind domain.IncidentKind) (domain.Incident, error) {
	name := strings.TrimSpace(raw.Name)
	if name == "" {
		name = strings.TrimSpace(raw.Title)
	}
	if strings.TrimSpace(raw.ID) == "" || name == "" {
		return domain.Incident{}, fmt.Errorf("%w: Instatus incident id and name are required", ErrInvalid)
	}
	componentIDs := make([]string, 0, len(raw.Components))
	for _, component := range raw.Components {
		componentIDs = append(componentIDs, component.ID)
	}
	updates := make([]domain.IncidentUpdate, 0, len(raw.Updates))
	for index, update := range raw.Updates {
		id := update.ID
		if id == "" {
			id = stableID("instatus-update", raw.ID, fmt.Sprint(index), update.Text, update.CreatedAt)
		}
		updates = append(updates, domain.IncidentUpdate{ID: id, UpstreamID: update.ID, IncidentID: raw.ID,
			Body: update.Text, Phase: normalizePhase(update.Status, false), RawPhase: update.Status,
			Impact: normalizeImpact(raw.Impact), SourceCreatedAt: parseTime(update.CreatedAt)})
	}
	started := parseTime(raw.Started)
	if started == nil {
		started = parseTime(raw.CreatedAt)
	}
	return domain.Incident{ID: raw.ID, UpstreamID: raw.ID, Kind: kind, Name: name,
		URL: validateAbsoluteHTTP(raw.URL), Phase: normalizePhase(raw.Status, raw.ResolvedAt != ""), RawPhase: raw.Status,
		Impact: normalizeImpact(raw.Impact), RawImpact: raw.Impact, ComponentIDs: dedupeStrings(componentIDs), Updates: updates,
		StartedAt: started, ScheduledFor: parseTime(raw.ScheduledAt), ResolvedAt: parseTime(raw.ResolvedAt),
		SourceCreatedAt: parseTime(raw.CreatedAt), SourceUpdatedAt: parseTime(raw.UpdatedAt)}, nil
}
