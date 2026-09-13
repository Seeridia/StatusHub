package reconcile

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

var testEpoch = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func TestFirstSnapshotEstablishesBaselineWithoutHistoricalEvents(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(testEpoch)
	snapshot.Components = []domain.Component{
		{ID: "database", Name: "Database", Status: domain.ComponentStatus("future-status"), RawStatus: "future-status"},
	}
	snapshot.Incidents = []domain.Incident{testIncident("incident-old", domain.IncidentPhaseMonitoring, "still investigating")}

	result, err := Apply(State{}, snapshot)
	if err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	if !result.BaselineEstablished || !result.State.BaselineEstablished || !result.Changed {
		t.Fatalf("baseline flags = %#v", result)
	}
	if len(result.Events) != 0 {
		t.Fatalf("baseline emitted historical events: %#v", result.Events)
	}
	component := result.State.Components["database"]
	if component.Revision != 1 || component.Component.Status != domain.ComponentStatusUnknown {
		t.Fatalf("component state = %#v", component)
	}
	if component.Component.RawStatus != "future-status" {
		t.Fatalf("raw status was not retained: %#v", component.Component)
	}
	incident := result.State.Incidents["incident-old"]
	if incident.Revision != 1 || incident.Incident.Phase != domain.IncidentPhaseMonitoring {
		t.Fatalf("incident state = %#v", incident)
	}

	// Apply must not retain aliases into its input.
	snapshot.Components[0].Tags = map[string]string{"mutated": "later"}
	if result.State.Components["database"].Component.Tags["mutated"] != "" {
		t.Fatal("state aliases snapshot component tags")
	}
}

func TestComponentStatusChangeIsMonotonicAndReplayIdempotent(t *testing.T) {
	t.Parallel()

	baseline := testSnapshot(testEpoch)
	baseline.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusOperational, RawStatus: "operational",
	}}
	first := mustApply(t, State{}, baseline)

	changed := testSnapshot(testEpoch.Add(time.Minute))
	changed.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusMajorOutage, RawStatus: "major_outage",
	}}
	second := mustApply(t, first.State, changed)
	if len(second.Events) != 1 {
		t.Fatalf("events = %#v", second.Events)
	}
	event := second.Events[0]
	if event.Kind != domain.EventKindComponentStatusChanged || event.EntityID != "api" || event.AggregateRevision != 2 {
		t.Fatalf("event = %#v", event)
	}
	if event.SourceEventKey == "" || !json.Valid(event.Payload) {
		t.Fatalf("event identity/payload = %#v", event)
	}
	if got := second.State.Components["api"]; got.Revision != 2 || got.Component.Status != domain.ComponentStatusMajorOutage {
		t.Fatalf("aggregate = %#v", got)
	}

	replayed := mustApply(t, second.State, changed)
	if !replayed.Duplicate || replayed.Changed || len(replayed.Events) != 0 {
		t.Fatalf("replay result = %#v", replayed)
	}
	if got := replayed.State.Components["api"].Revision; got != 2 {
		t.Fatalf("revision after replay = %d", got)
	}

	// A newer observation with identical semantics refreshes evidence without
	// creating a logical event or advancing the aggregate revision.
	unchanged := changed
	unchanged.ObservedAt = changed.ObservedAt.Add(time.Minute)
	third := mustApply(t, replayed.State, unchanged)
	if third.Duplicate || third.Changed || len(third.Events) != 0 {
		t.Fatalf("unchanged observation = %#v", third)
	}
	if got := third.State.Components["api"].Revision; got != 2 {
		t.Fatalf("revision after unchanged observation = %d", got)
	}
}

