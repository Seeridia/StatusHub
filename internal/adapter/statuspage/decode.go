package statuspage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const (
	Engine            = "atlassian-statuspage"
	Version           = "v2"
	AdapterVersion    = "statuspage-v2/1"
	NormalizerVersion = "statuspage-v2/1"
)

var (
	ErrInvalidPayload = errors.New("invalid Atlassian Statuspage v2 payload")
	ErrNotStatuspage  = errors.New("target does not expose Atlassian Statuspage v2 public JSON")
	ErrUnsupported    = errors.New("operation is not supported by the Statuspage public read adapter")
)

// DecodeSummary is a pure decoder for /api/v2/summary.json. The endpoint is a
// complete view of current components and unresolved incidents, not incident
// history. That distinction is preserved in AuthoritativeFor.
func DecodeSummary(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
	if err := requireObjectFields(body, "page", "status", "components"); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: summary: %v", ErrInvalidPayload, err)
	}

	var payload summaryPayload
	if err := decodeJSON(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: summary: %v", ErrInvalidPayload, err)
	}
	if err := validatePage(payload.Page, source); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: summary: %v", ErrInvalidPayload, err)
	}
	if payload.Status == nil || strings.TrimSpace(payload.Status.Indicator) == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: summary: status.indicator is required", ErrInvalidPayload)
	}

	components := make([]domain.Component, 0, len(payload.Components))
	for index, raw := range payload.Components {
		component, err := normalizeComponent(raw)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: summary: components[%d]: %v", ErrInvalidPayload, index, err)
		}
		components = append(components, component)
	}

	incidents := make([]domain.Incident, 0, len(payload.Incidents))
	for index, raw := range payload.Incidents {
		incident, err := normalizeIncident(raw)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: summary: incidents[%d]: %v", ErrInvalidPayload, index, err)
		}
		incidents = append(incidents, incident)
	}

	var topLevel map[string]json.RawMessage
	_ = json.Unmarshal(body, &topLevel)
	authoritativeFor := []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents}
	if incidentsValue, present := topLevel["incidents"]; present && string(incidentsValue) != "null" {
		authoritativeFor = append(authoritativeFor, domain.ResourceUnresolvedIncidents)
	}
	source = normalizeSource(source, payload.Page)
	sourceUpdatedAt := timePointer(payload.Page.UpdatedAt)
	return domain.Snapshot{
		Source:            source,
		ResourceKind:      domain.ResourceSummary,
		OverallStatus:     normalizeOverallStatus(payload.Status.Indicator),
		RawOverallStatus:  payload.Status.Indicator,
		ComputedStatus:    computeComponentStatus(components),
		Components:        components,
		Incidents:         incidents,
		Completeness:      domain.CompletenessComplete,
		AuthoritativeFor:  authoritativeFor,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		SchemaVersion:     Version,
		AdapterVersion:    AdapterVersion,
		NormalizerVersion: NormalizerVersion,
	}, nil
}

// DecodeIncidents is a pure decoder for /api/v2/incidents.json. Atlassian
// returns a bounded recent-history window, so absence is not authoritative.
func DecodeIncidents(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
	return decodeIncidentCollection(body, source, observedAt, domain.ResourceIncidents)
}

// DecodeUnresolvedIncidents decodes /api/v2/incidents/unresolved.json. This
// collection is complete and authoritative only for unresolved incidents.
func DecodeUnresolvedIncidents(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
	return decodeIncidentCollection(body, source, observedAt, domain.ResourceUnresolvedIncidents)
}

func decodeIncidentCollection(body []byte, source domain.Source, observedAt time.Time, kind domain.ResourceKind) (domain.Snapshot, error) {
	if err := requireObjectFields(body, "page", "incidents"); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: incidents: %v", ErrInvalidPayload, err)
	}

	var payload incidentsPayload
	if err := decodeJSON(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: incidents: %v", ErrInvalidPayload, err)
	}
	if err := validatePage(payload.Page, source); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: incidents: %v", ErrInvalidPayload, err)
	}

	incidents := make([]domain.Incident, 0, len(payload.Incidents))
	for index, raw := range payload.Incidents {
		incident, err := normalizeIncident(raw)
		if err != nil {
			return domain.Snapshot{}, fmt.Errorf("%w: incidents: incidents[%d]: %v", ErrInvalidPayload, index, err)
		}
		incidents = append(incidents, incident)
	}

	completeness := domain.CompletenessPartial
	var authoritativeFor []domain.ResourceKind
	if kind == domain.ResourceUnresolvedIncidents {
		completeness = domain.CompletenessComplete
		authoritativeFor = []domain.ResourceKind{domain.ResourceUnresolvedIncidents}
	}

	source = normalizeSource(source, payload.Page)
	return domain.Snapshot{
		Source:            source,
		ResourceKind:      kind,
		OverallStatus:     domain.ComponentStatusUnknown,
		ComputedStatus:    domain.ComponentStatusUnknown,
		Incidents:         incidents,
		Completeness:      completeness,
		AuthoritativeFor:  authoritativeFor,
		SourceUpdatedAt:   timePointer(payload.Page.UpdatedAt),
		ObservedAt:        observedAt,
		SchemaVersion:     Version,
		AdapterVersion:    AdapterVersion,
		NormalizerVersion: NormalizerVersion,
	}, nil
}

