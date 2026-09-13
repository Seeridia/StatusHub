package domain

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestSourceJSONUsesURLStringsAndRoundTrips(t *testing.T) {
	t.Parallel()

	requested, err := url.Parse("https://status.example.com/input")
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := url.Parse("https://status.example.com")
	if err != nil {
		t.Fatal(err)
	}
	original := Source{
		ID:           "source-1",
		Provider:     "statuspage",
		PageID:       "page-1",
		Kind:         SourceKindStatusPage,
		RequestedURL: requested,
		CanonicalURL: canonical,
	}

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), `"Scheme"`) || !strings.Contains(string(encoded), `"requested_url":"https://status.example.com/input"`) {
		t.Fatalf("unexpected JSON: %s", encoded)
	}

	var decoded Source
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded.RequestedURL == nil || decoded.RequestedURL.String() != requested.String() {
		t.Fatalf("requested URL = %v", decoded.RequestedURL)
	}
	if decoded.CanonicalURL == nil || decoded.CanonicalURL.String() != canonical.String() {
		t.Fatalf("canonical URL = %v", decoded.CanonicalURL)
	}
}
