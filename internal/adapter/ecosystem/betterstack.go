package ecosystem

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const EngineBetterStack = "better-stack-status-page"

type betterStackPayload struct {
	Data struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Attributes struct {
			CompanyName    string `json:"company_name"`
			AggregateState string `json:"aggregate_state"`
			UpdatedAt      string `json:"updated_at"`
		} `json:"attributes"`
	} `json:"data"`
	Included []betterStackIncluded `json:"included"`
}

type betterStackIncluded struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		Name                string      `json:"name"`
		PublicName          string      `json:"public_name"`
		Explanation         string      `json:"explanation"`
		Status              string      `json:"status"`
		StatusPageSectionID json.Number `json:"status_page_section_id"`
		Title               string      `json:"title"`
		ReportType          string      `json:"report_type"`
		AggregateState      string      `json:"aggregate_state"`
		StartsAt            string      `json:"starts_at"`
		EndsAt              string      `json:"ends_at"`
		Message             string      `json:"message"`
		PublishedAt         string      `json:"published_at"`
		AffectedResources   []struct {
			StatusPageResourceID string `json:"status_page_resource_id"`
			Status               string `json:"status"`
		} `json:"affected_resources"`
	} `json:"attributes"`
	Relationships struct {
		StatusUpdates struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"status_updates"`
	} `json:"relationships"`
}

func definitionBetterStack() engineDefinition {
	return engineDefinition{name: EngineBetterStack, version: "index-v1", confidence: 1, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{{resource: domain.ResourceSummary, path: "/index.json", required: true,
			completeness:     domain.CompletenessComplete,
			authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
			decode:           decodeBetterStack}}, nil
	}}
}

func decodeBetterStack(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields["data"] == nil || fields["included"] == nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Better Stack data and included are required", ErrInvalid)
	}
	var payload betterStackPayload
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Better Stack: %v", ErrInvalid, err)
	}
	if payload.Data.Type != "status_page" || payload.Data.ID == "" || payload.Data.Attributes.AggregateState == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: Better Stack status_page identity and aggregate_state are required", ErrInvalid)
	}
	updates := make(map[string]betterStackIncluded)
	components := make([]domain.Component, 0)
	for _, included := range payload.Included {
		switch included.Type {
		case "status_update":
			updates[included.ID] = included
		case "status_page_resource":
			if included.ID == "" || strings.TrimSpace(included.Attributes.PublicName) == "" || included.Attributes.Status == "" {
				return domain.Snapshot{}, fmt.Errorf("%w: Better Stack resource identity, name and status are required", ErrInvalid)
			}
			components = append(components, domain.Component{ID: included.ID, UpstreamID: included.ID,
				Name: included.Attributes.PublicName, Description: included.Attributes.Explanation,
				GroupID: included.Attributes.StatusPageSectionID.String(), Status: normalizeComponentStatus(included.Attributes.Status),
				RawStatus: included.Attributes.Status})
		}
	}
	incidents := make([]domain.Incident, 0)
	for _, included := range payload.Included {
		if included.Type != "status_report" {
			continue
		}
		if included.ID == "" || strings.TrimSpace(included.Attributes.Title) == "" || included.Attributes.AggregateState == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Better Stack report identity, title and state are required", ErrInvalid)
		}
		kind := domain.IncidentKindIncident
		if strings.Contains(strings.ToLower(included.Attributes.ReportType), "maintenance") {
			kind = domain.IncidentKindMaintenance
		}
		componentIDs := make([]string, 0, len(included.Attributes.AffectedResources))
		for _, affected := range included.Attributes.AffectedResources {
			componentIDs = append(componentIDs, affected.StatusPageResourceID)
		}
		normalizedUpdates := make([]domain.IncidentUpdate, 0, len(included.Relationships.StatusUpdates.Data))
		for _, reference := range included.Relationships.StatusUpdates.Data {
			update, found := updates[reference.ID]
			if !found {
				continue
			}
			normalizedUpdates = append(normalizedUpdates, domain.IncidentUpdate{ID: update.ID, UpstreamID: update.ID,
				IncidentID: included.ID, Body: update.Attributes.Message,
				Phase:    normalizePhase(included.Attributes.AggregateState, included.Attributes.EndsAt != ""),
				RawPhase: included.Attributes.AggregateState, Impact: normalizeImpact(included.Attributes.AggregateState),
				SourceCreatedAt: parseTime(update.Attributes.PublishedAt)})
		}
		phase := normalizePhase(included.Attributes.AggregateState, included.Attributes.EndsAt != "")
		incident := domain.Incident{ID: included.ID, UpstreamID: included.ID, Kind: kind, Name: included.Attributes.Title,
			Phase: phase, RawPhase: included.Attributes.AggregateState,
			Impact: normalizeImpact(included.Attributes.AggregateState), RawImpact: included.Attributes.AggregateState,
			ComponentIDs: dedupeStrings(componentIDs), Updates: normalizedUpdates, StartedAt: parseTime(included.Attributes.StartsAt),
			ResolvedAt: parseTime(included.Attributes.EndsAt), SourceCreatedAt: parseTime(included.Attributes.StartsAt),
			SourceUpdatedAt: latestBetterStackUpdate(normalizedUpdates)}
		if kind == domain.IncidentKindMaintenance {
			incident.ScheduledFor = incident.StartedAt
			incident.ScheduledUntil = parseTime(included.Attributes.EndsAt)
		}
		incidents = append(incidents, incident)
	}
	source = sourceForEngine(source, EngineBetterStack)
	source.PageID = payload.Data.ID
	overall := normalizeComponentStatus(payload.Data.Attributes.AggregateState)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall,
		RawOverallStatus: payload.Data.Attributes.AggregateState, ComputedStatus: computedStatus(components), Components: components,
		Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
		SourceUpdatedAt:  parseTime(payload.Data.Attributes.UpdatedAt), ObservedAt: observedAt,
		SchemaVersion: "index-v1", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func latestBetterStackUpdate(updates []domain.IncidentUpdate) *time.Time {
	var latest *time.Time
	for _, update := range updates {
		candidate := update.SourceUpdatedAt
		if candidate == nil {
			candidate = update.SourceCreatedAt
		}
		if candidate != nil && (latest == nil || candidate.After(*latest)) {
			value := *candidate
			latest = &value
		}
	}
	return latest
}
