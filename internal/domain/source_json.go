package domain

import (
	"encoding/json"
	"fmt"
	"net/url"
)

type sourceJSON struct {
	ID           string     `json:"id"`
	Provider     string     `json:"provider"`
	PageID       string     `json:"page_id,omitempty"`
	Kind         SourceKind `json:"kind"`
	RequestedURL string     `json:"requested_url,omitempty"`
	CanonicalURL string     `json:"canonical_url,omitempty"`
}

// MarshalJSON keeps URLs stable and readable at API and message boundaries.
// net/url.URL's default JSON representation exposes implementation fields.
func (s Source) MarshalJSON() ([]byte, error) {
	return json.Marshal(sourceJSON{
		ID:           s.ID,
		Provider:     s.Provider,
		PageID:       s.PageID,
		Kind:         s.Kind,
		RequestedURL: urlString(s.RequestedURL),
		CanonicalURL: urlString(s.CanonicalURL),
	})
}

// UnmarshalJSON is the inverse of MarshalJSON so persisted snapshots and
// compact bus messages can round-trip without a second wire type.
func (s *Source) UnmarshalJSON(data []byte) error {
	if s == nil {
		return fmt.Errorf("decode source: nil destination")
	}
	var wire sourceJSON
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode source: %w", err)
	}
	requestedURL, err := parseOptionalURL(wire.RequestedURL)
	if err != nil {
		return fmt.Errorf("decode source requested_url: %w", err)
	}
	canonicalURL, err := parseOptionalURL(wire.CanonicalURL)
	if err != nil {
		return fmt.Errorf("decode source canonical_url: %w", err)
	}
	*s = Source{
		ID:           wire.ID,
		Provider:     wire.Provider,
		PageID:       wire.PageID,
		Kind:         wire.Kind,
		RequestedURL: requestedURL,
		CanonicalURL: canonicalURL,
	}
	return nil
}

func urlString(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.String()
}

func parseOptionalURL(value string) (*url.URL, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	return parsed, nil
}
