package ecosystem

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const EngineStatusIO = "status-io-v1"

type statusIOPayload struct {
	Result struct {
		StatusOverall statusIOStatus      `json:"status_overall"`
		Status        []statusIOComponent `json:"status"`
		Incidents     []statusIOIncident  `json:"incidents"`
		Maintenance   struct {
			Active   []statusIOMaintenance `json:"active"`
			Upcoming []statusIOMaintenance `json:"upcoming"`
		} `json:"maintenance"`
	} `json:"result"`
}

type statusIOStatus struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code"`
	Updated    string `json:"updated"`
}

type statusIOComponent struct {
	statusIOStatus
	Containers []statusIOStatus `json:"containers"`
}

type statusIOMessage struct {
	Details  string `json:"details"`
	Datetime string `json:"datetime"`
	Status   int    `json:"status"`
	State    int    `json:"state"`
}

type statusIOAffected struct {
	ID   string `json:"_id"`
	Name string `json:"name"`
}

type statusIOIncident struct {
	ID                 string             `json:"_id"`
	Name               string             `json:"name"`
	DatetimeOpen       string             `json:"datetime_open"`
	DatetimeResolved   string             `json:"datetime_resolved"`
	CurrentStatus      int                `json:"current_status"`
	CurrentState       int                `json:"current_state"`
	Messages           []statusIOMessage  `json:"messages"`
	ComponentsAffected []statusIOAffected `json:"components_affected"`
}

type statusIOMaintenance struct {
	statusIOIncident
	DatetimePlannedStart string `json:"datetime_planned_start"`
	DatetimePlannedEnd   string `json:"datetime_planned_end"`
}

func definitionStatusIO() engineDefinition {
	return engineDefinition{name: EngineStatusIO, version: "1.0", confidence: 1, endpoints: func(target domain.Target) ([]endpointDefinition, error) {
		endpoint := ""
		if target.URL != nil && strings.EqualFold(target.URL.Hostname(), "api.status.io") && strings.HasPrefix(target.URL.Path, "/1.0/status/") {
			endpoint = target.URL.String()
		} else if strings.TrimSpace(target.PageID) != "" {
			endpoint = "https://api.status.io/1.0/status/" + url.PathEscape(strings.TrimSpace(target.PageID))
		} else {
			return nil, fmt.Errorf("%w: Status.io requires page_id or an api.status.io status URL", ErrInvalid)
		}
		return []endpointDefinition{{resource: domain.ResourceSummary, path: endpoint, required: true,
			completeness:     domain.CompletenessComplete,
			authoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
			decode:           decodeStatusIO}}, nil
	}}
}

func decodeStatusIO(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	var payload statusIOPayload
	if err := decodeStrict(body, &payload); err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: Status.io: %v", ErrInvalid, err)
	}
	if strings.TrimSpace(payload.Result.StatusOverall.Status) == "" || payload.Result.StatusOverall.Updated == "" {
		return domain.Snapshot{}, fmt.Errorf("%w: Status.io overall status and updated are required", ErrInvalid)
	}
	components := make([]domain.Component, 0)
	for _, raw := range payload.Result.Status {
		if raw.ID == "" || raw.Name == "" || raw.Status == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: Status.io component identity, name and status are required", ErrInvalid)
		}
		components = append(components, statusIOComponentValue(raw.statusIOStatus, ""))
		for _, container := range raw.Containers {
			components = append(components, statusIOComponentValue(container, raw.ID))
		}
	}
	incidents := make([]domain.Incident, 0, len(payload.Result.Incidents)+len(payload.Result.Maintenance.Active)+len(payload.Result.Maintenance.Upcoming))
	for _, raw := range payload.Result.Incidents {
		incident, err := normalizeStatusIOIncident(raw, domain.IncidentKindIncident, false)
		if err != nil {
			return domain.Snapshot{}, err
		}
		incidents = append(incidents, incident)
	}
	for _, raw := range payload.Result.Maintenance.Active {
		incident, err := normalizeStatusIOIncident(raw.statusIOIncident, domain.IncidentKindMaintenance, false)
		if err != nil {
			return domain.Snapshot{}, err
		}
		incident.ScheduledFor, incident.ScheduledUntil = parseTime(raw.DatetimePlannedStart), parseTime(raw.DatetimePlannedEnd)
		incidents = append(incidents, incident)
	}
	for _, raw := range payload.Result.Maintenance.Upcoming {
		incident, err := normalizeStatusIOIncident(raw.statusIOIncident, domain.IncidentKindMaintenance, true)
		if err != nil {
			return domain.Snapshot{}, err
		}
		incident.ScheduledFor, incident.ScheduledUntil = parseTime(raw.DatetimePlannedStart), parseTime(raw.DatetimePlannedEnd)
		incidents = append(incidents, incident)
	}
	source = sourceForEngine(source, EngineStatusIO)
	overall := normalizeComponentStatus(payload.Result.StatusOverall.Status)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall,
		RawOverallStatus: payload.Result.StatusOverall.Status, ComputedStatus: computedStatus(components), Components: components,
		Incidents: incidents, Completeness: domain.CompletenessComplete,
		AuthoritativeFor: []domain.ResourceKind{domain.ResourceSummary, domain.ResourceStatus, domain.ResourceComponents, domain.ResourceUnresolvedIncidents, domain.ResourceScheduledMaintenances},
		SourceUpdatedAt:  parseTime(payload.Result.StatusOverall.Updated), ObservedAt: observedAt,
		SchemaVersion: "1.0", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func statusIOComponentValue(raw statusIOStatus, groupID string) domain.Component {
	id := raw.ID
	if groupID != "" {
		id = groupID + ":" + raw.ID
	}
	return domain.Component{ID: id, UpstreamID: raw.ID, Name: raw.Name, GroupID: groupID,
		Status: normalizeComponentStatus(raw.Status), RawStatus: raw.Status, SourceUpdatedAt: parseTime(raw.Updated)}
}

