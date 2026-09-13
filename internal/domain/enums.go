package domain

// SourceKind describes how a source exposes status information.
type SourceKind string

const (
	SourceKindStatusPage       SourceKind = "status_page"
	SourceKindFeed             SourceKind = "feed"
	SourceKindWebhook          SourceKind = "webhook"
	SourceKindAccountConnector SourceKind = "account_connector"
	SourceKindSynthetic        SourceKind = "synthetic"
)

func (k SourceKind) Valid() bool {
	switch k {
	case SourceKindStatusPage, SourceKindFeed, SourceKindWebhook,
		SourceKindAccountConnector, SourceKindSynthetic:
		return true
	default:
		return false
	}
}

// ResourceKind is a separately discoverable or fetchable status resource.
type ResourceKind string

const (
	ResourceSummary               ResourceKind = "summary"
	ResourceStatus                ResourceKind = "status"
	ResourceComponents            ResourceKind = "components"
	ResourceIncidents             ResourceKind = "incidents"
	ResourceUnresolvedIncidents   ResourceKind = "unresolved_incidents"
	ResourceIncidentUpdates       ResourceKind = "incident_updates"
	ResourceScheduledMaintenances ResourceKind = "scheduled_maintenances"
)

func (k ResourceKind) Valid() bool {
	switch k {
	case ResourceSummary, ResourceStatus, ResourceComponents, ResourceIncidents,
		ResourceUnresolvedIncidents, ResourceIncidentUpdates, ResourceScheduledMaintenances:
		return true
	default:
		return false
	}
}

// Completeness says whether absence from a snapshot carries information.
// Only Complete snapshots that are authoritative for a resource may be used
// for missing-item reconciliation.
type Completeness string

const (
	CompletenessUnknown  Completeness = "unknown"
	CompletenessPartial  Completeness = "partial"
	CompletenessComplete Completeness = "complete"
)

func (c Completeness) Valid() bool {
	switch c {
	case CompletenessUnknown, CompletenessPartial, CompletenessComplete:
		return true
	default:
		return false
	}
}

type PaginationKind string

const (
	PaginationNone   PaginationKind = "none"
	PaginationPage   PaginationKind = "page"
	PaginationCursor PaginationKind = "cursor"
	PaginationLink   PaginationKind = "link"
)

func (k PaginationKind) Valid() bool {
	switch k {
	case PaginationNone, PaginationPage, PaginationCursor, PaginationLink:
		return true
	default:
		return false
	}
}

type ComponentStatus string

const (
	ComponentStatusOperational      ComponentStatus = "operational"
	ComponentStatusDegraded         ComponentStatus = "degraded"
	ComponentStatusPartialOutage    ComponentStatus = "partial_outage"
	ComponentStatusMajorOutage      ComponentStatus = "major_outage"
	ComponentStatusUnderMaintenance ComponentStatus = "under_maintenance"
	ComponentStatusUnknown          ComponentStatus = "unknown"
)

func (s ComponentStatus) Valid() bool {
	switch s {
	case ComponentStatusOperational, ComponentStatusDegraded,
		ComponentStatusPartialOutage, ComponentStatusMajorOutage,
		ComponentStatusUnderMaintenance, ComponentStatusUnknown:
		return true
	default:
		return false
	}
}

type IncidentPhase string

const (
	IncidentPhaseInvestigating IncidentPhase = "investigating"
	IncidentPhaseIdentified    IncidentPhase = "identified"
	IncidentPhaseMonitoring    IncidentPhase = "monitoring"
	IncidentPhaseResolved      IncidentPhase = "resolved"
	IncidentPhaseUnknown       IncidentPhase = "unknown"
)

func (p IncidentPhase) Valid() bool {
	switch p {
	case IncidentPhaseInvestigating, IncidentPhaseIdentified,
		IncidentPhaseMonitoring, IncidentPhaseResolved, IncidentPhaseUnknown:
		return true
	default:
		return false
	}
}

type Impact string

const (
	ImpactNone     Impact = "none"
	ImpactMinor    Impact = "minor"
	ImpactMajor    Impact = "major"
	ImpactCritical Impact = "critical"
	ImpactUnknown  Impact = "unknown"
)

func (i Impact) Valid() bool {
	switch i {
	case ImpactNone, ImpactMinor, ImpactMajor, ImpactCritical, ImpactUnknown:
		return true
	default:
		return false
	}
}

type IncidentKind string

const (
	IncidentKindIncident    IncidentKind = "incident"
	IncidentKindMaintenance IncidentKind = "maintenance"
)

func (k IncidentKind) Valid() bool {
	return k == IncidentKindIncident || k == IncidentKindMaintenance
}

type EntityKind string

const (
	EntityIncident    EntityKind = "incident"
	EntityMaintenance EntityKind = "maintenance"
	EntityComponent   EntityKind = "component"
	EntitySource      EntityKind = "source"
)

func (k EntityKind) Valid() bool {
	switch k {
	case EntityIncident, EntityMaintenance, EntityComponent, EntitySource:
		return true
	default:
		return false
	}
}

type EventKind string

const (
	EventKindIncidentCreated        EventKind = "incident.created"
	EventKindIncidentUpdated        EventKind = "incident.updated"
	EventKindIncidentResolved       EventKind = "incident.resolved"
	EventKindMaintenanceScheduled   EventKind = "maintenance.scheduled"
	EventKindMaintenanceStarted     EventKind = "maintenance.started"
	EventKindMaintenanceCompleted   EventKind = "maintenance.completed"
	EventKindComponentStatusChanged EventKind = "component.status_changed"
	EventKindSourceDegraded         EventKind = "source.degraded"
	EventKindSourceRecovered        EventKind = "source.recovered"
)

func (k EventKind) Valid() bool {
	switch k {
	case EventKindIncidentCreated, EventKindIncidentUpdated, EventKindIncidentResolved,
		EventKindMaintenanceScheduled, EventKindMaintenanceStarted,
		EventKindMaintenanceCompleted, EventKindComponentStatusChanged,
		EventKindSourceDegraded, EventKindSourceRecovered:
		return true
	default:
		return false
	}
}
