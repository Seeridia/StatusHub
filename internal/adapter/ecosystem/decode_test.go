package ecosystem

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type goldenProjection struct {
	Overall    domain.ComponentStatus `json:"overall"`
	Components int                    `json:"components"`
	Incidents  int                    `json:"incidents"`
	Synthetic  bool                   `json:"synthetic"`
}

func TestEngineGoldenFixtures(t *testing.T) {
	goldenData := readFixture(t, "ecosystem.golden.json")
	golden := make(map[string]goldenProjection)
	if err := json.Unmarshal(goldenData, &golden); err != nil {
		t.Fatal(err)
	}
	recipe, err := compileHTMLRecipe(testHTMLRecipe())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		engine   string
		fixture  string
		resource domain.ResourceKind
		decode   decoder
	}{
		{"incidentio", EngineIncidentIO, "incidentio.json", domain.ResourceSummary, decodeIncidentIO},
		{"instatus", EngineInstatus, "instatus.json", domain.ResourceSummary, decodeInstatusSummary},
		{"betterstack", EngineBetterStack, "betterstack.json", domain.ResourceSummary, decodeBetterStack},
		{"statusio", EngineStatusIO, "statusio.json", domain.ResourceSummary, decodeStatusIO},
		{"cachet-v2", EngineCachet, "cachet.json", domain.ResourceComponents, decodeCachetComponents},
		{"cachet-v3", EngineCachet, "cachet-v3.json", domain.ResourceComponents, decodeCachetComponents},
		{"gatus", EngineGatus, "gatus.json", domain.ResourceSummary, decodeGatus},
		{"cstate", EngineCState, "cstate.json", domain.ResourceSummary, decodeCState},
		{"html-recipe", EngineHTMLRecipe, "status.html", domain.ResourceSummary, recipe.decode},
	}
	observedAt := time.Date(2026, 9, 10, 1, 0, 0, 0, time.UTC)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := test.decode(readFixture(t, test.fixture), domain.Source{ID: "source"}, observedAt, test.resource)
			if err != nil {
				t.Fatal(err)
			}
			actual := goldenProjection{Overall: snapshot.OverallStatus, Components: len(snapshot.Components), Incidents: len(snapshot.Incidents)}
			for _, incident := range snapshot.Incidents {
				actual.Synthetic = actual.Synthetic || incident.Synthetic
			}
			if actual != golden[test.engine] {
				t.Fatalf("projection=%#v want=%#v", actual, golden[test.engine])
			}
			if snapshot.Source.Provider != test.engine || snapshot.ObservedAt != observedAt || snapshot.AdapterVersion == "" {
				t.Fatalf("snapshot metadata=%#v", snapshot)
			}
		})
	}
}

func TestJSONDecodersRejectMissingEngineMarker(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		remove   string
		resource domain.ResourceKind
		decode   decoder
	}{
		{"incidentio", "incidentio.json", "ongoing_incidents", domain.ResourceSummary, decodeIncidentIO},
		{"instatus", "instatus.json", "page", domain.ResourceSummary, decodeInstatusSummary},
		{"betterstack", "betterstack.json", "data", domain.ResourceSummary, decodeBetterStack},
		{"statusio", "statusio.json", "result", domain.ResourceSummary, decodeStatusIO},
		{"cachet", "cachet.json", "data", domain.ResourceComponents, decodeCachetComponents},
		{"cstate", "cstate.json", "systems", domain.ResourceSummary, decodeCState},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]json.RawMessage
			if err := json.Unmarshal(readFixture(t, test.fixture), &value); err != nil {
				t.Fatal(err)
			}
			delete(value, test.remove)
			mutated, _ := json.Marshal(value)
			if _, err := test.decode(mutated, domain.Source{ID: "source"}, time.Now(), test.resource); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want ErrInvalid", err)
			}
		})
	}
}

func TestGatusRequiresRecentFailureQuorum(t *testing.T) {
	body := []byte(`[{"name":"API","group":"core","key":"core_api","results":[{"success":false,"timestamp":"2026-09-10T00:03:00Z"},{"success":true,"timestamp":"2026-09-10T00:02:00Z"},{"success":true,"timestamp":"2026-09-10T00:01:00Z"}]}]`)
	snapshot, err := decodeGatus(body, domain.Source{ID: "source"}, time.Now(), domain.ResourceSummary)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Incidents) != 0 || snapshot.Components[0].Status != domain.ComponentStatusOperational {
		t.Fatalf("single failure must be debounced: %#v", snapshot)
	}
}