func normalizeSource(source domain.Source, page *pagePayload) domain.Source {
	if source.Provider == "" {
		source.Provider = Engine
	}
	if source.Kind == "" {
		source.Kind = domain.SourceKindStatusPage
	}
	if source.PageID == "" {
		source.PageID = page.ID
	}
	if source.CanonicalURL == nil {
		parsed, _ := url.Parse(page.URL)
		source.CanonicalURL = parsed
	}
	return source
}

func validatePage(page *pagePayload, source domain.Source) error {
	if page == nil {
		return errors.New("page is required")
	}
	if strings.TrimSpace(page.ID) == "" {
		return errors.New("page.id is required")
	}
	if source.PageID != "" && source.PageID != page.ID {
		return fmt.Errorf("page.id %q does not match expected page %q", page.ID, source.PageID)
	}
	if strings.TrimSpace(page.Name) == "" {
		return errors.New("page.name is required")
	}
	parsed, err := url.Parse(page.URL)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("page.url must be an absolute HTTP URL without user information")
	}
	return nil
}

func normalizeComponent(raw componentPayload) (domain.Component, error) {
	if strings.TrimSpace(raw.ID) == "" {
		return domain.Component{}, errors.New("id is required")
	}
	if strings.TrimSpace(raw.Name) == "" {
		return domain.Component{}, errors.New("name is required")
	}
	if strings.TrimSpace(raw.Status) == "" {
		return domain.Component{}, errors.New("status is required")
	}

	groupID := ""
	if raw.GroupID != nil {
		groupID = *raw.GroupID
	}
	return domain.Component{
		ID:              raw.ID,
		UpstreamID:      raw.ID,
		Name:            raw.Name,
		Description:     raw.Description,
		GroupID:         groupID,
		Status:          normalizeComponentStatus(raw.Status),
		RawStatus:       raw.Status,
		SourceCreatedAt: timePointer(raw.CreatedAt),
		SourceUpdatedAt: timePointer(raw.UpdatedAt),
	}, nil
}

func normalizeIncident(raw incidentPayload) (domain.Incident, error) {
	if strings.TrimSpace(raw.ID) == "" {
		return domain.Incident{}, errors.New("id is required")
	}
	if strings.TrimSpace(raw.Name) == "" {
		return domain.Incident{}, errors.New("name is required")
	}
	if strings.TrimSpace(raw.Status) == "" {
		return domain.Incident{}, errors.New("status is required")
	}
	if strings.TrimSpace(raw.Impact) == "" {
		return domain.Incident{}, errors.New("impact is required")
	}

	componentIDs := make([]string, 0, len(raw.Components))
	seenComponentIDs := make(map[string]struct{}, len(raw.Components))
	for _, component := range raw.Components {
		if component.ID != "" {
			componentIDs = appendUnique(componentIDs, seenComponentIDs, component.ID)
		}
	}

	updates := make([]domain.IncidentUpdate, 0, len(raw.IncidentUpdates))
	for index, update := range raw.IncidentUpdates {
		if strings.TrimSpace(update.ID) == "" {
			return domain.Incident{}, fmt.Errorf("incident_updates[%d].id is required", index)
		}
		if strings.TrimSpace(update.Status) == "" {
			return domain.Incident{}, fmt.Errorf("incident_updates[%d].status is required", index)
		}
		updateComponentIDs := make([]string, 0, len(update.AffectedComponents))
		seenUpdateComponentIDs := make(map[string]struct{}, len(update.AffectedComponents))
		for _, component := range update.AffectedComponents {
			if component.Code == "" {
				continue
			}
			updateComponentIDs = appendUnique(updateComponentIDs, seenUpdateComponentIDs, component.Code)
			componentIDs = appendUnique(componentIDs, seenComponentIDs, component.Code)
		}

		updates = append(updates, domain.IncidentUpdate{
			ID:              update.ID,
			UpstreamID:      update.ID,
			IncidentID:      raw.ID,
			Body:            update.Body,
			Phase:           normalizeIncidentPhase(update.Status),
			RawPhase:        update.Status,
			Impact:          normalizeImpact(raw.Impact),
			RawImpact:       raw.Impact,
			ComponentIDs:    updateComponentIDs,
			SourceCreatedAt: timePointer(update.CreatedAt),
			SourceUpdatedAt: timePointer(update.UpdatedAt),
		})
	}

	return domain.Incident{
		ID:              raw.ID,
		UpstreamID:      raw.ID,
		Kind:            domain.IncidentKindIncident,
		Name:            raw.Name,
		URL:             raw.Shortlink,
		Phase:           normalizeIncidentPhase(raw.Status),
		RawPhase:        raw.Status,
		Impact:          normalizeImpact(raw.Impact),
		RawImpact:       raw.Impact,
		ComponentIDs:    componentIDs,
		Updates:         updates,
		StartedAt:       timePointer(raw.StartedAt),
		ResolvedAt:      timePointer(raw.ResolvedAt),
		SourceCreatedAt: timePointer(raw.CreatedAt),
		SourceUpdatedAt: timePointer(raw.UpdatedAt),
	}, nil
}

