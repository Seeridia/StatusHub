package reconcile

import (
	"slices"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

func ensureMaps(state *State) {
	if state.Components == nil {
		state.Components = make(map[string]ComponentState)
	}
	if state.Incidents == nil {
		state.Incidents = make(map[string]IncidentState)
	}
	if state.AppliedObservations == nil {
		state.AppliedObservations = make(map[string]bool)
	}
}

func cloneState(state State) State {
	cloned := State{
		SourceID:            state.SourceID,
		BaselineEstablished: state.BaselineEstablished,
		Components:          make(map[string]ComponentState, len(state.Components)),
		Incidents:           make(map[string]IncidentState, len(state.Incidents)),
		AppliedObservations: make(map[string]bool, len(state.AppliedObservations)),
	}
	for id, aggregate := range state.Components {
		aggregate.Component = cloneComponent(aggregate.Component)
		aggregate.Watermark.SourceUpdatedAt = cloneTime(aggregate.Watermark.SourceUpdatedAt)
		cloned.Components[id] = aggregate
	}
	for id, aggregate := range state.Incidents {
		aggregate.Incident = cloneIncident(aggregate.Incident)
		aggregate.Watermark.SourceUpdatedAt = cloneTime(aggregate.Watermark.SourceUpdatedAt)
		cloned.Incidents[id] = aggregate
	}
	for key, applied := range state.AppliedObservations {
		cloned.AppliedObservations[key] = applied
	}
	return cloned
}

func cloneComponent(component domain.Component) domain.Component {
	component.Tags = cloneStringMap(component.Tags)
	component.SourceCreatedAt = cloneTime(component.SourceCreatedAt)
	component.SourceUpdatedAt = cloneTime(component.SourceUpdatedAt)
	return component
}

func cloneIncident(incident domain.Incident) domain.Incident {
	incident.ComponentIDs = slices.Clone(incident.ComponentIDs)
	incident.Updates = slices.Clone(incident.Updates)
	for index, update := range incident.Updates {
		update.ComponentIDs = slices.Clone(update.ComponentIDs)
		update.SourceCreatedAt = cloneTime(update.SourceCreatedAt)
		update.SourceUpdatedAt = cloneTime(update.SourceUpdatedAt)
		incident.Updates[index] = update
	}
	incident.StartedAt = cloneTime(incident.StartedAt)
	incident.ScheduledFor = cloneTime(incident.ScheduledFor)
	incident.ScheduledUntil = cloneTime(incident.ScheduledUntil)
	incident.ResolvedAt = cloneTime(incident.ResolvedAt)
	incident.SourceCreatedAt = cloneTime(incident.SourceCreatedAt)
	incident.SourceUpdatedAt = cloneTime(incident.SourceUpdatedAt)
	return incident
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}

func sortedStrings(values []string) []string {
	cloned := slices.Clone(values)
	slices.Sort(cloned)
	return cloned
}
