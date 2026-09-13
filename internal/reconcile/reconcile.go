package reconcile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const observationHashVersion = "reconcile-observation/v1"

var (
	ErrInvalidSnapshot = errors.New("reconcile: invalid snapshot")
	ErrInvalidState    = errors.New("reconcile: invalid state")
)

// Apply deterministically applies snapshot to previous. It never mutates
// previous or snapshot. The first accepted snapshot establishes a baseline and
// emits no historical events.
func Apply(previous State, snapshot domain.Snapshot) (Result, error) {
	if err := validateSnapshot(snapshot); err != nil {
		return Result{}, err
	}
	if err := validateState(previous, snapshot.Source.ID); err != nil {
		return Result{}, err
	}

	next := cloneState(previous)
	if next.SourceID == "" {
		next.SourceID = snapshot.Source.ID
	}
	ensureMaps(&next)

	observationKey, err := snapshotObservationKey(snapshot)
	if err != nil {
		return Result{}, fmt.Errorf("%w: fingerprint: %v", ErrInvalidSnapshot, err)
	}
	if next.AppliedObservations[observationKey] {
		return Result{State: next, Duplicate: true}, nil
	}

	baseline := !next.BaselineEstablished
	changed := false
	events := make([]domain.CanonicalEvent, 0)

	components := append([]domain.Component(nil), snapshot.Components...)
	slices.SortFunc(components, func(left, right domain.Component) int {
		return strings.Compare(entityID(left.ID, left.UpstreamID), entityID(right.ID, right.UpstreamID))
	})
	for _, incoming := range components {
		componentChanged, event, emitted, applyErr := applyComponent(&next, snapshot, incoming, baseline)
		if applyErr != nil {
			return Result{}, applyErr
		}
		changed = changed || componentChanged
		if emitted {
			events = append(events, event)
		}
	}

	incidents := append([]domain.Incident(nil), snapshot.Incidents...)
	slices.SortFunc(incidents, func(left, right domain.Incident) int {
		return strings.Compare(entityID(left.ID, left.UpstreamID), entityID(right.ID, right.UpstreamID))
	})
	presentIncidents := make(map[string]struct{}, len(incidents))
	for _, incoming := range incidents {
		id := entityID(incoming.ID, incoming.UpstreamID)
		presentIncidents[id] = struct{}{}
		incidentChanged, event, emitted, applyErr := applyIncident(&next, snapshot, incoming, baseline)
		if applyErr != nil {
			return Result{}, applyErr
		}
		changed = changed || incidentChanged
		if emitted {
			events = append(events, event)
		}
	}

	if canInferIncidentAbsence(snapshot) {
		missingChanged, missingEvents, applyErr := applyMissingIncidents(&next, snapshot, presentIncidents, baseline)
		if applyErr != nil {
			return Result{}, applyErr
		}
		changed = changed || missingChanged
		events = append(events, missingEvents...)
	}

	next.AppliedObservations[observationKey] = true
	if baseline {
		next.BaselineEstablished = true
		changed = true
	}

	return Result{
		State:               next,
		Events:              events,
		BaselineEstablished: baseline,
		Changed:             changed,
	}, nil
}

func validateSnapshot(snapshot domain.Snapshot) error {
	if strings.TrimSpace(snapshot.Source.ID) == "" {
		return fmt.Errorf("%w: source ID is required", ErrInvalidSnapshot)
	}
	if !snapshot.ResourceKind.Valid() {
		return fmt.Errorf("%w: resource kind %q is invalid", ErrInvalidSnapshot, snapshot.ResourceKind)
	}
	if !snapshot.Completeness.Valid() {
		return fmt.Errorf("%w: completeness %q is invalid", ErrInvalidSnapshot, snapshot.Completeness)
	}
	if snapshot.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at is required", ErrInvalidSnapshot)
	}
	if strings.TrimSpace(snapshot.NormalizerVersion) == "" {
		return fmt.Errorf("%w: normalizer version is required", ErrInvalidSnapshot)
	}
	if strings.TrimSpace(snapshot.SchemaVersion) == "" {
		return fmt.Errorf("%w: schema version is required", ErrInvalidSnapshot)
	}

	componentIDs := make(map[string]struct{}, len(snapshot.Components))
	for index, component := range snapshot.Components {
		id := entityID(component.ID, component.UpstreamID)
		if id == "" {
			return fmt.Errorf("%w: components[%d] has no ID", ErrInvalidSnapshot, index)
		}
		if _, duplicate := componentIDs[id]; duplicate {
			return fmt.Errorf("%w: duplicate component ID %q", ErrInvalidSnapshot, id)
		}
		componentIDs[id] = struct{}{}
	}

	incidentIDs := make(map[string]struct{}, len(snapshot.Incidents))
	for index, incident := range snapshot.Incidents {
		id := entityID(incident.ID, incident.UpstreamID)
		if id == "" {
			return fmt.Errorf("%w: incidents[%d] has no ID", ErrInvalidSnapshot, index)
		}
		if _, duplicate := incidentIDs[id]; duplicate {
			return fmt.Errorf("%w: duplicate incident ID %q", ErrInvalidSnapshot, id)
		}
		incidentIDs[id] = struct{}{}
	}
	return nil
}

func validateState(state State, sourceID string) error {
	if state.SourceID != "" && state.SourceID != sourceID {
		return fmt.Errorf("%w: source %q cannot accept snapshot for %q", ErrInvalidState, state.SourceID, sourceID)
	}
	for id, component := range state.Components {
		if strings.TrimSpace(id) == "" || component.Revision == 0 {
			return fmt.Errorf("%w: component %q has invalid revision", ErrInvalidState, id)
		}
	}
	for id, incident := range state.Incidents {
		if strings.TrimSpace(id) == "" || incident.Revision == 0 {
			return fmt.Errorf("%w: incident %q has invalid revision", ErrInvalidState, id)
		}
	}
	return nil
}

func canInferIncidentAbsence(snapshot domain.Snapshot) bool {
	return snapshot.CanInferAbsence(domain.ResourceUnresolvedIncidents) ||
		snapshot.CanInferAbsence(domain.ResourceIncidents)
}

func entityID(id, upstreamID string) string {
	if id = strings.TrimSpace(id); id != "" {
		return id
	}
	return strings.TrimSpace(upstreamID)
}

func entitySourceTime(entityTime, snapshotTime *time.Time) *time.Time {
	if entityTime != nil {
		value := entityTime.UTC()
		return &value
	}
	if snapshotTime != nil {
		value := snapshotTime.UTC()
		return &value
	}
	return nil
}

func compareWatermark(candidate, current Watermark) int {
	if candidate.SourceUpdatedAt != nil && current.SourceUpdatedAt != nil {
		if candidate.SourceUpdatedAt.Before(*current.SourceUpdatedAt) {
			return -1
		}
		if candidate.SourceUpdatedAt.After(*current.SourceUpdatedAt) {
			return 1
		}
	}
	if candidate.ObservedAt.Before(current.ObservedAt) {
		return -1
	}
	if candidate.ObservedAt.After(current.ObservedAt) {
		return 1
	}
	return 0
}