func TestIncidentCreatedUpdatedAndExplicitlyResolved(t *testing.T) {
	t.Parallel()

	baseline := mustApply(t, State{}, testSnapshot(testEpoch))
	createdSnapshot := testSnapshot(testEpoch.Add(time.Minute))
	createdSnapshot.Incidents = []domain.Incident{testIncident("incident-1", domain.IncidentPhaseInvestigating, "initial")}
	created := mustApply(t, baseline.State, createdSnapshot)
	assertSingleIncidentEvent(t, created, domain.EventKindIncidentCreated, 1)

	updatedSnapshot := testSnapshot(testEpoch.Add(2 * time.Minute))
	updatedSnapshot.Incidents = []domain.Incident{testIncident("incident-1", domain.IncidentPhaseMonitoring, "mitigated")}
	updated := mustApply(t, created.State, updatedSnapshot)
	assertSingleIncidentEvent(t, updated, domain.EventKindIncidentUpdated, 2)

	resolvedAt := testEpoch.Add(3 * time.Minute)
	resolvedIncident := testIncident("incident-1", domain.IncidentPhaseResolved, "resolved")
	resolvedIncident.ResolvedAt = &resolvedAt
	resolvedSnapshot := testSnapshot(resolvedAt)
	resolvedSnapshot.Incidents = []domain.Incident{resolvedIncident}
	resolved := mustApply(t, updated.State, resolvedSnapshot)
	assertSingleIncidentEvent(t, resolved, domain.EventKindIncidentResolved, 3)

	replayed := mustApply(t, resolved.State, resolvedSnapshot)
	if !replayed.Duplicate || len(replayed.Events) != 0 || replayed.State.Incidents["incident-1"].Revision != 3 {
		t.Fatalf("resolved replay = %#v", replayed)
	}
}

