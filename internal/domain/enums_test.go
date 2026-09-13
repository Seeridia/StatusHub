package domain

import "testing"

func TestCanonicalEnums(t *testing.T) {
	t.Parallel()

	valid := []interface{ Valid() bool }{
		SourceKindStatusPage,
		ResourceSummary,
		CompletenessComplete,
		PaginationCursor,
		ComponentStatusUnknown,
		IncidentPhaseResolved,
		ImpactCritical,
		IncidentKindMaintenance,
		EntityComponent,
		EventKindSourceDegraded,
	}
	for _, value := range valid {
		if !value.Valid() {
			t.Fatalf("expected %#v to be valid", value)
		}
	}

	invalid := []interface{ Valid() bool }{
		SourceKind("other"),
		ResourceKind("other"),
		Completeness("full"),
		PaginationKind("offset"),
		ComponentStatus("green"),
		IncidentPhase("closed"),
		Impact("severe"),
		IncidentKind("other"),
		EntityKind("other"),
		EventKind("incident.changed"),
	}
	for _, value := range invalid {
		if value.Valid() {
			t.Fatalf("expected %#v to be invalid", value)
		}
	}
}

func TestAbsenceInferenceRequiresCompleteAndAuthoritative(t *testing.T) {
	t.Parallel()

	capability := EndpointCapability{
		Completeness:     CompletenessComplete,
		AuthoritativeFor: []ResourceKind{ResourceComponents},
	}
	if !capability.CanInferAbsence(ResourceComponents) {
		t.Fatal("complete authoritative endpoint should allow absence inference")
	}
	if capability.CanInferAbsence(ResourceIncidents) {
		t.Fatal("endpoint must not infer absence outside its authority")
	}

	capability.Completeness = CompletenessPartial
	if capability.CanInferAbsence(ResourceComponents) {
		t.Fatal("partial endpoint must not infer absence")
	}

	snapshot := Snapshot{
		Completeness:     CompletenessComplete,
		AuthoritativeFor: []ResourceKind{ResourceIncidents},
	}
	if !snapshot.CanInferAbsence(ResourceIncidents) {
		t.Fatal("complete authoritative snapshot should allow absence inference")
	}
}