func TestGatusRejectsObjectMutation(t *testing.T) {
	if _, err := decodeGatus([]byte(`{"endpoints":[]}`), domain.Source{ID: "source"}, time.Now(), domain.ResourceSummary); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error=%v, want ErrInvalid", err)
	}
}

func TestHTMLRecipeIsBoundedAndNeverAuthoritative(t *testing.T) {
	if _, err := compileHTMLRecipe(HTMLRecipe{Host: "example.test", Path: "/", ComponentSelector: "script:has("}); err == nil {
		t.Fatal("expected invalid selector rejection")
	}
	recipe, err := compileHTMLRecipe(testHTMLRecipe())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := recipe.decode(readFixture(t, "status.html"), domain.Source{ID: "source"}, time.Now(), domain.ResourceSummary)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Completeness != domain.CompletenessPartial || len(snapshot.AuthoritativeFor) != 0 {
		t.Fatalf("HTML fallback must not infer absence: %#v", snapshot)
	}
}

func FuzzEcosystemDecoders(f *testing.F) {
	seeds := []struct {
		engine  string
		fixture string
	}{
		{EngineIncidentIO, "incidentio.json"}, {EngineInstatus, "instatus.json"},
		{EngineBetterStack, "betterstack.json"}, {EngineStatusIO, "statusio.json"},
		{EngineCachet, "cachet.json"}, {EngineCachet, "cachet-v3.json"}, {EngineGatus, "gatus.json"}, {EngineCState, "cstate.json"},
	}
	for _, seed := range seeds {
		f.Add(seed.engine, readFixture(f, seed.fixture))
	}
	f.Fuzz(func(t *testing.T, engine string, body []byte) {
		var decode decoder
		resource := domain.ResourceSummary
		switch engine {
		case EngineIncidentIO:
			decode = decodeIncidentIO
		case EngineInstatus:
			decode = decodeInstatusSummary
		case EngineBetterStack:
			decode = decodeBetterStack
		case EngineStatusIO:
			decode = decodeStatusIO
		case EngineCachet:
			decode, resource = decodeCachetComponents, domain.ResourceComponents
		case EngineGatus:
			decode = decodeGatus
		case EngineCState:
			decode = decodeCState
		default:
			return
		}
		_, _ = decode(body, domain.Source{ID: "fuzz"}, time.Unix(1, 0), resource)
	})
}

func FuzzHTMLRecipeDecoder(f *testing.F) {
	recipe, err := compileHTMLRecipe(testHTMLRecipe())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(readFixture(f, "status.html"))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = recipe.decode(body, domain.Source{ID: "fuzz"}, time.Unix(1, 0), domain.ResourceSummary)
	})
}

func BenchmarkEcosystemDecoders(b *testing.B) {
	tests := []struct {
		name     string
		fixture  string
		resource domain.ResourceKind
		decode   decoder
	}{
		{"incidentio", "incidentio.json", domain.ResourceSummary, decodeIncidentIO},
		{"instatus", "instatus.json", domain.ResourceSummary, decodeInstatusSummary},
		{"betterstack", "betterstack.json", domain.ResourceSummary, decodeBetterStack},
		{"statusio", "statusio.json", domain.ResourceSummary, decodeStatusIO},
		{"cachet", "cachet.json", domain.ResourceComponents, decodeCachetComponents},
		{"gatus", "gatus.json", domain.ResourceSummary, decodeGatus},
		{"cstate", "cstate.json", domain.ResourceSummary, decodeCState},
	}
	for _, test := range tests {
		body := readFixture(b, test.fixture)
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if _, err := test.decode(body, domain.Source{ID: "benchmark"}, time.Unix(1, 0), test.resource); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func testHTMLRecipe() HTMLRecipe {
	return HTMLRecipe{Host: "status.example.test", Path: "/status", OverallStatusSelector: "#overall",
		ComponentSelector: ".component", ComponentNameSelector: ".name", ComponentStatusSelector: ".status",
		IncidentSelector: ".incident", IncidentTitleSelector: "h2", IncidentBodySelector: ".body", IncidentStatusSelector: ".state"}
}

type fixtureTesting interface {
	Helper()
	Fatal(...any)
}

func readFixture(t fixtureTesting, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
