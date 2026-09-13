package controlplane

import (
	"context"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter/vendorprofile"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

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
	var cursor *store.TimeCursor
	for {
		sources, err := s.repository.ListSources(ctx, details.Tenant.ID, cursor, 200)
		if err != nil {
			return err
		}
		for _, source := range sources {
			if sourceURLKey(source.CanonicalURL) == sourceURLKey(result.CanonicalURL) {
				result.ExistingSource = &source
				result.Vendor = &sourceVendor{ID: source.VendorID, Slug: source.VendorSlug, Name: source.VendorName}
				return nil
			}
		}
		if len(sources) < 200 {
			break
		}
		last := sources[len(sources)-1]
		cursor = &store.TimeCursor{Time: last.UpdatedAt, ID: last.ID}
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
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("statusmon/vendor/"+key)).String()
	result.Vendor = &sourceVendor{ID: id, Slug: "site-" + id, Name: parsed.Hostname() + strings.TrimRight(parsed.Path, "/"), New: true}
	return nil
}
