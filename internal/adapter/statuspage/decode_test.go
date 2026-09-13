package statuspage

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

const summaryFixture = `{
  "page": {
    "id": "page-1",
    "name": "Example Status",
    "url": "https://status.example.com",
    "time_zone": "Etc/UTC",
    "updated_at": "2026-09-10T01:02:03.456Z"
  },
  "status": {"indicator": "major", "description": "Partial System Outage"},
  "components": [
    {
      "id": "component-api",
      "name": "API",
      "status": "major_outage",
      "created_at": "2024-01-02T03:04:05Z",
      "updated_at": "2026-09-10T00:55:00Z",
      "description": "Public API",
      "group_id": null,
      "page_id": "page-1"
    },
    {
      "id": "component-edge",
      "name": "Edge",
      "status": "vendor_new_state",
      "created_at": "2024-01-02T03:04:05Z",
      "updated_at": "2026-09-10T00:56:00Z",
      "group_id": "component-network",
      "page_id": "page-1"
    }
  ],
  "incidents": [
    {
      "id": "incident-1",
      "name": "Elevated errors",
      "status": "verifying",
      "created_at": "2026-09-10T00:30:00Z",
      "updated_at": "2026-09-10T00:59:00Z",
      "monitoring_at": null,
      "resolved_at": null,
      "impact": "catastrophic",
      "shortlink": "https://stspg.io/example",
      "started_at": "2026-09-10T00:25:00Z",
      "page_id": "page-1",
      "components": [{"id": "component-api", "name": "API", "status": "major_outage"}],
      "incident_updates": [
        {
          "id": "update-1",
          "status": "verifying",
          "body": "Validating recovery",
          "incident_id": "incident-1",
          "created_at": "2026-09-10T00:58:00Z",
          "updated_at": "2026-09-10T00:59:00Z",
          "display_at": "2026-09-10T00:58:00Z",
          "affected_components": [
            {"code": "component-api", "name": "API", "old_status": "partial_outage", "new_status": "operational"},
            {"code": "component-edge", "name": "Edge", "old_status": "operational", "new_status": "vendor_new_state"}
          ],
          "deliver_notifications": true
        }
      ]
    }
  ],
  "scheduled_maintenances": []
}`

const unresolvedFixture = `{
  "page": {
    "id": "page-1",
    "name": "Example Status",
    "url": "https://status.example.com",
    "time_zone": "Etc/UTC",
    "updated_at": "2026-09-10T01:02:03Z"
  },
  "incidents": [
    {
      "id": "incident-2",
      "name": "API latency",
      "status": "monitoring",
      "created_at": "2026-09-10T00:30:00Z",
      "updated_at": "2026-09-10T01:00:00Z",
      "monitoring_at": "2026-09-10T01:00:00Z",
      "resolved_at": null,
      "impact": "minor",
      "shortlink": "https://stspg.io/example2",
      "started_at": "2026-09-10T00:25:00Z",
      "page_id": "page-1",
      "components": [],
      "incident_updates": []
    }
  ]
}`