func appendUnique(values []string, seen map[string]struct{}, value string) []string {
	if _, exists := seen[value]; exists {
		return values
	}
	seen[value] = struct{}{}
	return append(values, value)
}

func timePointer(raw timestamp) *time.Time {
	if !raw.Valid {
		return nil
	}
	value := raw.Time
	return &value
}

func normalizeOverallStatus(raw string) domain.ComponentStatus {
	switch raw {
	case "none":
		return domain.ComponentStatusOperational
	case "minor":
		return domain.ComponentStatusDegraded
	case "major":
		return domain.ComponentStatusPartialOutage
	case "critical":
		return domain.ComponentStatusMajorOutage
	default:
		return domain.ComponentStatusUnknown
	}
}

func normalizeComponentStatus(raw string) domain.ComponentStatus {
	switch raw {
	case "operational":
		return domain.ComponentStatusOperational
	case "degraded_performance", "degraded":
		return domain.ComponentStatusDegraded
	case "partial_outage":
		return domain.ComponentStatusPartialOutage
	case "major_outage":
		return domain.ComponentStatusMajorOutage
	case "under_maintenance":
		return domain.ComponentStatusUnderMaintenance
	default:
		return domain.ComponentStatusUnknown
	}
}

func normalizeIncidentPhase(raw string) domain.IncidentPhase {
	switch raw {
	case "investigating":
		return domain.IncidentPhaseInvestigating
	case "identified":
		return domain.IncidentPhaseIdentified
	case "monitoring":
		return domain.IncidentPhaseMonitoring
	case "resolved":
		return domain.IncidentPhaseResolved
	default:
		return domain.IncidentPhaseUnknown
	}
}

func normalizeImpact(raw string) domain.Impact {
	switch raw {
	case "none":
		return domain.ImpactNone
	case "minor":
		return domain.ImpactMinor
	case "major":
		return domain.ImpactMajor
	case "critical":
		return domain.ImpactCritical
	default:
		return domain.ImpactUnknown
	}
}

func computeComponentStatus(components []domain.Component) domain.ComponentStatus {
	if len(components) == 0 {
		return domain.ComponentStatusUnknown
	}

	status := domain.ComponentStatusOperational
	unknown := false
	for _, component := range components {
		if component.Status == domain.ComponentStatusUnknown {
			unknown = true
			continue
		}
		if componentStatusRank(component.Status) > componentStatusRank(status) {
			status = component.Status
		}
	}
	if status == domain.ComponentStatusOperational && unknown {
		return domain.ComponentStatusUnknown
	}
	return status
}

func componentStatusRank(status domain.ComponentStatus) int {
	switch status {
	case domain.ComponentStatusOperational:
		return 0
	case domain.ComponentStatusUnderMaintenance:
		return 1
	case domain.ComponentStatusDegraded:
		return 2
	case domain.ComponentStatusPartialOutage:
		return 3
	case domain.ComponentStatusMajorOutage:
		return 4
	default:
		return -1
	}
}

func summaryAuthority() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceSummary,
		domain.ResourceStatus,
		domain.ResourceComponents,
		domain.ResourceUnresolvedIncidents,
	}
}

func requireObjectFields(body []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if err := decodeJSON(body, &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("top-level value must be an object")
	}
	for _, field := range fields {
		value, exists := object[field]
		if !exists {
			return fmt.Errorf("missing top-level field %q", field)
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("top-level field %q must not be null", field)
		}
	}
	return nil
}
