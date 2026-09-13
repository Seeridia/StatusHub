package controlplane

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/adapter/shadow"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/secret"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

const (
	testTenantID   = "60000000-0000-0000-0000-000000000001"
	testEndpointID = "61000000-0000-0000-0000-000000000001"
)

type repositoryStub struct {
	Repository
	vendors      []store.VendorStatus
	created      store.CreateEndpointParams
	rollout      store.AdapterRolloutView
	rolloutInput store.CreateAdapterRolloutParams
	decision     store.DecideAdapterRolloutParams
	promoted     bool
	completedKey string
	event        store.EventView
}

func (r *repositoryStub) ResolveTenant(_ context.Context, key string) (store.Tenant, error) {
	if key != "acme" && key != testTenantID {
		return store.Tenant{}, store.ErrNotFound
	}
	return store.Tenant{ID: testTenantID, Slug: "acme", Name: "Acme"}, nil
}
func (r *repositoryStub) ListVendorStatuses(context.Context, string) ([]store.VendorStatus, error) {
	return r.vendors, nil
}
func (r *repositoryStub) BeginIdempotency(_ context.Context, record store.IdempotencyRecord) (store.IdempotencyRecord, bool, error) {
	if record.ResourceID == "" {
		record.ResourceID = testEndpointID
	}
	return record, true, nil
}
func (r *repositoryStub) CompleteIdempotency(_ context.Context, _ string, key string, _ int, _ json.RawMessage) error {
	r.completedKey = key
	return nil
}
func (r *repositoryStub) CreateEndpoint(_ context.Context, params store.CreateEndpointParams) (store.EndpointView, error) {
	r.created = params
	return store.EndpointView{ID: params.ID, TenantID: params.TenantID, Name: params.Name,
		Channel: params.Channel, Enabled: params.Enabled, KeyID: params.KeyID, SecretVersion: params.SecretVersion}, nil
}
func (r *repositoryStub) CreateAdapterRollout(_ context.Context, params store.CreateAdapterRolloutParams) (store.AdapterRollout, error) {
	r.rolloutInput = params
	return store.AdapterRollout{Rollout: shadow.Rollout{ID: params.ID, SourceID: params.SourceID,
		CandidateAdapterName: params.CandidateAdapterName, CandidateAdapterVersion: params.CandidateAdapterVersion,
		SampleRate: params.SampleRate, MinimumSamples: params.MinimumSamples,
		MaximumMismatchRate: params.MaximumMismatchRate, MaximumErrorRate: params.MaximumErrorRate}, State: "shadow"}, nil
}
func (r *repositoryStub) TenantLatestAdapterRollout(_ context.Context, tenantID, sourceID string) (store.AdapterRolloutView, error) {
	if tenantID != testTenantID || sourceID != r.rollout.Rollout.SourceID {
		return store.AdapterRolloutView{}, store.ErrNotFound
	}
	return r.rollout, nil
}
func (r *repositoryStub) TenantAdapterRollout(_ context.Context, tenantID, rolloutID string) (store.AdapterRolloutView, error) {
	if tenantID != testTenantID || rolloutID != r.rollout.Rollout.ID {
		return store.AdapterRolloutView{}, store.ErrNotFound
	}
	return r.rollout, nil
}
func (r *repositoryStub) PromoteAdapterRollout(_ context.Context, params store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error) {
	r.decision, r.promoted = params, true
	return r.rollout.Statistics, nil
}
func (r *repositoryStub) RollbackAdapterRollout(_ context.Context, params store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error) {
	r.decision, r.promoted = params, false
	return r.rollout.Statistics, nil
}
func (r *repositoryStub) ListTenantEvents(context.Context, string, *store.TimeCursor, int) ([]store.EventView, error) {
	return nil, nil
}
func (r *repositoryStub) TenantEvent(_ context.Context, tenantID, eventID string) (store.EventView, error) {
	if tenantID != testTenantID || eventID != r.event.ID {
		return store.EventView{}, store.ErrNotFound
	}
	return r.event, nil
}

type verifierStub struct{ tenantID string }

func (v verifierStub) Authenticate(_ context.Context, tenantID, token string) (auth.Identity, error) {
	if token != "valid" {
		return auth.Identity{}, auth.ErrUnauthenticated
	}
	return auth.Identity{TenantID: v.tenantID, ActorType: "service_account", ActorID: "tester", Role: auth.RoleOwner}, nil
}

type proberStub struct{}

func (proberStub) Probe(context.Context, domain.Target) (domain.Capabilities, error) {
	return domain.Capabilities{}, nil
}

