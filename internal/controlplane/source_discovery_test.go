package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type discoveryRepository struct {
	repositoryStub
	sources       []store.SourceView
	createdSource store.CreateSourceParams
}

func (r *discoveryRepository) ListSources(context.Context, string, *store.TimeCursor, int) ([]store.SourceView, error) {
	return r.sources, nil
}
func (r *discoveryRepository) CreateSource(_ context.Context, p store.CreateSourceParams) (store.SourceView, error) {
	r.createdSource = p
	return store.SourceView{ID: p.ID, VendorID: p.VendorID}, nil
}

type discoveryProber struct{}

func (discoveryProber) Probe(context.Context, domain.Target) (domain.Capabilities, error) {
	return domain.Capabilities{Engine: "atlassian-statuspage", ExpiresAt: time.Now().Add(time.Hour), Endpoints: map[domain.ResourceKind]domain.EndpointCapability{domain.ResourceSummary: {Path: "/api/v2/summary.json"}}}, nil
}

func TestSourceURLOnlyDiscoveryAndCreation(t *testing.T) {
	const vendorID = "10000000-0000-0000-0000-000000000013"
	for _, tc := range []struct {
		name, url string
		existing  bool
		newVendor bool
	}{{"official", "https://status.openai.com/", false, false}, {"existing", "https://status.openai.com/", true, false}, {"unrecognized identity", "https://status.example.com/team-a", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := &discoveryRepository{repositoryStub: repositoryStub{vendors: []store.VendorStatus{{ID: vendorID, Slug: "openai", Name: "OpenAI"}}}}
			if tc.existing {
				r.sources = []store.SourceView{{ID: "existing", VendorID: vendorID, VendorName: "OpenAI", CanonicalURL: "https://status.openai.com"}}
			}
			s := newTestServer(t, r, verifierStub{tenantID: testTenantID}, nil)
			s.prober = discoveryProber{}
			req := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/sources", strings.NewReader(`{"url":"`+tc.url+`"}`))
			req.Header.Set("Authorization", "Bearer valid")
			req.Header.Set("Idempotency-Key", "source-test")
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, req)
			expected := http.StatusCreated
			if tc.existing {
				expected = http.StatusOK
			}
			if response.Code != expected {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if tc.existing {
				if r.createdSource.ID != "" {
					t.Fatal("duplicated existing source")
				}
				return
			}
			if tc.newVendor {
				if r.createdSource.AutoVendorName != "status.example.com/team-a" {
					t.Fatalf("identity %+v", r.createdSource)
				}
			} else if r.createdSource.VendorID != vendorID {
				t.Fatalf("wrong vendor %+v", r.createdSource)
			}
		})
	}
}
func TestSourceIdentityKeepsSharedHostingPathsSeparate(t *testing.T) {
	if sourceURLKey("https://status.example.com/a") == sourceURLKey("https://status.example.com/b") {
		t.Fatal("merged different hosted pages")
	}
	if sourceURLKey("https://status.anthropic.com/") != sourceURLKey("https://status.claude.com") {
		t.Fatal("official alias not normalized")
	}
}

func TestSourceDisplayName(t *testing.T) {
	for _, tc := range []struct {
		name, display, url, want string
		status                   int
	}{
		{"custom", "  My Service  ", "https://status.example.com", "My Service", http.StatusCreated},
		{"existing vendor", "Wrong Name", "https://status.openai.com", "", http.StatusCreated},
		{"too long", strings.Repeat("x", 121), "https://status.example.com", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &discoveryRepository{repositoryStub: repositoryStub{vendors: []store.VendorStatus{{ID: "10000000-0000-0000-0000-000000000013", Slug: "openai", Name: "OpenAI"}}}}
			s := newTestServer(t, r, verifierStub{tenantID: testTenantID}, nil)
			s.prober = discoveryProber{}
			req := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/sources", strings.NewReader(`{"url":"`+tc.url+`","display_name":"`+tc.display+`"}`))
			req.Header.Set("Authorization", "Bearer valid")
			req.Header.Set("Idempotency-Key", "display-test")
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if r.createdSource.AutoVendorName != tc.want {
				t.Fatalf("name %q, want %q", r.createdSource.AutoVendorName, tc.want)
			}
		})
	}
}