func TestMissingIncidentRequiresTwoConsecutiveCompleteAuthoritativeSnapshots(t *testing.T) {
	t.Parallel()

	baselineSnapshot := testSnapshot(testEpoch)
	baselineSnapshot.Incidents = []domain.Incident{testIncident("incident-1", domain.IncidentPhaseMonitoring, "active")}
	baseline := mustApply(t, State{}, baselineSnapshot)

	partial := testSnapshot(testEpoch.Add(time.Minute))
	partial.Completeness = domain.CompletenessPartial
	partial.AuthoritativeFor = []domain.ResourceKind{domain.ResourceUnresolvedIncidents}
	partialResult := mustApply(t, baseline.State, partial)
	assertIncidentUnresolved(t, partialResult.State, 0, 1)

	nonAuthoritative := testSnapshot(testEpoch.Add(2 * time.Minute))
	nonAuthoritative.AuthoritativeFor = nil
	nonAuthoritativeResult := mustApply(t, partialResult.State, nonAuthoritative)
	assertIncidentUnresolved(t, nonAuthoritativeResult.State, 0, 1)

	missingOnceSnapshot := testSnapshot(testEpoch.Add(3 * time.Minute))
	missingOnce := mustApply(t, nonAuthoritativeResult.State, missingOnceSnapshot)
	if len(missingOnce.Events) != 0 {
		t.Fatalf("first miss emitted events: %#v", missingOnce.Events)
	}
	assertIncidentUnresolved(t, missingOnce.State, 1, 1)

	// Re-delivery of the same poll must not impersonate a second poll.
	replayedFirstMiss := mustApply(t, missingOnce.State, missingOnceSnapshot)
	if !replayedFirstMiss.Duplicate {
		t.Fatal("first missing observation replay was not detected")
	}
	assertIncidentUnresolved(t, replayedFirstMiss.State, 1, 1)

	missingTwiceSnapshot := testSnapshot(testEpoch.Add(4 * time.Minute))
	missingTwice := mustApply(t, replayedFirstMiss.State, missingTwiceSnapshot)
	assertSingleIncidentEvent(t, missingTwice, domain.EventKindIncidentResolved, 2)
	resolved := missingTwice.State.Incidents["incident-1"]
	if resolved.MissingCount != 2 || resolved.Incident.ResolvedAt == nil || !resolved.Incident.ResolvedAt.Equal(missingTwiceSnapshot.ObservedAt) {
		t.Fatalf("inferred resolution = %#v", resolved)
	}
	var payload struct {
		ResolutionReason string `json:"resolution_reason"`
	}
	if err := json.Unmarshal(missingTwice.Events[0].Payload, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload.ResolutionReason != "missing_from_two_consecutive_authoritative_snapshots" {
		t.Fatalf("resolution reason = %q", payload.ResolutionReason)
	}

	replayedSecondMiss := mustApply(t, missingTwice.State, missingTwiceSnapshot)
	if !replayedSecondMiss.Duplicate || len(replayedSecondMiss.Events) != 0 || replayedSecondMiss.State.Incidents["incident-1"].Revision != 2 {
		t.Fatalf("second missing replay = %#v", replayedSecondMiss)
	}
}

func TestPresenceResetsMissingConfirmation(t *testing.T) {
	t.Parallel()

	baselineSnapshot := testSnapshot(testEpoch)
	baselineSnapshot.Incidents = []domain.Incident{testIncident("incident-1", domain.IncidentPhaseMonitoring, "active")}
	state := mustApply(t, State{}, baselineSnapshot).State

	missing := testSnapshot(testEpoch.Add(time.Minute))
	state = mustApply(t, state, missing).State
	assertIncidentUnresolved(t, state, 1, 1)

	present := testSnapshot(testEpoch.Add(2 * time.Minute))
	present.Incidents = []domain.Incident{testIncident("incident-1", domain.IncidentPhaseMonitoring, "active")}
	presentResult := mustApply(t, state, present)
	assertIncidentUnresolved(t, presentResult.State, 0, 1)

	missingAgain := testSnapshot(testEpoch.Add(3 * time.Minute))
	result := mustApply(t, presentResult.State, missingAgain)
	if len(result.Events) != 0 {
		t.Fatalf("non-consecutive miss resolved incident: %#v", result.Events)
	}
	assertIncidentUnresolved(t, result.State, 1, 1)
}

func TestOlderSnapshotCannotOverwriteNewerAggregate(t *testing.T) {
	t.Parallel()

	oldSourceTime := testEpoch
	baselineSnapshot := testSnapshot(testEpoch)
	baselineSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusOperational, RawStatus: "operational", SourceUpdatedAt: &oldSourceTime,
	}}
	state := mustApply(t, State{}, baselineSnapshot).State

	newSourceTime := testEpoch.Add(2 * time.Minute)
	newSnapshot := testSnapshot(testEpoch.Add(2 * time.Minute))
	newSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusMajorOutage, RawStatus: "major_outage", SourceUpdatedAt: &newSourceTime,
	}}
	newResult := mustApply(t, state, newSnapshot)
	if newResult.State.Components["api"].Revision != 2 {
		t.Fatalf("new revision = %d", newResult.State.Components["api"].Revision)
	}

	lateSnapshot := testSnapshot(testEpoch.Add(3 * time.Minute))
	lateSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusOperational, RawStatus: "operational", SourceUpdatedAt: &oldSourceTime,
	}}
	late := mustApply(t, newResult.State, lateSnapshot)
	if len(late.Events) != 0 || late.State.Components["api"].Revision != 2 || late.State.Components["api"].Component.Status != domain.ComponentStatusMajorOutage {
		t.Fatalf("late snapshot regressed aggregate: %#v", late)
	}

	// Same upstream timestamp with a later observation is a legitimate changed
	// revision (some providers reuse timestamps).
	sameTimestamp := testSnapshot(testEpoch.Add(4 * time.Minute))
	sameTimestamp.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusDegraded, RawStatus: "degraded_performance", SourceUpdatedAt: &newSourceTime,
	}}
	sameTimeResult := mustApply(t, late.State, sameTimestamp)
	if len(sameTimeResult.Events) != 1 || sameTimeResult.State.Components["api"].Revision != 3 {
		t.Fatalf("same timestamp changed payload = %#v", sameTimeResult)
	}
}

func TestUnknownComponentStateNeverBecomesOperational(t *testing.T) {
	t.Parallel()

	baselineSnapshot := testSnapshot(testEpoch)
	baselineSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusDegraded, RawStatus: "degraded_performance",
	}}
	state := mustApply(t, State{}, baselineSnapshot).State

	unknownSnapshot := testSnapshot(testEpoch.Add(time.Minute))
	unknownSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatus("new_vendor_enum"), RawStatus: "new_vendor_enum",
	}}
	result := mustApply(t, state, unknownSnapshot)
	if result.State.Components["api"].Component.Status != domain.ComponentStatusUnknown {
		t.Fatalf("unknown mapped to %q", result.State.Components["api"].Component.Status)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != domain.EventKindComponentStatusChanged {
		t.Fatalf("unknown transition event = %#v", result.Events)
	}
}

