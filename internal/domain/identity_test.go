package domain

import (
	"testing"
	"time"
)

func TestObservationIdentityIsStableAcrossEquivalentTimeZones(t *testing.T) {
	t.Parallel()

	shanghai := time.FixedZone("CST", 8*60*60)
	localTime := time.Date(2026, 9, 10, 16, 30, 0, 0, shanghai)
	utcTime := localTime.UTC()

	first, err := (ObservationIdentity{
		SourceID:     "source-1",
		Endpoint:     "/api/v2/summary.json",
		LastModified: &localTime,
		RawHash:      "raw-1",
	}).Key()
	if err != nil {
		t.Fatalf("first observation key: %v", err)
	}
	second, err := (ObservationIdentity{
		SourceID:     "source-1",
		Endpoint:     "/api/v2/summary.json",
		LastModified: &utcTime,
		RawHash:      "raw-1",
	}).Key()
	if err != nil {
		t.Fatalf("second observation key: %v", err)
	}
	if first != second {
		t.Fatalf("equivalent instants produced different keys: %s != %s", first, second)
	}
}

func TestObservationIdentityRequiresEvidence(t *testing.T) {
	t.Parallel()

	if _, err := (ObservationIdentity{SourceID: "source-1", Endpoint: "/status"}).Key(); err == nil {
		t.Fatal("expected observation without validator or raw hash to fail")
	}
}

func TestSourceEventIdentityPrefersUpstreamEventID(t *testing.T) {
	t.Parallel()

	first, err := (SourceEventIdentity{
		SourceID:        "source-1",
		UpstreamEventID: "update-42",
		Kind:            EventKindIncidentUpdated,
		EntityID:        "incident-1",
	}).Key()
	if err != nil {
		t.Fatalf("first source-event key: %v", err)
	}
	second, err := (SourceEventIdentity{
		SourceID:               "source-1",
		UpstreamEventID:        "update-42",
		Kind:                   EventKindIncidentResolved,
		EntityID:               "different-incident",
		NormalizerVersion:      "different-version",
		SemanticProjectionHash: "different-hash",
	}).Key()
	if err != nil {
		t.Fatalf("second source-event key: %v", err)
	}
	if first != second {
		t.Fatal("native upstream event identity should take precedence")
	}
}

func TestSourceEventFallbackUsesVersionedSemanticProjection(t *testing.T) {
	t.Parallel()

	base := SourceEventIdentity{
		SourceID:               "source-1",
		Provider:               "github",
		PageID:                 "page-1",
		Kind:                   EventKindIncidentUpdated,
		EntityKind:             EntityIncident,
		EntityID:               "incident-1",
		NormalizerVersion:      "normalizer/v1",
		SemanticProjectionHash: "projection-a",
	}
	first, err := base.Key()
	if err != nil {
		t.Fatalf("first fallback key: %v", err)
	}
	base.SemanticProjectionHash = "projection-b"
	second, err := base.Key()
	if err != nil {
		t.Fatalf("second fallback key: %v", err)
	}
	if first == second {
		t.Fatal("semantic projection changes must produce a new source-event key")
	}
}

func TestSourceEventAndCanonicalIdentitiesAreDistinctTypes(t *testing.T) {
	t.Parallel()

	sourceKey := SourceEventKey("source-event")
	canonical := CanonicalEventIdentity{ID: CanonicalEventID("0199-canonical"), Revision: 3}
	if string(sourceKey) == string(canonical.ID) {
		t.Fatal("test fixture must use distinct values")
	}
}
