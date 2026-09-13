package controlplane

// Opt-in browser fixture: exercises the real HTTP handler and production bundle
// with isolated in-memory records. It never connects to a database or notifier.
import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/audit"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type browserRepository struct{ repositoryStub }

func (r *browserRepository) ResolveTenant(ctx context.Context, key string) (store.Tenant, error) {
	tenant, err := r.repositoryStub.ResolveTenant(ctx, key)
	tenant.Name = "浏览器验收（测试数据）"
	return tenant, err
}
func (r *browserRepository) ListIncidents(context.Context, string, string, string, *time.Time, *store.TimeCursor, int) ([]store.IncidentView, error) {
	return []store.IncidentView{}, nil
}
func (r *browserRepository) ListEndpoints(context.Context, string, *store.TimeCursor, int) ([]store.EndpointView, error) {
	return []store.EndpointView{}, nil
}
func (r *browserRepository) ListSubscriptions(context.Context, string, *store.TimeCursor, int) ([]store.SubscriptionView, error) {
	return []store.SubscriptionView{}, nil
}
func (r *browserRepository) ListSources(context.Context, string, *store.TimeCursor, int) ([]store.SourceView, error) {
	return []store.SourceView{}, nil
}
func (r *browserRepository) ListDeliveries(context.Context, string, string, *store.TimeCursor, int) ([]store.DeliveryView, error) {
	return []store.DeliveryView{}, nil
}
func (r *browserRepository) ExportAuditEvents(context.Context, string, int64, int) ([]audit.Event, error) {
	return []audit.Event{}, nil
}
func TestConsoleBrowserFixture(t *testing.T) {
	if os.Getenv("STATUSMON_UI_BROWSER_FIXTURE") != "1" {
		t.Skip("opt-in manual browser fixture")
	}
	now := time.Now()
	repo := &browserRepository{repositoryStub{vendors: []store.VendorStatus{{ID: "00000000-0000-4000-8000-000000000001", Name: "测试厂商", Slug: "fixture-vendor", Status: domain.ComponentStatusOperational, VisibleSources: 1, LastObservedAt: &now, LastSuccessfulAt: &now, SourceHealthState: "healthy"}}}}
	api := newTestServer(t, repo, verifierStub{tenantID: testTenantID}, nil)
	listener, err := net.Listen("tcp", "127.0.0.1:5174")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	go func() { _ = server.Serve(listener) }()
	t.Log("Production UI fixture: http://127.0.0.1:5174/ui/; workspace acme; test-only token valid")
	<-time.After(15 * time.Minute)
}