func TestApplyDoesNotMutatePreviousOnErrorOrSuccess(t *testing.T) {
	t.Parallel()

	baselineSnapshot := testSnapshot(testEpoch)
	baselineSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusOperational, Tags: map[string]string{"tier": "edge"},
	}}
	previous := mustApply(t, State{}, baselineSnapshot).State
	want := cloneState(previous)

	nextSnapshot := testSnapshot(testEpoch.Add(time.Minute))
	nextSnapshot.Components = []domain.Component{{
		ID: "api", Name: "API", Status: domain.ComponentStatusDegraded, Tags: map[string]string{"tier": "core"},
	}}
	if _, err := Apply(previous, nextSnapshot); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !reflect.DeepEqual(previous, want) {
		t.Fatalf("previous state mutated\n got: %#v\nwant: %#v", previous, want)
	}

	invalid := nextSnapshot
	invalid.Source.ID = "other-source"
	if _, err := Apply(previous, invalid); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("source mismatch error = %v", err)
	}
	if !reflect.DeepEqual(previous, want) {
		t.Fatal("previous state mutated on error")
	}
}

func TestDuplicateEntityIDIsRejected(t *testing.T) {
	t.Parallel()

	snapshot := testSnapshot(testEpoch)
	snapshot.Incidents = []domain.Incident{
		testIncident("duplicate", domain.IncidentPhaseMonitoring, "one"),
		testIncident("duplicate", domain.IncidentPhaseMonitoring, "two"),
	}
	if _, err := Apply(State{}, snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("duplicate incident error = %v", err)
	}
}

func testSnapshot(observedAt time.Time) domain.Snapshot {
	return domain.Snapshot{
		Source: domain.Source{
			ID:       "source-1",
			Provider: "atlassian-statuspage",
			PageID:   "page-1",
			Kind:     domain.SourceKindStatusPage,
		},
		ResourceKind:      domain.ResourceSummary,
		OverallStatus:     domain.ComponentStatusOperational,
		ComputedStatus:    domain.ComponentStatusOperational,
		Completeness:      domain.CompletenessComplete,
		AuthoritativeFor:  []domain.ResourceKind{domain.ResourceComponents, domain.ResourceUnresolvedIncidents},
		ObservedAt:        observedAt,
		SchemaVersion:     "v1",
		AdapterVersion:    "adapter/v1",
		NormalizerVersion: "normalizer/v1",
	}
}

func testIncident(id string, phase domain.IncidentPhase, body string) domain.Incident {
	return domain.Incident{
		ID:        id,
		Kind:      domain.IncidentKindIncident,
		Name:      "API disruption",
		Phase:     phase,
		RawPhase:  string(phase),
		Impact:    domain.ImpactMajor,
		RawImpact: "major",
		Updates: []domain.IncidentUpdate{{
			ID:         "update-" + body,
			IncidentID: id,
			Body:       body,
			Phase:      phase,
			RawPhase:   string(phase),
			Impact:     domain.ImpactMajor,
			RawImpact:  "major",
		}},
	}
}

func mustApply(t *testing.T, state State, snapshot domain.Snapshot) Result {
	t.Helper()
	result, err := Apply(state, snapshot)
	if err != nil {
		t.Fatalf("apply snapshot at %v: %v", snapshot.ObservedAt, err)
	}
	return result
}

func assertSingleIncidentEvent(t *testing.T, result Result, kind domain.EventKind, revision uint64) {
	t.Helper()
	if len(result.Events) != 1 {
		t.Fatalf("event count = %d, events = %#v", len(result.Events), result.Events)
	}
	event := result.Events[0]
	if event.Kind != kind || event.EntityKind != domain.EntityIncident || event.EntityID != "incident-1" || event.AggregateRevision != revision {
		t.Fatalf("event = %#v", event)
	}
	if event.SourceEventKey == "" || !json.Valid(event.Payload) {
		t.Fatalf("event identity/payload = %#v", event)
	}
}

func assertIncidentUnresolved(t *testing.T, state State, missing uint8, revision uint64) {
	t.Helper()
	incident := state.Incidents["incident-1"]
	if incident.Incident.Phase == domain.IncidentPhaseResolved || incident.MissingCount != missing || incident.Revision != revision {
		t.Fatalf("incident = %#v", incident)
	}
}
