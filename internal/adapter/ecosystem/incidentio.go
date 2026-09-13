package ecosystem

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const EngineIncidentIO = "incident-io-widget"

type incidentIOPayload struct {
	OngoingIncidents       []incidentIOEvent `json:"ongoing_incidents"`
	InProgressMaintenances []incidentIOEvent `json:"in_progress_maintenances"`
	ScheduledMaintenances  []incidentIOEvent `json:"scheduled_maintenances"`
}

type incidentIOEvent struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Status             string                `json:"status"`
	Impact             string                `json:"impact"`
	URL                string                `json:"url"`
	StartedAt          string                `json:"started_at"`
	CreatedAt          string                `json:"created_at"`
	UpdatedAt          string                `json:"updated_at"`
	ResolvedAt         string                `json:"resolved_at"`
	StartAt            string                `json:"start_at"`
	EndAt              string                `json:"end_at"`
	ScheduledStartAt   string                `json:"scheduled_start_at"`
	ScheduledEndAt     string                `json:"scheduled_end_at"`
	LastUpdate         *incidentIOUpdate     `json:"last_update"`
	Updates            []incidentIOUpdate    `json:"updates"`
	AffectedComponents []incidentIOComponent `json:"affected_components"`
	Components         []incidentIOComponent `json:"components"`
}

type incidentIOUpdate struct {
	ID        string `json:"id"`
	Message   string `json:"message"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type incidentIOComponent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func definitionIncidentIO() engineDefinition {
	return engineDefinition{name: EngineIncidentIO, version: "widget-v1", confidence: 1, endpoints: func(target domain.Target) ([]endpointDefinition, error) {
		if target.URL == nil {
			return nil, fmt.Errorf("%w: incident.io Widget API URL is required", ErrInvalid)
		}
		path := target.URL.EscapedPath()
		if path == "" {
			path = "/"
		}
		if target.URL.RawQuery != "" {
			path += "?" + target.URL.RawQuery
		}
		return []endpointDefinition{{resource: domain.ResourceSummary, path: path, required: true,
			completeness:     domain.CompletenessComplete,
			authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
			decode:           decodeIncidentIO}}, nil
	}}
}

func decodeIncidentIO(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(body, &keys); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: incident.io: %v", ErrInvalid, err)
	}
	for _, key := range []string{"ongoing_incidents", "in_progress_maintenances", "scheduled_maintenances"} {
		if _, found := keys[key]; !found {
			return domain.Snapshot{}, fmt.Errorf("%w: incident.io: %s is required", ErrInvalid, key)
		}
	}
	var payload incidentIOPayload
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: incident.io: %v", ErrInvalid, err)
	}
	incidents := make([]domain.Incident, 0, len(payload.OngoingIncidents)+len(payload.InProgressMaintenances)+len(payload.ScheduledMaintenances))
	for index, event := range payload.OngoingIncidents {
		incident, err := normalizeIncidentIOEvent(event, domain.IncidentKindIncident, false)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: incident.io incident[%d]: %v", ErrInvalid, index, err)
		}
		incidents = append(incidents, incident)
	}
	for index, event := range payload.InProgressMaintenances {
		incident, err := normalizeIncidentIOEvent(event, domain.IncidentKindMaintenance, false)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: incident.io active maintenance[%d]: %v", ErrInvalid, index, err)
		}
		incidents = append(incidents, incident)
	}
	for index, event := range payload.ScheduledMaintenances {
		incident, err := normalizeIncidentIOEvent(event, domain.IncidentKindMaintenance, true)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: incident.io scheduled maintenance[%d]: %v", ErrInvalid, index, err)
		}
		incidents = append(incidents, incident)
	}
	overall := domain.ComponentStatusOperational
	if len(payload.OngoingIncidents) > 0 {
		overall = domain.ComponentStatusDegraded
	} else if len(payload.InProgressMaintenances) > 0 {
		overall = domain.ComponentStatusUnderMaintenance
	}
	source = sourceForEngine(source, EngineIncidentIO)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall, ComputedStatus: overall,
		Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
		ObservedAt:       observedAt, SchemaVersion: "widget-v1", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func normalizeIncidentIOEvent(raw incidentIOEvent, kind domain.IncidentKind, scheduled bool) (domain.Incident, error) {
	if strings.TrimSpace(raw.ID) == "" || strings.TrimSpace(raw.Name) == "" {
		return domain.Incident{}, fmt.Errorf("id and name are required")
	}
	phase := normalizePhase(raw.Status, false)
	if scheduled && phase == domain.IncidentPhaseUnknown {
		phase = domain.IncidentPhaseIdentified
	}
	components := append(append([]incidentIOComponent(nil), raw.AffectedComponents...), raw.Components...)
	componentIDs := make([]string, 0, len(components))
	for _, component := range components {
		id := strings.TrimSpace(component.ID)
		if id == "" {
			id = stableID("incidentio-component", component.Name)
		}
		componentIDs = append(componentIDs, id)
	}
	updates := append([]incidentIOUpdate(nil), raw.Updates...)
	if raw.LastUpdate != nil {
		updates = append(updates, *raw.LastUpdate)
	}
	normalizedUpdates := make([]domain.IncidentUpdate, 0, len(updates))
	for index, update := range updates {
		id := strings.TrimSpace(update.ID)
		if id == "" {
			id = stableID("incidentio-update", raw.ID, fmt.Sprint(index), update.Message, update.Body, update.CreatedAt)
		}
		body := strings.TrimSpace(update.Message)
		if body == "" {
			body = strings.TrimSpace(update.Body)
		}
		normalizedUpdates = append(normalizedUpdates, domain.IncidentUpdate{ID: id, UpstreamID: update.ID,
			IncidentID: raw.ID, Body: body, Phase: normalizePhase(update.Status, false), RawPhase: update.Status,
			Impact: normalizeImpact(raw.Impact), SourceCreatedAt: parseTime(update.CreatedAt), SourceUpdatedAt: parseTime(update.UpdatedAt)})
	}
	startedAt := parseTime(raw.StartedAt)
	if startedAt == nil {
		startedAt = parseTime(raw.CreatedAt)
	}
	scheduledFor := parseTime(raw.ScheduledStartAt)
	if scheduledFor == nil {
		scheduledFor = parseTime(raw.StartAt)
	}
	scheduledUntil := parseTime(raw.ScheduledEndAt)
	if scheduledUntil == nil {
		scheduledUntil = parseTime(raw.EndAt)
	}
	return domain.Incident{ID: raw.ID, UpstreamID: raw.ID, Kind: kind, Name: raw.Name,
		URL: validateAbsoluteHTTP(raw.URL), Phase: phase, RawPhase: raw.Status,
		Impact: normalizeImpact(raw.Impact), RawImpact: raw.Impact, ComponentIDs: dedupeStrings(componentIDs),
		Updates: normalizedUpdates, StartedAt: startedAt, ScheduledFor: scheduledFor, ScheduledUntil: scheduledUntil,
		ResolvedAt: parseTime(raw.ResolvedAt), SourceCreatedAt: parseTime(raw.CreatedAt), SourceUpdatedAt: parseTime(raw.UpdatedAt)}, nil
}

func incidentIOURL(value string) *url.URL {
	parsed, _ := url.Parse(validateAbsoluteHTTP(value))
	return parsed
}
