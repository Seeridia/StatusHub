package controlplane

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/Seeridia/StatusHub/internal/adapter/ecosystem"
	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
	"github.com/Seeridia/StatusHub/internal/adapter/vendorprofile"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"github.com/Seeridia/StatusHub/internal/transport"
	"github.com/google/uuid"
)

type sourceProbeError struct{ err error }

func (e *sourceProbeError) Error() string { return "controlplane: source probe failed" }
func (e *sourceProbeError) Unwrap() error { return e.err }

func sourceProbeProblem(err error) (int, string, string, bool) {
	var probeErr *sourceProbeError
	if !errors.As(err, &probeErr) {
		return 0, "", "", false
	}
	if kind, found := transport.KindOf(probeErr.err); found {
		switch kind {
		case transport.KindUnsafeTarget, transport.KindInvalidURL, transport.KindUnsupportedScheme:
			return 400, "unsafe_source_url", "The address must be a public HTTPS status page", true
		case transport.KindDNS:
			return 422, "source_dns_failure", "The status page hostname could not be resolved", true
		case transport.KindConnect:
			return 422, "source_connection_failure", "The status page refused or could not accept the connection", true
		case transport.KindTLS:
			return 422, "source_tls_failure", "The status page did not provide a valid TLS connection", true
		case transport.KindTimeout, transport.KindCanceled:
			return 504, "source_probe_timeout", "The status page did not respond before the probe timed out", true
		case transport.KindBodyTooLarge:
			return 422, "source_response_too_large", "The status page response is too large to inspect safely", true
		default:
			return 422, "source_probe_failed", "The status page could not be inspected", true
		}
	}
	if errors.Is(probeErr.err, statuspage.ErrNotStatuspage) || errors.Is(probeErr.err, ecosystem.ErrNotRecognized) {
		return 422, "unsupported_source", "No supported status-page API was detected at this address", true
	}
	return 500, "internal_error", "The request could not be completed", true
}

type sourceVendor struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
	New  bool   `json:"new"`
}

// Match only exact canonical URLs or a curated vendor profile. A hosting
// platform's domain is not proof that all pages belong to the same vendor.
func sourceURLKey(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	canonical, _, _, err := vendorprofile.Canonicalize(parsed)
	if err != nil {
		return raw
	}
	canonical.Host = strings.ToLower(canonical.Host)
	canonical.Path = strings.TrimRight(canonical.Path, "/")
	return canonical.String()
}

func (s *Server) identifySource(ctx context.Context, result *probeResponse) error {
	details, _ := ctx.Value(contextKeys{}).(requestContext)
	if source, err := s.repository.FindSourceByCanonicalURL(ctx, details.Tenant.ID, result.CanonicalURL); err == nil {
		result.ExistingSource = &source
		result.Vendor = &sourceVendor{ID: source.VendorID, Slug: source.VendorSlug, Name: source.VendorName}
		return nil
	} else if err != store.ErrNotFound {
		return err
	}
	parsed, _ := url.Parse(result.CanonicalURL)
	if profile, found := vendorprofile.Lookup(parsed.Hostname()); found {
		vendors, err := s.repository.ListVendorStatuses(ctx, details.Tenant.ID)
		if err != nil {
			return err
		}
		for _, vendor := range vendors {
			if vendor.Slug == profile.Slug {
				result.Vendor = &sourceVendor{ID: vendor.ID, Slug: vendor.Slug, Name: vendor.Name}
				return nil
			}
		}
	}
	// Preserve path identity for shared status hosting. The hostname is an
	// honest initial display name; it is not asserted to be a verified company.
	key := sourceURLKey(result.CanonicalURL)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("statushub/vendor/"+key)).String()
	result.Vendor = &sourceVendor{ID: id, Slug: "site-" + id, Name: parsed.Hostname() + strings.TrimRight(parsed.Path, "/"), New: true}
	return nil
}
