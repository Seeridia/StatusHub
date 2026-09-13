package reconcile

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

type componentProjection struct {
	ID          string                 `json:"id"`
	UpstreamID  string                 `json:"upstream_id,omitempty"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	GroupID     string                 `json:"group_id,omitempty"`
	Status      domain.ComponentStatus `json:"status"`
	RawStatus   string                 `json:"raw_status,omitempty"`
	Tags        map[string]string      `json:"tags,omitempty"`
}

type incidentProjection struct {
	ID             string                  `json:"id"`
	UpstreamID     string                  `json:"upstream_id,omitempty"`
	Kind           domain.IncidentKind     `json:"kind"`
	Name           string                  `json:"name"`
	URL            string                  `json:"url,omitempty"`
	Phase          domain.IncidentPhase    `json:"phase"`
	RawPhase       string                  `json:"raw_phase,omitempty"`
	Impact         domain.Impact           `json:"impact"`
	RawImpact      string                  `json:"raw_impact,omitempty"`
	ComponentIDs   []string                `json:"component_ids,omitempty"`
	Updates        []incidentUpdateProject `json:"updates,omitempty"`
	StartedAt      *time.Time              `json:"started_at,omitempty"`
	ScheduledFor   *time.Time              `json:"scheduled_for,omitempty"`
	ScheduledUntil *time.Time              `json:"scheduled_until,omitempty"`
	ResolvedAt     *time.Time              `json:"resolved_at,omitempty"`
}

type incidentUpdateProject struct {
	ID           string               `json:"id"`
	UpstreamID   string               `json:"upstream_id,omitempty"`
	IncidentID   string               `json:"incident_id"`
	Body         string               `json:"body,omitempty"`
	Phase        domain.IncidentPhase `json:"phase"`
	RawPhase     string               `json:"raw_phase,omitempty"`
	Impact       domain.Impact        `json:"impact"`
	RawImpact    string               `json:"raw_impact,omitempty"`
	ComponentIDs []string             `json:"component_ids,omitempty"`
}

type componentEventPayload struct {
	Previous *domain.Component `json:"previous,omitempty"`
	Current  domain.Component  `json:"current"`
}

type incidentEventPayload struct {
	Previous         *domain.Incident `json:"previous,omitempty"`
	Current          domain.Incident  `json:"current"`
	ResolutionReason string           `json:"resolution_reason,omitempty"`
}

func applyComponent(state *State, snapshot domain.Snapshot, incoming domain.Component, baseline bool) (bool, domain.CanonicalEvent, bool, error) {
	incoming = normalizeComponent(incoming)
	id := entityID(incoming.ID, incoming.UpstreamID)
	hash, err := domain.SemanticHash(snapshot.NormalizerVersion, projectComponent(incoming))
	if err != nil {
		return false, domain.CanonicalEvent{}, false, fmt.Errorf("reconcile component %q: %w", id, err)
	}
	watermark := Watermark{
		SourceUpdatedAt: entitySourceTime(incoming.SourceUpdatedAt, snapshot.SourceUpdatedAt),
		ObservedAt:      snapshot.ObservedAt.UTC(),
	}

	current, exists := state.Components[id]
	if !exists {
		state.Components[id] = ComponentState{
			Component:    cloneComponent(incoming),
			Revision:     1,
			SemanticHash: hash,
			Watermark:    watermark,
		}
		return true, domain.CanonicalEvent{}, false, nil
	}
	if compareWatermark(watermark, current.Watermark) < 0 {
		return false, domain.CanonicalEvent{}, false, nil
	}
	if current.SemanticHash == hash {
		current.Component = cloneComponent(incoming)
		current.Watermark = watermark
		state.Components[id] = current
		return false, domain.CanonicalEvent{}, false, nil
	}

	previous := cloneComponent(current.Component)
	current.Component = cloneComponent(incoming)
	current.SemanticHash = hash
	current.Watermark = watermark
	current.Revision++
	state.Components[id] = current

	if baseline || previous.Status == incoming.Status {
		return true, domain.CanonicalEvent{}, false, nil
	}
	event, err := newCanonicalEvent(snapshot, domain.EventKindComponentStatusChanged, domain.EntityComponent, id, current.Revision, componentEventPayload{
		Previous: &previous,
		Current:  cloneComponent(incoming),
	}, incoming.SourceUpdatedAt)
	if err != nil {
		return false, domain.CanonicalEvent{}, false, err
	}
	return true, event, true, nil
}

func applyIncident(state *State, snapshot domain.Snapshot, incoming domain.Incident, baseline bool) (bool, domain.CanonicalEvent, bool, error) {
	incoming = normalizeIncident(incoming)
	id := entityID(incoming.ID, incoming.UpstreamID)
	hash, err := domain.SemanticHash(snapshot.NormalizerVersion, projectIncident(incoming))
	if err != nil {
		return false, domain.CanonicalEvent{}, false, fmt.Errorf("reconcile incident %q: %w", id, err)
	}
	watermark := Watermark{
		SourceUpdatedAt: entitySourceTime(incoming.SourceUpdatedAt, snapshot.SourceUpdatedAt),
		ObservedAt:      snapshot.ObservedAt.UTC(),
	}

	current, exists := state.Incidents[id]
	if !exists {
		state.Incidents[id] = IncidentState{
			Incident:       cloneIncident(incoming),
			Revision:       1,
			SemanticHash:   hash,
			Watermark:      watermark,
			LastObservedAt: snapshot.ObservedAt.UTC(),
		}
		if baseline {
			return true, domain.CanonicalEvent{}, false, nil
		}
		event, eventErr := newCanonicalEvent(snapshot, domain.EventKindIncidentCreated, domain.EntityIncident, id, 1, incidentEventPayload{
			Current: cloneIncident(incoming),
		}, incoming.SourceUpdatedAt)
		if eventErr != nil {
			return false, domain.CanonicalEvent{}, false, eventErr
		}
		return true, event, true, nil
	}
	if snapshot.ObservedAt.Before(current.LastObservedAt) || compareWatermark(watermark, current.Watermark) < 0 {
		return false, domain.CanonicalEvent{}, false, nil
	}

	current.LastObservedAt = snapshot.ObservedAt.UTC()
	current.MissingCount = 0
	if current.SemanticHash == hash {
		current.Incident = cloneIncident(incoming)
		current.Watermark = watermark
		state.Incidents[id] = current
		return false, domain.CanonicalEvent{}, false, nil
	}

	previous := cloneIncident(current.Incident)
	current.Incident = cloneIncident(incoming)
	current.SemanticHash = hash
	current.Watermark = watermark
	current.Revision++
	state.Incidents[id] = current

	if baseline {
		return true, domain.CanonicalEvent{}, false, nil
	}
	kind := domain.EventKindIncidentUpdated
	if previous.Phase != domain.IncidentPhaseResolved && incoming.Phase == domain.IncidentPhaseResolved {
		kind = domain.EventKindIncidentResolved
	}
	event, eventErr := newCanonicalEvent(snapshot, kind, domain.EntityIncident, id, current.Revision, incidentEventPayload{
		Previous: &previous,
		Current:  cloneIncident(incoming),
	}, incoming.SourceUpdatedAt)
	if eventErr != nil {
		return false, domain.CanonicalEvent{}, false, eventErr
	}
	return true, event, true, nil
}

func applyMissingIncidents(state *State, snapshot domain.Snapshot, present map[string]struct{}, baseline bool) (bool, []domain.CanonicalEvent, error) {
	ids := make([]string, 0, len(state.Incidents))
	for id := range state.Incidents {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	changed := false
	events := make([]domain.CanonicalEvent, 0)
	for _, id := range ids {
		if _, exists := present[id]; exists {
			continue
		}
		current := state.Incidents[id]
		if current.Incident.Phase == domain.IncidentPhaseResolved || !snapshot.ObservedAt.After(current.LastObservedAt) {
			continue
		}

		current.LastObservedAt = snapshot.ObservedAt.UTC()
		if current.MissingCount < 2 {
			current.MissingCount++
		}
		changed = true
		if current.MissingCount < 2 {
			state.Incidents[id] = current
			continue
		}

		previous := cloneIncident(current.Incident)
		resolvedAt := snapshot.ObservedAt.UTC()
		current.Incident.Phase = domain.IncidentPhaseResolved
		current.Incident.ResolvedAt = &resolvedAt
		current.Watermark = Watermark{
			// Absence has no entity-level upstream timestamp. Keeping this nil
			// lets a later, newer observation reopen the incident even when the
			// reappearing entity carries an older provider timestamp.
			SourceUpdatedAt: nil,
			ObservedAt:      resolvedAt,
		}
		current.Revision++
		hash, err := domain.SemanticHash(snapshot.NormalizerVersion, projectIncident(current.Incident))
		if err != nil {
			return false, nil, fmt.Errorf("reconcile missing incident %q: %w", id, err)
		}
		current.SemanticHash = hash
		state.Incidents[id] = current

		if baseline {
			continue
		}
		event, err := newCanonicalEvent(snapshot, domain.EventKindIncidentResolved, domain.EntityIncident, id, current.Revision, incidentEventPayload{
			Previous:         &previous,
			Current:          cloneIncident(current.Incident),
			ResolutionReason: "missing_from_two_consecutive_authoritative_snapshots",
		}, snapshot.SourceUpdatedAt)
		if err != nil {
			return false, nil, err
		}
		events = append(events, event)
	}
	return changed, events, nil
}

func newCanonicalEvent(snapshot domain.Snapshot, kind domain.EventKind, entityKind domain.EntityKind, entityID string, revision uint64, payload any, sourceUpdatedAt *time.Time) (domain.CanonicalEvent, error) {
	canonicalPayload, err := domain.CanonicalJSON(payload)
	if err != nil {
		return domain.CanonicalEvent{}, fmt.Errorf("reconcile %s %q payload: %w", kind, entityID, err)
	}
	projectionHash, err := domain.SemanticHash(snapshot.NormalizerVersion, json.RawMessage(canonicalPayload))
	if err != nil {
		return domain.CanonicalEvent{}, fmt.Errorf("reconcile %s %q hash: %w", kind, entityID, err)
	}
	key, err := (domain.SourceEventIdentity{
		SourceID:               snapshot.Source.ID,
		Provider:               snapshot.Source.Provider,
		PageID:                 snapshot.Source.PageID,
		Kind:                   kind,
		EntityKind:             entityKind,
		EntityID:               entityID,
		SourceUpdatedAt:        sourceUpdatedAt,
		NormalizerVersion:      snapshot.NormalizerVersion,
		SemanticProjectionHash: projectionHash,
	}).Key()
	if err != nil {
		return domain.CanonicalEvent{}, fmt.Errorf("reconcile %s %q key: %w", kind, entityID, err)
	}
	return domain.CanonicalEvent{
		SourceID:          snapshot.Source.ID,
		Provider:          snapshot.Source.Provider,
		PageID:            snapshot.Source.PageID,
		Kind:              kind,
		EntityKind:        entityKind,
		EntityID:          entityID,
		AggregateRevision: revision,
		SourceEventKey:    key,
		NormalizerVersion: snapshot.NormalizerVersion,
		SchemaVersion:     snapshot.SchemaVersion,
		Payload:           canonicalPayload,
		SourceUpdatedAt:   cloneTime(sourceUpdatedAt),
		ObservedAt:        snapshot.ObservedAt.UTC(),
	}, nil
}

func normalizeComponent(component domain.Component) domain.Component {
	component = cloneComponent(component)
	if !component.Status.Valid() {
		component.Status = domain.ComponentStatusUnknown
	}
	if component.ID == "" {
		component.ID = strings.TrimSpace(component.UpstreamID)
	}
	return component
}

func normalizeIncident(incident domain.Incident) domain.Incident {
	incident = cloneIncident(incident)
	if !incident.Kind.Valid() {
		incident.Kind = domain.IncidentKindIncident
	}
	if !incident.Phase.Valid() {
		incident.Phase = domain.IncidentPhaseUnknown
	}
	if !incident.Impact.Valid() {
		incident.Impact = domain.ImpactUnknown
	}
	if incident.ID == "" {
		incident.ID = strings.TrimSpace(incident.UpstreamID)
	}
	for index := range incident.Updates {
		if !incident.Updates[index].Phase.Valid() {
			incident.Updates[index].Phase = domain.IncidentPhaseUnknown
		}
		if !incident.Updates[index].Impact.Valid() {
			incident.Updates[index].Impact = domain.ImpactUnknown
		}
	}
	return incident
}

func projectComponent(component domain.Component) componentProjection {
	return componentProjection{
		ID:          component.ID,
		UpstreamID:  component.UpstreamID,
		Name:        component.Name,
		Description: component.Description,
		GroupID:     component.GroupID,
		Status:      component.Status,
		RawStatus:   component.RawStatus,
		Tags:        cloneStringMap(component.Tags),
	}
}

func projectIncident(incident domain.Incident) incidentProjection {
	updates := make([]incidentUpdateProject, 0, len(incident.Updates))
	for _, update := range incident.Updates {
		componentIDs := sortedStrings(update.ComponentIDs)
		updates = append(updates, incidentUpdateProject{
			ID:           update.ID,
			UpstreamID:   update.UpstreamID,
			IncidentID:   update.IncidentID,
			Body:         update.Body,
			Phase:        update.Phase,
			RawPhase:     update.RawPhase,
			Impact:       update.Impact,
			RawImpact:    update.RawImpact,
			ComponentIDs: componentIDs,
		})
	}
	slices.SortFunc(updates, func(left, right incidentUpdateProject) int {
		leftID := entityID(left.ID, left.UpstreamID)
		rightID := entityID(right.ID, right.UpstreamID)
		if compared := strings.Compare(leftID, rightID); compared != 0 {
			return compared
		}
		return strings.Compare(left.Body, right.Body)
	})
	return incidentProjection{
		ID:             incident.ID,
		UpstreamID:     incident.UpstreamID,
		Kind:           incident.Kind,
		Name:           incident.Name,
		URL:            incident.URL,
		Phase:          incident.Phase,
		RawPhase:       incident.RawPhase,
		Impact:         incident.Impact,
		RawImpact:      incident.RawImpact,
		ComponentIDs:   sortedStrings(incident.ComponentIDs),
		Updates:        updates,
		StartedAt:      cloneTime(incident.StartedAt),
		ScheduledFor:   cloneTime(incident.ScheduledFor),
		ScheduledUntil: cloneTime(incident.ScheduledUntil),
		ResolvedAt:     cloneTime(incident.ResolvedAt),
	}
}
