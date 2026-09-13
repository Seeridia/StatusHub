package domain

import (
	"net/url"
	"time"
)

// Source identifies one upstream status source. RequestedURL preserves user
// input while CanonicalURL identifies aliases that resolve to the same source.
type Source struct {
	ID           string     `json:"id"`
	Provider     string     `json:"provider"`
	PageID       string     `json:"page_id,omitempty"`
	Kind         SourceKind `json:"kind"`
	RequestedURL *url.URL   `json:"requested_url,omitempty"`
	CanonicalURL *url.URL   `json:"canonical_url,omitempty"`
}

// Target is the bounded input to adapter capability probing.
type Target struct {
	URL      *url.URL `json:"url"`
	SourceID string   `json:"source_id,omitempty"`
	Provider string   `json:"provider,omitempty"`
	PageID   string   `json:"page_id,omitempty"`
}

type CacheCapability struct {
	SupportsETag         bool          `json:"supports_etag"`
	SupportsLastModified bool          `json:"supports_last_modified"`
	SupportsCacheControl bool          `json:"supports_cache_control"`
	DefaultTTL           time.Duration `json:"default_ttl,omitempty"`
}

type PaginationCapability struct {
	Kind                        PaginationKind `json:"kind"`
	DefaultPageSize             int            `json:"default_page_size,omitempty"`
	MaximumPageSize             int            `json:"maximum_page_size,omitempty"`
	MaximumPages                int            `json:"maximum_pages,omitempty"`
	CompleteOnlyAfterExhaustion bool           `json:"complete_only_after_exhaustion"`
}

// EndpointCapability deliberately carries correctness properties at endpoint
// scope. A source must not inherit completeness or authority from another
// endpoint merely because both are implemented by the same engine.
type EndpointCapability struct {
	Resource          ResourceKind         `json:"resource"`
	Path              string               `json:"path"`
	Completeness      Completeness         `json:"completeness"`
	AuthoritativeFor  []ResourceKind       `json:"authoritative_for,omitempty"`
	Cache             CacheCapability      `json:"cache"`
	Pagination        PaginationCapability `json:"pagination"`
	Authentication    string               `json:"authentication,omitempty"`
	HistoryWindow     time.Duration        `json:"history_window,omitempty"`
	MaximumBodyBytes  int64                `json:"maximum_body_bytes,omitempty"`
	ExpectedMediaType string               `json:"expected_media_type,omitempty"`
}

func (c EndpointCapability) IsAuthoritativeFor(kind ResourceKind) bool {
	for _, authoritativeKind := range c.AuthoritativeFor {
		if authoritativeKind == kind {
			return true
		}
	}
	return false
}

// CanInferAbsence is intentionally strict: a complete response alone is not
// enough unless the endpoint is also authoritative for the resource.
func (c EndpointCapability) CanInferAbsence(kind ResourceKind) bool {
	return c.Completeness == CompletenessComplete && c.IsAuthoritativeFor(kind)
}

type WebhookCapability struct {
	Supported        bool           `json:"supported"`
	SignatureScheme  string         `json:"signature_scheme,omitempty"`
	TriggerOnly      bool           `json:"trigger_only"`
	AuthoritativeFor []ResourceKind `json:"authoritative_for,omitempty"`
	MaximumBodyBytes int64          `json:"maximum_body_bytes,omitempty"`
}

// Capabilities are persisted probe results. SupportsETag,
// SupportsModified and SnapshotCompleteness summarize legacy/source-wide
// behavior; scheduling and reconciliation must use the endpoint properties.
type Capabilities struct {
	Engine               string                              `json:"engine"`
	Version              string                              `json:"version"`
	Confidence           float64                             `json:"confidence"`
	Endpoints            map[ResourceKind]EndpointCapability `json:"endpoints"`
	Webhook              WebhookCapability                   `json:"webhook"`
	SupportsETag         bool                                `json:"supports_etag"`
	SupportsModified     bool                                `json:"supports_modified"`
	SnapshotCompleteness Completeness                        `json:"snapshot_completeness"`
	SchemaHash           string                              `json:"schema_hash,omitempty"`
	ExpiresAt            time.Time                           `json:"expires_at"`
}

// SourceCapabilities is the explicit source-scoped name for Capabilities.
// Capabilities remains the short name used by the adapter interface.
type SourceCapabilities = Capabilities