func TestDecodeSummaryNormalizesAndPreservesRawValues(t *testing.T) {
	t.Parallel()

	requestedURL := mustParseURL(t, "https://requested.example/status")
	observedAt := time.Date(2026, 9, 10, 2, 3, 4, 0, time.UTC)
	snapshot, err := DecodeSummary([]byte(summaryFixture), domain.Source{
		ID:           "source-1",
		RequestedURL: requestedURL,
	}, observedAt)
	if err != nil {
		t.Fatalf("DecodeSummary() error = %v", err)
	}

	if snapshot.Source.Provider != Engine || snapshot.Source.Kind != domain.SourceKindStatusPage {
		t.Fatalf("source engine = %q, kind = %q", snapshot.Source.Provider, snapshot.Source.Kind)
	}
	if snapshot.Source.PageID != "page-1" {
		t.Fatalf("source page ID = %q", snapshot.Source.PageID)
	}
	if snapshot.Source.CanonicalURL == nil || snapshot.Source.CanonicalURL.String() != "https://status.example.com" {
		t.Fatalf("canonical URL = %v", snapshot.Source.CanonicalURL)
	}
	if snapshot.Source.RequestedURL != requestedURL {
		t.Fatal("requested URL was not preserved")
	}
	if snapshot.OverallStatus != domain.ComponentStatusPartialOutage || snapshot.RawOverallStatus != "major" {
		t.Fatalf("overall status = %q, raw = %q", snapshot.OverallStatus, snapshot.RawOverallStatus)
	}
	if snapshot.ComputedStatus != domain.ComponentStatusMajorOutage {
		t.Fatalf("computed status = %q", snapshot.ComputedStatus)
	}
	if snapshot.Completeness != domain.CompletenessComplete {
		t.Fatalf("completeness = %q", snapshot.Completeness)
	}
	for _, kind := range summaryAuthority() {
		if !snapshot.IsAuthoritativeFor(kind) {
			t.Errorf("snapshot is not authoritative for %q", kind)
		}
	}
	if snapshot.IsAuthoritativeFor(domain.ResourceIncidents) {
		t.Error("summary must not claim complete incident history")
	}
	if !snapshot.ObservedAt.Equal(observedAt) || snapshot.SourceUpdatedAt == nil {
		t.Fatalf("observation/source time = %v/%v", snapshot.ObservedAt, snapshot.SourceUpdatedAt)
	}

	if got := snapshot.Components[1]; got.Status != domain.ComponentStatusUnknown || got.RawStatus != "vendor_new_state" {
		t.Fatalf("unknown component status = %q, raw = %q", got.Status, got.RawStatus)
	}
	incident := snapshot.Incidents[0]
	if incident.Phase != domain.IncidentPhaseUnknown || incident.RawPhase != "verifying" {
		t.Fatalf("unknown phase = %q, raw = %q", incident.Phase, incident.RawPhase)
	}
	if incident.Impact != domain.ImpactUnknown || incident.RawImpact != "catastrophic" {
		t.Fatalf("unknown impact = %q, raw = %q", incident.Impact, incident.RawImpact)
	}
	if len(incident.ComponentIDs) != 2 || incident.ComponentIDs[0] != "component-api" || incident.ComponentIDs[1] != "component-edge" {
		t.Fatalf("component IDs = %#v", incident.ComponentIDs)
	}
	if len(incident.Updates) != 1 {
		t.Fatalf("updates = %#v", incident.Updates)
	}
	update := incident.Updates[0]
	if update.Phase != domain.IncidentPhaseUnknown || update.RawPhase != "verifying" {
		t.Fatalf("update phase = %q, raw = %q", update.Phase, update.RawPhase)
	}
	if update.Impact != domain.ImpactUnknown || update.RawImpact != "catastrophic" {
		t.Fatalf("update impact = %q, raw = %q", update.Impact, update.RawImpact)
	}
	if snapshot.SchemaVersion != Version || snapshot.AdapterVersion != AdapterVersion || snapshot.NormalizerVersion != NormalizerVersion {
		t.Fatalf("version metadata = %q/%q/%q", snapshot.SchemaVersion, snapshot.AdapterVersion, snapshot.NormalizerVersion)
	}
}

func TestDecodeSummaryUnknownComponentIsNotOperational(t *testing.T) {
	t.Parallel()

	body := `{
	  "page":{"id":"p","name":"P","url":"https://status.example.com","updated_at":"2026-09-10T00:00:00Z"},
	  "status":{"indicator":"new_indicator","description":"new"},
	  "components":[
	    {"id":"ok","name":"OK","status":"operational"},
	    {"id":"new","name":"New","status":"new_component_state"}
	  ],
	  "incidents":[]
	}`
	snapshot, err := DecodeSummary([]byte(body), domain.Source{}, time.Time{})
	if err != nil {
		t.Fatalf("DecodeSummary() error = %v", err)
	}
	if snapshot.OverallStatus != domain.ComponentStatusUnknown || snapshot.RawOverallStatus != "new_indicator" {
		t.Fatalf("overall = %q, raw = %q", snapshot.OverallStatus, snapshot.RawOverallStatus)
	}
	if snapshot.ComputedStatus != domain.ComponentStatusUnknown {
		t.Fatalf("computed status = %q; unknown must not become operational", snapshot.ComputedStatus)
	}
}