func normalizeStatusIOIncident(raw statusIOIncident, kind domain.IncidentKind, scheduled bool) (domain.Incident, error) {
	if raw.ID == "" || strings.TrimSpace(raw.Name) == "" {
		return domain.Incident{}, fmt.Errorf("%w: Status.io incident id and name are required", ErrInvalid)
	}
	componentIDs := make([]string, 0, len(raw.ComponentsAffected))
	for _, affected := range raw.ComponentsAffected {
		componentIDs = append(componentIDs, affected.ID)
	}
	updates := make([]domain.IncidentUpdate, 0, len(raw.Messages))
	for index, message := range raw.Messages {
		id := stableID("statusio-update", raw.ID, fmt.Sprint(index), message.Datetime, message.Details)
		updates = append(updates, domain.IncidentUpdate{ID: id, IncidentID: raw.ID, Body: message.Details,
			Phase: statusIOPhase(message.Status, message.State, false), RawPhase: fmt.Sprintf("status=%d,state=%d", message.Status, message.State),
			Impact: statusIOImpact(message.State), SourceCreatedAt: parseTime(message.Datetime)})
	}
	resolved := raw.DatetimeResolved != ""
	phase := statusIOPhase(raw.CurrentStatus, raw.CurrentState, resolved)
	if scheduled && phase == domain.IncidentPhaseUnknown {
		phase = domain.IncidentPhaseIdentified
	}
	return domain.Incident{ID: raw.ID, UpstreamID: raw.ID, Kind: kind, Name: raw.Name, Phase: phase,
		RawPhase: fmt.Sprintf("status=%d,state=%d", raw.CurrentStatus, raw.CurrentState), Impact: statusIOImpact(raw.CurrentState),
		RawImpact: fmt.Sprint(raw.CurrentState), ComponentIDs: dedupeStrings(componentIDs), Updates: updates,
		StartedAt: parseTime(raw.DatetimeOpen), ResolvedAt: parseTime(raw.DatetimeResolved), SourceCreatedAt: parseTime(raw.DatetimeOpen)}, nil
}

func statusIOPhase(status, state int, resolved bool) domain.IncidentPhase {
	if resolved || status == 500 || status == 600 || state == 100 {
		return domain.IncidentPhaseResolved
	}
	switch status {
	case 100:
		return domain.IncidentPhaseInvestigating
	case 200:
		return domain.IncidentPhaseIdentified
	case 300, 400:
		return domain.IncidentPhaseMonitoring
	default:
		return domain.IncidentPhaseUnknown
	}
}

func statusIOImpact(state int) domain.Impact {
	switch {
	case state >= 500:
		return domain.ImpactCritical
	case state >= 300:
		return domain.ImpactMajor
	case state >= 200:
		return domain.ImpactMinor
	case state == 100:
		return domain.ImpactNone
	default:
		return domain.ImpactUnknown
	}
}
