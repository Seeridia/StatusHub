package ecosystem

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const EngineCachet = "cachet-v2-v3"

type cachetEnvelope struct {
	Meta struct {
		Pagination struct {
			CurrentPage int `json:"current_page"`
			TotalPages  int `json:"total_pages"`
		} `json:"pagination"`
	} `json:"meta"`
	Data json.RawMessage `json:"data"`
}

type cachetStatus struct {
	Status  any    `json:"status"`
	Message string `json:"message"`
}

type cachetComponentAttributes struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Status      any    `json:"status"`
	StatusName  string `json:"status_name"`
	GroupID     any    `json:"group_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type cachetComponentRecord struct {
	ID   any    `json:"id"`
	Type string `json:"type"`
	cachetComponentAttributes
	Attributes cachetComponentAttributes `json:"attributes"`
}

type cachetIncidentAttributes struct {
	Name         string               `json:"name"`
	Message      string               `json:"message"`
	Status       any                  `json:"status"`
	HumanStatus  string               `json:"human_status"`
	LatestStatus any                  `json:"latest_status"`
	IsResolved   bool                 `json:"is_resolved"`
	ComponentID  any                  `json:"component_id"`
	OccurredAt   string               `json:"occurred_at"`
	CreatedAt    string               `json:"created_at"`
	UpdatedAt    string               `json:"updated_at"`
	Permalink    string               `json:"permalink"`
	Updates      []cachetUpdateRecord `json:"updates"`
}

type cachetIncidentRecord struct {
	ID   any    `json:"id"`
	Type string `json:"type"`
	cachetIncidentAttributes
	Attributes cachetIncidentAttributes `json:"attributes"`
}

type cachetUpdateRecord struct {
	ID          any    `json:"id"`
	Message     string `json:"message"`
	Status      any    `json:"status"`
	HumanStatus string `json:"human_status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type cachetScheduleAttributes struct {
	Name        string `json:"name"`
	Message     string `json:"message"`
	Status      any    `json:"status"`
	HumanStatus string `json:"human_status"`
	ScheduledAt string `json:"scheduled_at"`
	CompletedAt string `json:"completed_at"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	Components  []struct {
		ID any `json:"id"`
	} `json:"components"`
}

type cachetScheduleRecord struct {
	ID   any    `json:"id"`
	Type string `json:"type"`
	cachetScheduleAttributes
	Attributes cachetScheduleAttributes `json:"attributes"`
}

func definitionCachet() engineDefinition {
	return engineDefinition{name: EngineCachet, version: "v2-v3", confidence: .96, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{
			{resource: domain.ResourceStatus, path: "/api/status", completeness: domain.CompletenessComplete,
				authoritativeFor: []domain.ResourceKind{domain.ResourceStatus}, decode: decodeCachetStatus},
			{resource: domain.ResourceComponents, path: "/api/components", completeness: domain.CompletenessPartial, decode: decodeCachetComponents},
			{resource: domain.ResourceIncidents, path: "/api/incidents", completeness: domain.CompletenessPartial, decode: decodeCachetIncidents},
			{resource: domain.ResourceScheduledMaintenances, path: "/api/schedules", completeness: domain.CompletenessPartial, decode: decodeCachetSchedules},
			{resource: domain.ResourceSummary, path: "/api/v1/status", completeness: domain.CompletenessComplete,
				authoritativeFor: []domain.ResourceKind{domain.ResourceStatus}, decode: decodeCachetStatus},
			{resource: domain.ResourceComponents, path: "/api/v1/components?per_page=100", completeness: domain.CompletenessPartial, decode: decodeCachetComponents},
			{resource: domain.ResourceIncidents, path: "/api/v1/incidents?per_page=100", completeness: domain.CompletenessPartial, decode: decodeCachetIncidents},
			{resource: domain.ResourceScheduledMaintenances, path: "/api/v1/schedules?per_page=100", completeness: domain.CompletenessPartial, decode: decodeCachetSchedules},
		}, nil
	}}
}

func decodeCachetStatus(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var envelope cachetEnvelope
	if err := decodeStrict(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet status data is required", ErrInvalid)
	}
	var status cachetStatus
	if err := json.Unmarshal(envelope.Data, &status); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet status: %v", ErrInvalid, err)
	}
	// Cachet v3 JSON:API wraps fields in attributes.
	if status.Status == nil {
		var wrapped struct {
			Attributes cachetStatus `json:"attributes"`
		}
		if err := json.Unmarshal(envelope.Data, &wrapped); err == nil {
			status = wrapped.Attributes
		}
	}
	if status.Status == nil && status.Message == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet status value is required", ErrInvalid)
	}
	raw := scalarString(status.Status)
	if raw == "" {
		raw = status.Message
	}
	normalized := normalizeComponentStatus(raw)
	if normalized == domain.ComponentStatusUnknown {
		normalized = normalizeComponentStatus(status.Message)
	}
	source = sourceForEngine(source, EngineCachet)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: normalized, RawOverallStatus: raw,
		ComputedStatus: normalized, Completeness: domain.CompletenessComplete, AuthoritativeFor: []domain.ResourceKind{domain.ResourceStatus},
		ObservedAt: observedAt, SchemaVersion: "v2-v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func decodeCachetComponents(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var envelope cachetEnvelope
	if err := decodeStrict(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet components data is required", ErrInvalid)
	}
	var records []cachetComponentRecord
	if err := json.Unmarshal(envelope.Data, &records); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet components: %v", ErrInvalid, err)
	}
	components := make([]domain.Component, 0, len(records))
	for index, record := range records {
		attributes := record.cachetComponentAttributes
		if record.Attributes.Name != "" || record.Attributes.Status != nil {
			attributes = record.Attributes
		}
		id := scalarString(record.ID)
		rawStatus := scalarString(attributes.Status)
		if rawStatus == "" {
			rawStatus = attributes.StatusName
		}
		if id == "" || strings.TrimSpace(attributes.Name) == "" || rawStatus == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Cachet component[%d] requires id, name and status", ErrInvalid, index)
		}
		components = append(components, domain.Component{ID: id, UpstreamID: id, Name: attributes.Name,
			Description: attributes.Description, GroupID: scalarString(attributes.GroupID),
			Status: normalizeComponentStatus(rawStatus), RawStatus: rawStatus,
			SourceCreatedAt: parseTime(attributes.CreatedAt), SourceUpdatedAt: parseTime(attributes.UpdatedAt)})
	}
	complete := cachetCompleteness(envelope)
	authoritative := []domain.ResourceKind(nil)
	if complete == domain.CompletenessComplete {
		authoritative = []domain.ResourceKind{domain.ResourceComponents}
	}
	source = sourceForEngine(source, EngineCachet)
	status := computedStatus(components)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: status, ComputedStatus: status,
		Components: components, Completeness: complete, AuthoritativeFor: authoritative, ObservedAt: observedAt,
		SchemaVersion: "v2-v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func decodeCachetIncidents(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var envelope cachetEnvelope
	if err := decodeStrict(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet incidents data is required", ErrInvalid)
	}
	var records []cachetIncidentRecord
	if err := json.Unmarshal(envelope.Data, &records); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet incidents: %v", ErrInvalid, err)
	}
	incidents := make([]domain.Incident, 0, len(records))
	for index, record := range records {
		attributes := record.cachetIncidentAttributes
		if record.Attributes.Name != "" || record.Attributes.Status != nil {
			attributes = record.Attributes
		}
		id := scalarString(record.ID)
		if id == "" || strings.TrimSpace(attributes.Name) == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Cachet incident[%d] requires id and name", ErrInvalid, index)
		}
		rawPhase := scalarString(attributes.LatestStatus)
		if rawPhase == "" {
			rawPhase = attributes.HumanStatus
		}
		if rawPhase == "" {
			rawPhase = scalarString(attributes.Status)
		}
		componentIDs := []string{scalarString(attributes.ComponentID)}
		updates := make([]domain.IncidentUpdate, 0, len(attributes.Updates)+1)
		if strings.TrimSpace(attributes.Message) != "" {
			updates = append(updates, domain.IncidentUpdate{ID: stableID("cachet-initial", id, attributes.Message),
				IncidentID: id, Body: attributes.Message, Phase: normalizePhase(attributes.HumanStatus, attributes.IsResolved),
				RawPhase: attributes.HumanStatus, Impact: cachetImpact(attributes.Status), SourceCreatedAt: parseTime(attributes.OccurredAt)})
		}
		for updateIndex, raw := range attributes.Updates {
			updateID := scalarString(raw.ID)
			if updateID == "" {
				updateID = stableID("cachet-update", id, strconv.Itoa(updateIndex), raw.Message, raw.CreatedAt)
			}
			updates = append(updates, domain.IncidentUpdate{ID: updateID, UpstreamID: scalarString(raw.ID), IncidentID: id,
				Body: raw.Message, Phase: normalizePhase(firstNonEmpty(raw.HumanStatus, scalarString(raw.Status)), false),
				RawPhase: firstNonEmpty(raw.HumanStatus, scalarString(raw.Status)), Impact: cachetImpact(raw.Status),
				SourceCreatedAt: parseTime(raw.CreatedAt), SourceUpdatedAt: parseTime(raw.UpdatedAt)})
		}
		incidents = append(incidents, domain.Incident{ID: id, UpstreamID: id, Kind: domain.IncidentKindIncident,
			Name: attributes.Name, URL: validateAbsoluteHTTP(attributes.Permalink), Phase: normalizePhase(rawPhase, attributes.IsResolved),
			RawPhase: rawPhase, Impact: cachetImpact(attributes.Status), RawImpact: scalarString(attributes.Status),
			ComponentIDs: dedupeStrings(componentIDs), Updates: updates, StartedAt: parseTime(attributes.OccurredAt),
			SourceCreatedAt: parseTime(attributes.CreatedAt), SourceUpdatedAt: parseTime(attributes.UpdatedAt)})
	}
	complete := cachetCompleteness(envelope)
	source = sourceForEngine(source, EngineCachet)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: domain.ComponentStatusUnknown,
		ComputedStatus: domain.ComponentStatusUnknown, Incidents: incidents, Completeness: complete,
		ObservedAt: observedAt, SchemaVersion: "v2-v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func decodeCachetSchedules(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var envelope cachetEnvelope
	if err := decodeStrict(body, &envelope); err != nil || len(envelope.Data) == 0 {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet schedules data is required", ErrInvalid)
	}
	var records []cachetScheduleRecord
	if err := json.Unmarshal(envelope.Data, &records); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Cachet schedules: %v", ErrInvalid, err)
	}
	incidents := make([]domain.Incident, 0, len(records))
	for index, record := range records {
		attributes := record.cachetScheduleAttributes
		if record.Attributes.Name != "" || record.Attributes.Status != nil {
			attributes = record.Attributes
		}
		id := scalarString(record.ID)
		if id == "" || strings.TrimSpace(attributes.Name) == "" || attributes.ScheduledAt == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Cachet schedule[%d] requires id, name and scheduled_at", ErrInvalid, index)
		}
		componentIDs := make([]string, 0, len(attributes.Components))
		for _, component := range attributes.Components {
			componentIDs = append(componentIDs, scalarString(component.ID))
		}
		rawPhase := firstNonEmpty(attributes.HumanStatus, scalarString(attributes.Status))
		completed := attributes.CompletedAt != ""
		incident := domain.Incident{ID: id, UpstreamID: id, Kind: domain.IncidentKindMaintenance,
			Name: attributes.Name, Phase: normalizePhase(rawPhase, completed), RawPhase: rawPhase,
			Impact: domain.ImpactNone, RawImpact: scalarString(attributes.Status), ComponentIDs: dedupeStrings(componentIDs),
			ScheduledFor: parseTime(attributes.ScheduledAt), ResolvedAt: parseTime(attributes.CompletedAt),
			SourceCreatedAt: parseTime(attributes.CreatedAt), SourceUpdatedAt: parseTime(attributes.UpdatedAt)}
		if strings.TrimSpace(attributes.Message) != "" {
			incident.Updates = []domain.IncidentUpdate{{ID: stableID("cachet-schedule-update", id, attributes.Message),
				IncidentID: id, Body: attributes.Message, Phase: incident.Phase, RawPhase: rawPhase, Impact: domain.ImpactNone,
				SourceCreatedAt: incident.SourceCreatedAt}}
		}
		incidents = append(incidents, incident)
	}
	complete := cachetCompleteness(envelope)
	authoritative := []domain.ResourceKind(nil)
	if complete == domain.CompletenessComplete {
		authoritative = []domain.ResourceKind{domain.ResourceScheduledMaintenances}
	}
	source = sourceForEngine(source, EngineCachet)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: domain.ComponentStatusUnknown,
		ComputedStatus: domain.ComponentStatusUnknown, Incidents: incidents, Completeness: complete, AuthoritativeFor: authoritative,
		ObservedAt: observedAt, SchemaVersion: "v2-v3", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func cachetCompleteness(envelope cachetEnvelope) domain.Completeness {
	pages := envelope.Meta.Pagination.TotalPages
	if pages == 1 {
		return domain.CompletenessComplete
	}
	return domain.CompletenessPartial
}

func cachetImpact(value any) domain.Impact {
	switch scalarString(value) {
	case "1":
		return domain.ImpactMinor
	case "2":
		return domain.ImpactMajor
	case "3", "4":
		return domain.ImpactCritical
	default:
		return domain.ImpactUnknown
	}
}

func scalarString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		data, _ := json.Marshal(typed)
		return strings.Trim(string(data), `"`)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
