package reconcile

import (
	"slices"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type observationProjection struct {
	SourceID          string                 `json:"source_id"`
	ResourceKind      domain.ResourceKind    `json:"resource_kind"`
	OverallStatus     domain.ComponentStatus `json:"overall_status"`
	RawOverallStatus  string                 `json:"raw_overall_status,omitempty"`
	ComputedStatus    domain.ComponentStatus `json:"computed_status"`
	Components        []componentProjection  `json:"components,omitempty"`
	Incidents         []incidentProjection   `json:"incidents,omitempty"`
	Completeness      domain.Completeness    `json:"completeness"`
	AuthoritativeFor  []domain.ResourceKind  `json:"authoritative_for,omitempty"`
	SourceUpdatedAt   *time.Time             `json:"source_updated_at,omitempty"`
	ObservedAt        time.Time              `json:"observed_at"`
	SchemaVersion     string                 `json:"schema_version"`
	AdapterVersion    string                 `json:"adapter_version"`
	NormalizerVersion string                 `json:"normalizer_version"`
}

func snapshotObservationKey(snapshot domain.Snapshot) (string, error) {
	components := make([]componentProjection, 0, len(snapshot.Components))
	for _, component := range snapshot.Components {
		components = append(components, projectComponent(normalizeComponent(component)))
	}
	slices.SortFunc(components, func(left, right componentProjection) int {
		return strings.Compare(entityID(left.ID, left.UpstreamID), entityID(right.ID, right.UpstreamID))
	})
	incidents := make([]incidentProjection, 0, len(snapshot.Incidents))
	for _, incident := range snapshot.Incidents {
		incidents = append(incidents, projectIncident(normalizeIncident(incident)))
	}
	slices.SortFunc(incidents, func(left, right incidentProjection) int {
		return strings.Compare(entityID(left.ID, left.UpstreamID), entityID(right.ID, right.UpstreamID))
	})
	authoritativeFor := append([]domain.ResourceKind(nil), snapshot.AuthoritativeFor...)
	slices.Sort(authoritativeFor)
	projection := observationProjection{
		SourceID:          snapshot.Source.ID,
		ResourceKind:      snapshot.ResourceKind,
		OverallStatus:     normalizeComponentStatus(snapshot.OverallStatus),
		RawOverallStatus:  snapshot.RawOverallStatus,
		ComputedStatus:    normalizeComponentStatus(snapshot.ComputedStatus),
		Components:        components,
		Incidents:         incidents,
		Completeness:      snapshot.Completeness,
		AuthoritativeFor:  authoritativeFor,
		SourceUpdatedAt:   snapshot.SourceUpdatedAt,
		ObservedAt:        snapshot.ObservedAt,
		SchemaVersion:     snapshot.SchemaVersion,
		AdapterVersion:    snapshot.AdapterVersion,
		NormalizerVersion: snapshot.NormalizerVersion,
	}
	return domain.SemanticHash(observationHashVersion, projection)
}

func normalizeComponentStatus(status domain.ComponentStatus) domain.ComponentStatus {
	if !status.Valid() {
		return domain.ComponentStatusUnknown
	}
	return status
}
