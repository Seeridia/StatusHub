package domain

import (
	"encoding/json"
	"testing"
)

func TestSemanticHashCanonicalizesKeyOrderAndTimeZone(t *testing.T) {
	t.Parallel()

	first := json.RawMessage(`{
		"updated_at":"2026-09-10T16:30:00+08:00",
		"nested":{"z":2,"a":1},
		"phase":"monitoring"
	}`)
	second := json.RawMessage(`{"phase":"monitoring","nested":{"a":1,"z":2},"updated_at":"2026-09-10T08:30:00Z"}`)

	firstHash, err := SemanticHash("statuspage/v1", first)
	if err != nil {
		t.Fatalf("hash first projection: %v", err)
	}
	secondHash, err := SemanticHash("statuspage/v1", second)
	if err != nil {
		t.Fatalf("hash second projection: %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("equivalent projections differ: %s != %s", firstHash, secondHash)
	}
	if len(firstHash) != 64 {
		t.Fatalf("expected SHA-256 hex digest, got %q", firstHash)
	}
}

func TestSemanticHashIncludesNormalizerVersion(t *testing.T) {
	t.Parallel()

	projection := map[string]any{"phase": "resolved", "impact": "minor"}
	first, err := SemanticHash("normalizer/v1", projection)
	if err != nil {
		t.Fatalf("hash v1: %v", err)
	}
	second, err := SemanticHash("normalizer/v2", projection)
	if err != nil {
		t.Fatalf("hash v2: %v", err)
	}
	if first == second {
		t.Fatal("normalizer version must alter semantic hash")
	}
}

func TestSemanticHashRejectsMissingVersionAndInvalidProjection(t *testing.T) {
	t.Parallel()

	if _, err := SemanticHash("", map[string]string{"status": "ok"}); err == nil {
		t.Fatal("expected missing normalizer version to fail")
	}
	if _, err := SemanticHash("v1", func() {}); err == nil {
		t.Fatal("expected non-JSON projection to fail")
	}
}

func TestCanonicalJSONProducesStableProjection(t *testing.T) {
	t.Parallel()

	canonical, err := CanonicalJSON(json.RawMessage(`{"z":0,"at":"2026-09-10T16:30:00+08:00","a":1}`))
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	const want = `{"a":1,"at":"2026-09-10T08:30:00Z","z":0}`
	if string(canonical) != want {
		t.Fatalf("canonical projection = %s, want %s", canonical, want)
	}
}