func TestDecodeIncidentCollectionCompleteness(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 9, 10, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		decode        func([]byte, domain.Source, time.Time) (domain.Snapshot, error)
		resource      domain.ResourceKind
		completeness  domain.Completeness
		authoritative bool
	}{
		{
			name:         "bounded incident history",
			decode:       DecodeIncidents,
			resource:     domain.ResourceIncidents,
			completeness: domain.CompletenessPartial,
		},
		{
			name:          "unresolved collection",
			decode:        DecodeUnresolvedIncidents,
			resource:      domain.ResourceUnresolvedIncidents,
			completeness:  domain.CompletenessComplete,
			authoritative: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := test.decode([]byte(unresolvedFixture), domain.Source{ID: "source-1"}, observedAt)
			if err != nil {
				t.Fatalf("decode() error = %v", err)
			}
			if snapshot.ResourceKind != test.resource || snapshot.Completeness != test.completeness {
				t.Fatalf("resource/completeness = %q/%q", snapshot.ResourceKind, snapshot.Completeness)
			}
			if snapshot.IsAuthoritativeFor(test.resource) != test.authoritative {
				t.Fatalf("authoritative = %t", snapshot.IsAuthoritativeFor(test.resource))
			}
			if len(snapshot.Incidents) != 1 || snapshot.Incidents[0].Phase != domain.IncidentPhaseMonitoring {
				t.Fatalf("incidents = %#v", snapshot.Incidents)
			}
		})
	}
}

func TestDecodeSummaryAllowsIncidentIOCompatibilityWithoutIncidents(t *testing.T) {
	body := []byte(`{
        "page":{"id":"page-openai","name":"OpenAI","url":"https://status.openai.com","updated_at":"2026-09-10T00:00:00Z"},
        "status":{"indicator":"none","description":"All Systems Operational"},
        "components":[]
    }`)
	snapshot, err := DecodeSummary(body, domain.Source{ID: "source-openai"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.IsAuthoritativeFor(domain.ResourceUnresolvedIncidents) {
		t.Fatal("summary without incidents must not infer incident absence")
	}
	if !snapshot.IsAuthoritativeFor(domain.ResourceComponents) {
		t.Fatal("summary must remain authoritative for components")
	}
}

func TestDecodeRejectsUnrecognizedOrMalformedPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "not JSON", body: `<html>status</html>`},
		{name: "wrong JSON API", body: `{"data":[]}`},
		{name: "null components", body: `{"page":{"id":"p"},"status":{"indicator":"none"},"components":null,"incidents":[]}`},
		{name: "missing page ID", body: `{"page":{},"status":{"indicator":"none"},"components":[],"incidents":[]}`},
		{name: "bad timestamp", body: `{"page":{"id":"p","updated_at":"yesterday"},"status":{"indicator":"none"},"components":[],"incidents":[]}`},
		{name: "trailing value", body: `{"page":{"id":"p"},"status":{"indicator":"none"},"components":[],"incidents":[]} {}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeSummary([]byte(test.body), domain.Source{}, time.Time{})
			if !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("DecodeSummary() error = %v, want ErrInvalidPayload", err)
			}
		})
	}
}

func TestSchemaHashIgnoresValuesAndArrayOrder(t *testing.T) {
	t.Parallel()

	first := []byte(`{"page":{"id":"one"},"incidents":[{"id":"a"},{"id":"b"}]}`)
	second := []byte(`{"incidents":[{"id":"different"}],"page":{"id":"two"}}`)
	firstHash, err := schemaHash(first)
	if err != nil {
		t.Fatalf("schemaHash(first) error = %v", err)
	}
	secondHash, err := schemaHash(second)
	if err != nil {
		t.Fatalf("schemaHash(second) error = %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("hash changed with values/cardinality: %q != %q", firstHash, secondHash)
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}