func newTestServer(t *testing.T, repository Repository, verifier auth.Verifier, hub *EventHub) *Server {
	t.Helper()
	envelope, err := secret.NewStaticEnvelope("test-v1", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := NewSessionManager(bytes.Repeat([]byte{2}, 32), false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cursors, err := NewCursorCodec(bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	broker := EventBroker{}
	if hub != nil {
		broker = hub.Broker()
	}
	server, err := NewServer(repository, verifier, proberStub{}, envelope, sessions, cursors, nil, broker,
		Config{ServiceRegion: "local", ProbeTimeout: time.Second, RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestServerAuthorizesTenantAndNeverReturnsEndpointSecret(t *testing.T) {
	repository := &repositoryStub{vendors: []store.VendorStatus{{ID: "vendor", Slug: "github", Name: "GitHub", Status: domain.ComponentStatusOperational}}}
	server := newTestServer(t, repository, verifierStub{tenantID: testTenantID}, nil)
	request := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/vendors", nil)
	request.Header.Set("Authorization", "Bearer valid")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "GitHub") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	crossTenant := newTestServer(t, repository, verifierStub{tenantID: "another-tenant"}, nil)
	recorder = httptest.NewRecorder()
	crossTenant.Handler().ServeHTTP(recorder, request.Clone(context.Background()))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("cross-tenant status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	body := `{"name":"Ops","channel":"generic_webhook","config":{"url":"https://notify.example.test/hook","secret":"top-secret","signing_key_id":"sign-v1"}}`
	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/endpoints", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer valid")
	request.Header.Set("Idempotency-Key", "create-endpoint-1")
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create endpoint status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if repository.created.ID != testEndpointID || repository.created.TenantID != testTenantID || repository.completedKey != "create-endpoint-1" {
		t.Fatalf("created=%#v completed=%q", repository.created, repository.completedKey)
	}
	if bytes.Contains(repository.created.EncryptedConfig, []byte("top-secret")) || strings.Contains(recorder.Body.String(), "top-secret") {
		t.Fatal("endpoint secret leaked from encrypted persistence or response")
	}
}

func TestCookieSessionRequiresCSRFForWrites(t *testing.T) {
	repository := &repositoryStub{}
	server := newTestServer(t, repository, verifierStub{tenantID: testTenantID}, nil)
	session, err := server.sessions.NewSession(TenantContext{ID: testTenantID, Key: "acme", Slug: "acme", Name: "Acme"},
		auth.Identity{TenantID: testTenantID, ActorType: "user", ActorID: "browser-user", Role: auth.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	cookieResponse := httptest.NewRecorder()
	if err := server.sessions.Set(cookieResponse, session); err != nil {
		t.Fatal(err)
	}
	cookie := cookieResponse.Result().Cookies()[0]
	body := `{"name":"Ops","channel":"generic_webhook","config":{"url":"https://notify.example.test/hook","secret":"top-secret","signing_key_id":"sign-v1"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/endpoints", strings.NewReader(body))
	request.AddCookie(cookie)
	request.Header.Set("Idempotency-Key", "cookie-endpoint-1")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "csrf_failed") {
		t.Fatalf("missing CSRF status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/endpoints", strings.NewReader(body))
	request.AddCookie(cookie)
	request.Header.Set("Idempotency-Key", "cookie-endpoint-2")
	request.Header.Set("X-CSRF-Token", session.CSRF)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated || repository.created.TenantID != testTenantID {
		t.Fatalf("valid CSRF status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestEventStreamUsesLiveBrokerAndTenantLookup(t *testing.T) {
	hub := NewEventHub()
	hub.SetReady(true)
	repository := &repositoryStub{event: store.EventView{ID: "62000000-0000-0000-0000-000000000001",
		SourceID: "source", VendorID: "vendor", VendorSlug: "github", Kind: domain.EventKindIncidentCreated,
		EntityKind: domain.EntityIncident, EntityID: "incident", AggregateRevision: 1, SchemaVersion: "v1",
		Payload: json.RawMessage(`{"current":{"name":"Incident"}}`), ObservedAt: time.Now().UTC(), IngestedAt: time.Now().UTC()}}
	server := httptest.NewServer(newTestServer(t, repository, verifierStub{tenantID: testTenantID}, hub).Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/tenants/acme/events/stream", nil)
	request.Header.Set("Authorization", "Bearer valid")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	hub.Publish(repository.event.ID)
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				t.Fatal("stream ended before event")
			}
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, repository.event.ID) {
				t.Fatalf("unexpected event line=%s", line)
			}
			break
		}
	}
}

func TestRequestTimeoutAppliesToAPIAndExcludesEventStream(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 25 * time.Millisecond}}
	for _, test := range []struct {
		path         string
		wantDeadline bool
	}{
		{path: "/v1/tenants/acme/vendors", wantDeadline: true},
		{path: "/v1/tenants/acme/events/stream", wantDeadline: false},
	} {
		t.Run(test.path, func(t *testing.T) {
			handler := server.requestTimeout(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				_, hasDeadline := request.Context().Deadline()
				if hasDeadline != test.wantDeadline {
					t.Fatalf("deadline=%v want=%v", hasDeadline, test.wantDeadline)
				}
				response.WriteHeader(http.StatusNoContent)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status=%d", recorder.Code)
			}
		})
	}
}

func TestAdapterRolloutAPIUsesTenantScopeDefaultsAndDecisions(t *testing.T) {
	const sourceID = "63000000-0000-0000-0000-000000000001"
	const rolloutID = "64000000-0000-0000-0000-000000000001"
	repository := &repositoryStub{rollout: store.AdapterRolloutView{Rollout: store.AdapterRollout{
		Rollout: shadow.Rollout{ID: rolloutID, SourceID: sourceID, CandidateAdapterName: "statuspage-v2", CandidateAdapterVersion: "2"},
		State:   "shadow",
	}, Statistics: store.AdapterRolloutStats{Total: 100, Equivalent: 100}}}
	server := newTestServer(t, repository, verifierStub{tenantID: testTenantID}, nil)

	create := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/sources/"+sourceID+"/adapter-rollouts",
		strings.NewReader(`{"candidate_adapter_name":"statuspage-v2","candidate_adapter_version":"2","maximum_error_rate":0}`))
	create.Header.Set("Authorization", "Bearer valid")
	create.Header.Set("Idempotency-Key", "rollout-create")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, create)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	if repository.rolloutInput.ScopeTenantID != testTenantID || repository.rolloutInput.SourceID != sourceID ||
		repository.rolloutInput.SampleRate != 1 || repository.rolloutInput.MinimumSamples != 100 ||
		repository.rolloutInput.MaximumMismatchRate != 0 || repository.rolloutInput.MaximumErrorRate != 0 {
		t.Fatalf("rollout input=%#v", repository.rolloutInput)
	}

	latest := httptest.NewRequest(http.MethodGet, "/v1/tenants/acme/sources/"+sourceID+"/adapter-rollout", nil)
	latest.Header.Set("Authorization", "Bearer valid")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, latest)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), rolloutID) {
		t.Fatalf("latest status=%d body=%s", response.Code, response.Body.String())
	}

	promote := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/adapter-rollouts/"+rolloutID+"/promote",
		strings.NewReader(`{"reason":"quality gate passed"}`))
	promote.Header.Set("Authorization", "Bearer valid")
	promote.Header.Set("Idempotency-Key", "rollout-promote")
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, promote)
	if response.Code != http.StatusOK || !repository.promoted || repository.decision.RolloutID != rolloutID ||
		repository.decision.ScopeTenantID != testTenantID || repository.decision.Reason != "quality gate passed" {
		t.Fatalf("promote status=%d decision=%#v promoted=%v body=%s", response.Code, repository.decision, repository.promoted, response.Body.String())
	}
}

func TestLarkEndpointCreation(t *testing.T) {
	for _, tc := range []struct {
		name, secret string
		status       int
	}{
		{"signed", "test-lark-secret", http.StatusCreated},
		{"missing signing secret", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &repositoryStub{}
			server := newTestServer(t, repository, verifierStub{tenantID: testTenantID}, nil)
			body := `{"name":"Feishu","channel":"lark","config":{"url":"https://open.feishu.cn/open-apis/bot/v2/hook/test","secret":"` + tc.secret + `"}}`
			request := httptest.NewRequest(http.MethodPost, "/v1/tenants/acme/endpoints", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer valid")
			request.Header.Set("Idempotency-Key", "create-lark")
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)
			if recorder.Code != tc.status {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if tc.secret != "" && (bytes.Contains(repository.created.EncryptedConfig, []byte(tc.secret)) || strings.Contains(recorder.Body.String(), tc.secret)) {
				t.Fatal("Lark signing secret leaked")
			}
		})
	}
}

func (r *repositoryStub) DeleteResource(context.Context, string, string, string, store.AuditActor) error {
	return nil
}

type deletionVerifier struct{ role auth.Role }

func (v deletionVerifier) Authenticate(context.Context, string, string) (auth.Identity, error) {
	return auth.Identity{TenantID: testTenantID, ActorType: "service_account", ActorID: "tester", Role: v.role}, nil
}
func TestDeleteResourcePermissions(t *testing.T) {
	for _, resource := range []string{"sources", "subscriptions", "endpoints"} {
		for _, role := range []auth.Role{auth.RoleViewer, auth.RoleOperator, auth.RoleAdmin, auth.RoleOwner} {
			t.Run(resource+"/"+string(role), func(t *testing.T) {
				repository := &repositoryStub{}
				server := newTestServer(t, repository, deletionVerifier{role}, nil)
				request := httptest.NewRequest(http.MethodDelete, "/v1/tenants/acme/"+resource+"/"+testEndpointID, nil)
				request.Header.Set("Authorization", "Bearer valid")
				request.Header.Set("Idempotency-Key", "delete-test")
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				want := http.StatusOK
				if role == auth.RoleViewer {
					want = http.StatusForbidden
				}
				if response.Code != want {
					t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
				}
				if want == http.StatusOK && !strings.Contains(response.Body.String(), `"deleted":true`) {
					t.Fatal(response.Body.String())
				}
			})
		}
	}
}
