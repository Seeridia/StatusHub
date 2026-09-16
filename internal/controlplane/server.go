package controlplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/audit"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/secret"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"github.com/google/uuid"
)

const maximumRequestBody = 1 << 20

type Repository interface {
	DeleteResource(context.Context, string, string, string, store.AuditActor) error
	RestoreResource(context.Context, string, string, string, store.AuditActor) error
	Ping(context.Context) error
	ResolveTenant(context.Context, string) (store.Tenant, error)
	ListVendorStatuses(context.Context, string) ([]store.VendorStatus, error)
	VendorStatus(context.Context, string, string) (store.VendorStatus, error)
	ListSources(context.Context, string, string, *store.TimeCursor, int) ([]store.SourceView, error)
	ListSourceCatalog(context.Context, string, string, int) ([]store.SourceCatalogItem, error)
	FindSourceByCanonicalURL(context.Context, string, string) (store.SourceView, error)
	Source(context.Context, string, string) (store.SourceView, error)
	CreateSource(context.Context, store.CreateSourceParams) (store.SourceView, error)
	AttachSource(context.Context, string, string, string, string, store.AuditActor) (store.SourceView, error)
	UpdateWorkspaceSource(context.Context, store.UpdateWorkspaceSourceParams) (store.SourceView, error)
	ListIncidents(context.Context, string, string, string, bool, *time.Time, *store.TimeCursor, int) ([]store.IncidentView, error)
	Incident(context.Context, string, string) (store.IncidentView, error)
	ListSubscriptions(context.Context, string, string, *store.TimeCursor, int) ([]store.SubscriptionView, error)
	Subscription(context.Context, string, string) (store.SubscriptionView, error)
	CreateSubscription(context.Context, store.CreateSubscriptionParams) (store.SubscriptionView, error)
	UpdateSubscription(context.Context, store.UpdateSubscriptionParams) (store.SubscriptionView, error)
	SetSubscriptionEnabled(context.Context, string, string, bool, store.AuditActor) (store.SubscriptionView, error)
	ListEndpoints(context.Context, string, string, *store.TimeCursor, int) ([]store.EndpointView, error)
	Endpoint(context.Context, string, string, bool) (store.EndpointSecret, error)
	CreateEndpoint(context.Context, store.CreateEndpointParams) (store.EndpointView, error)
	UpdateEndpoint(context.Context, store.CreateEndpointParams, int) (store.EndpointView, error)
	SetEndpointEnabled(context.Context, string, string, bool, store.AuditActor) (store.EndpointView, error)
	CreateEndpointTest(context.Context, string, string, string, store.AuditActor) (store.EndpointTestJob, error)
	EndpointTest(context.Context, string, string) (store.EndpointTestJob, error)
	ListDeliveries(context.Context, string, string, *store.TimeCursor, int) ([]store.DeliveryView, error)
	Delivery(context.Context, string, string) (store.DeliveryView, error)
	ReplayTenantDelivery(context.Context, string, string, time.Time, store.AuditActor) (bool, error)
	ListTenantEvents(context.Context, string, *store.TimeCursor, int) ([]store.EventView, error)
	TenantEvent(context.Context, string, string) (store.EventView, error)
	ExportAuditEvents(context.Context, string, int64, int) ([]audit.Event, error)
	BeginIdempotency(context.Context, store.IdempotencyRecord) (store.IdempotencyRecord, bool, error)
	CompleteIdempotency(context.Context, string, string, int, json.RawMessage) error
	CreateAdapterRollout(context.Context, store.CreateAdapterRolloutParams) (store.AdapterRollout, error)
	TenantAdapterRollout(context.Context, string, string) (store.AdapterRolloutView, error)
	TenantLatestAdapterRollout(context.Context, string, string) (store.AdapterRolloutView, error)
	PromoteAdapterRollout(context.Context, store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error)
	RollbackAdapterRollout(context.Context, store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error)
}

type Prober interface {
	Probe(context.Context, domain.Target) (domain.Capabilities, error)
}

type EventBroker struct {
	subscribe func() (<-chan string, func())
	ready     func() bool
}

func NewEventBroker(subscribe func() (<-chan string, func()), ready func() bool) EventBroker {
	return EventBroker{subscribe: subscribe, ready: ready}
}

type Config struct {
	ServiceRegion  string
	ProbeTimeout   time.Duration
	RequestTimeout time.Duration
	Logger         *slog.Logger
	PublicURL      string
	TrustedProxies []netip.Prefix
}

type Server struct {
	repository Repository
	verifier   auth.Verifier
	prober     Prober
	sealer     secret.Sealer
	sessions   *SessionManager
	cursors    *CursorCodec
	broker     EventBroker
	config     Config
	mux        *http.ServeMux
}

type contextKeys struct{}

type requestContext struct {
	Tenant     TenantContext
	Identity   auth.Identity
	RequestID  string
	CookieAuth bool
}

func NewServer(repository Repository, verifier auth.Verifier, prober Prober, sealer secret.Sealer,
	sessions *SessionManager, cursors *CursorCodec, broker EventBroker, config Config) (*Server, error) {
	if repository == nil || verifier == nil || prober == nil || sealer == nil || sessions == nil || cursors == nil {
		return nil, errors.New("controlplane: repository, verifier, prober, sealer, sessions, and cursors are required")
	}
	if strings.TrimSpace(config.ServiceRegion) == "" || config.ProbeTimeout <= 0 || config.RequestTimeout <= 0 {
		return nil, errors.New("controlplane: service region and positive timeouts are required")
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	server := &Server{repository: repository, verifier: verifier, prober: prober, sealer: sealer,
		sessions: sessions, cursors: cursors, broker: broker, config: config, mux: http.NewServeMux()}
	if origin, err := publicOrigin(config.PublicURL); err != nil {
		return nil, err
	} else {
		server.config.PublicURL = origin
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.requestID(s.requestTimeout(s.mux)))
}

func (s *Server) routes() {
	s.teamRoutes()
	s.mux.HandleFunc("GET /{$}", func(response http.ResponseWriter, request *http.Request) {
		target := "/ui/"
		if request.URL.RawQuery != "" {
			target += "?" + request.URL.RawQuery
		}
		http.Redirect(response, request, target, http.StatusFound)
	})
	s.mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
	})
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPI)
	s.mux.HandleFunc("GET /ui/", s.handleUI)
	s.mux.Handle("DELETE /v1/tenants/{tenant}/sources/{source}", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleDeleteResource(w, r, "source") })))

	s.mux.Handle("GET /v1/tenants/{tenant}/session", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSession)))

	s.mux.Handle("GET /v1/tenants/{tenant}/vendors", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleVendors)))
	s.mux.Handle("GET /v1/tenants/{tenant}/vendors/{vendor}/status", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleVendorStatus)))
	s.mux.Handle("GET /v1/tenants/{tenant}/sources", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSources)))
	s.mux.Handle("GET /v1/tenants/{tenant}/source-catalog", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSourceCatalog)))
	s.mux.Handle("POST /v1/tenants/{tenant}/sources:probe", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleProbeSource)))
	s.mux.Handle("POST /v1/tenants/{tenant}/sources", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleCreateSource)))
	s.mux.Handle("GET /v1/tenants/{tenant}/sources/{source}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSource)))
	s.mux.Handle("PATCH /v1/tenants/{tenant}/sources/{source}", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleUpdateSource)))
	s.mux.Handle("POST /v1/tenants/{tenant}/sources/{source}/replace", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleReplaceSource)))
	s.mux.Handle("POST /v1/tenants/{tenant}/sources/{source}/restore", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleRestoreResource(w, r, "source") })))
	s.mux.Handle("POST /v1/tenants/{tenant}/sources/{source}/adapter-rollouts", s.authorize(auth.PermissionAdapterRollout, http.HandlerFunc(s.handleCreateRollout)))
	s.mux.Handle("GET /v1/tenants/{tenant}/sources/{source}/adapter-rollout", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleLatestSourceRollout)))
	s.mux.Handle("GET /v1/tenants/{tenant}/adapter-rollouts/{rollout}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleRollout)))
	s.mux.Handle("POST /v1/tenants/{tenant}/adapter-rollouts/{rollout}/promote", s.authorize(auth.PermissionAdapterRollout, http.HandlerFunc(s.handlePromoteRollout)))
	s.mux.Handle("POST /v1/tenants/{tenant}/adapter-rollouts/{rollout}/rollback", s.authorize(auth.PermissionAdapterRollout, http.HandlerFunc(s.handleRollbackRollout)))

	s.mux.Handle("GET /v1/tenants/{tenant}/incidents", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleIncidents)))
	s.mux.Handle("GET /v1/tenants/{tenant}/incidents/{incident}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleIncident)))
	s.mux.Handle("GET /v1/tenants/{tenant}/events/stream", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleEventStream)))

	s.mux.Handle("GET /v1/tenants/{tenant}/subscriptions", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSubscriptions)))
	s.mux.Handle("POST /v1/tenants/{tenant}/subscriptions", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleCreateSubscription)))
	s.mux.Handle("GET /v1/tenants/{tenant}/subscriptions/{subscription}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleSubscription)))
	s.mux.Handle("PUT /v1/tenants/{tenant}/subscriptions/{subscription}", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleUpdateSubscription)))
	s.mux.Handle("DELETE /v1/tenants/{tenant}/subscriptions/{subscription}", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(s.handleDeleteSubscription)))
	s.mux.Handle("POST /v1/tenants/{tenant}/subscriptions/{subscription}/restore", s.authorize(auth.PermissionSubscriptionWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleRestoreResource(w, r, "subscription") })))

	s.mux.Handle("GET /v1/tenants/{tenant}/endpoints", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleEndpoints)))
	s.mux.Handle("POST /v1/tenants/{tenant}/endpoints", s.authorize(auth.PermissionEndpointWrite, http.HandlerFunc(s.handleCreateEndpoint)))
	s.mux.Handle("GET /v1/tenants/{tenant}/endpoints/{endpoint}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleEndpoint)))
	s.mux.Handle("PUT /v1/tenants/{tenant}/endpoints/{endpoint}", s.authorize(auth.PermissionEndpointWrite, http.HandlerFunc(s.handleUpdateEndpoint)))
	s.mux.Handle("DELETE /v1/tenants/{tenant}/endpoints/{endpoint}", s.authorize(auth.PermissionEndpointWrite, http.HandlerFunc(s.handleDeleteEndpoint)))
	s.mux.Handle("POST /v1/tenants/{tenant}/endpoints/{endpoint}/restore", s.authorize(auth.PermissionEndpointWrite, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.handleRestoreResource(w, r, "endpoint") })))
	s.mux.Handle("POST /v1/tenants/{tenant}/endpoints/{endpoint}/test", s.authorize(auth.PermissionEndpointWrite, http.HandlerFunc(s.handleTestEndpoint)))
	s.mux.Handle("GET /v1/tenants/{tenant}/endpoint-tests/{test}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleEndpointTest)))

	s.mux.Handle("GET /v1/tenants/{tenant}/deliveries", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleDeliveries)))
	s.mux.Handle("GET /v1/tenants/{tenant}/deliveries/{delivery}", s.authorize(auth.PermissionRead, http.HandlerFunc(s.handleDelivery)))
	s.mux.Handle("POST /v1/tenants/{tenant}/deliveries/{delivery}/retry", s.authorize(auth.PermissionDeliveryReplay, http.HandlerFunc(s.handleRetryDelivery)))
	s.mux.Handle("GET /v1/tenants/{tenant}/audit-events", s.authorize(auth.PermissionAuditExport, http.HandlerFunc(s.handleAuditEvents)))
}

func (s *Server) authorize(permission auth.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		tenantKey := request.PathValue("tenant")

		tenant, err := s.repository.ResolveTenant(request.Context(), tenantKey)
		if err != nil {
			writeProblem(response, request, err)
			return
		}
		contextValue := requestContext{Tenant: TenantContext{ID: tenant.ID, Key: tenantKey, Slug: tenant.Slug, Name: tenant.Name}, RequestID: requestID(request)}
		token, hasBearer := bearerToken(request.Header.Get("Authorization"))
		if hasBearer {
			contextValue.Identity, err = s.verifier.Authenticate(request.Context(), tenant.ID, token)
		} else {
			var session store.BrowserSession
			session, err = s.sessions.Read(request)
			if err == nil {
				contextValue.Identity, err = s.sessions.Store.MemberIdentity(request.Context(), session.User.ID, tenant.ID)
				if errors.Is(err, auth.ErrForbidden) {
					writeProblemStatus(response, request, 403, "workspace_forbidden", "Workspace access denied")
					return
				}
				contextValue.CookieAuth = true
				if isUnsafeMethod(request.Method) && !constantTimeEqual(request.Header.Get("X-CSRF-Token"), session.CSRF) {
					writeProblemStatus(response, request, http.StatusForbidden, "csrf_failed", "CSRF token is missing or invalid")
					return
				}
			} else {
				err = auth.ErrUnauthenticated
			}
		}
		if err != nil || contextValue.Identity.TenantID != tenant.ID {
			response.Header().Set("WWW-Authenticate", `Bearer realm="statushub"`)
			writeProblemStatus(response, request, http.StatusUnauthorized, "unauthenticated", "Authentication is required")
			return
		}
		if !auth.Allowed(contextValue.Identity.Role, permission) {
			writeProblemStatus(response, request, http.StatusForbidden, "forbidden", "The authenticated role does not have this permission")
			return
		}
		next.ServeHTTP(response, request.WithContext(context.WithValue(request.Context(), contextKeys{}, contextValue)))
	})
}

func requestDetails(request *http.Request) requestContext {
	value, _ := request.Context().Value(contextKeys{}).(requestContext)
	return value
}

func actor(request *http.Request) store.AuditActor {
	details := requestDetails(request)
	return store.AuditActor{Type: details.Identity.ActorType, ID: details.Identity.ActorID, RequestID: details.RequestID}
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(response, request)
	})
}

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		identifier := strings.TrimSpace(request.Header.Get("X-Request-ID"))
		if identifier == "" || len(identifier) > 128 {
			identifier = uuid.NewString()
		}
		request.Header.Set("X-Request-ID", identifier)
		response.Header().Set("X-Request-ID", identifier)
		next.ServeHTTP(response, request)
	})
}

func (s *Server) requestTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		// SSE is intentionally long lived. Its database catch-up calls and broker
		// lifecycle have their own cancellation through the client connection.
		if strings.HasSuffix(request.URL.Path, "/events/stream") {
			next.ServeHTTP(response, request)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), s.config.RequestTimeout)
		defer cancel()
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

func requestID(request *http.Request) string { return request.Header.Get("X-Request-ID") }

func bearerToken(value string) (string, bool) {
	parts := strings.Fields(value)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && len(parts[1]) <= 32<<10 {
		return parts[1], true
	}
	return "", false
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) || left == "" {
		return false
	}
	var difference byte
	for index := range len(left) {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id"`
}

func writeProblem(response http.ResponseWriter, request *http.Request, err error) {
	status, code, detail := http.StatusInternalServerError, "internal_error", "The request could not be completed"
	switch {
	case errors.Is(err, store.ErrNotFound):
		status, code, detail = http.StatusNotFound, "not_found", "The requested resource was not found"
	case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrIdempotencyConflict):
		status, code, detail = http.StatusConflict, "conflict", err.Error()
	case errors.Is(err, store.ErrIdempotencyBusy):
		status, code, detail = http.StatusConflict, "idempotency_in_progress", err.Error()
	case errors.Is(err, store.ErrReplayNotAllowed):
		status, code, detail = http.StatusConflict, "replay_not_allowed", "This delivery cannot be replayed"
	case errors.Is(err, store.ErrInvalidArgument), errors.Is(err, ErrInvalidCursor):
		status, code, detail = http.StatusBadRequest, "invalid_request", err.Error()
	case errors.Is(err, auth.ErrUnauthenticated):
		status, code, detail = http.StatusUnauthorized, "unauthenticated", "Authentication failed"
	case errors.Is(err, auth.ErrForbidden):
		status, code, detail = http.StatusForbidden, "forbidden", "Permission denied"
	}
	writeProblemStatus(response, request, status, code, detail)
}

func writeProblemStatus(response http.ResponseWriter, request *http.Request, status int, code, detail string) {
	response.Header().Set("Content-Type", "application/problem+json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(problem{Type: "https://statushub.dev/problems/" + code,
		Title: http.StatusText(status), Status: status, Detail: detail, RequestID: requestID(request)})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func decodeBody(request *http.Request, target any) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(request.Body, maximumRequestBody+1))
	if err != nil {
		return nil, fmt.Errorf("controlplane: read request body: %w", err)
	}
	if len(data) == 0 || len(data) > maximumRequestBody {
		return nil, fmt.Errorf("%w: request body is empty or exceeds 1 MiB", store.ErrInvalidArgument)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("%w: invalid JSON body: %v", store.ErrInvalidArgument, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("%w: request body contains trailing JSON", store.ErrInvalidArgument)
	}
	return data, nil
}

type writeOperation func(resourceID string) (int, any, error)

func (s *Server) executeIdempotent(response http.ResponseWriter, request *http.Request, body []byte, resourceID string, operation writeOperation) {
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > 128 {
		writeProblemStatus(response, request, http.StatusPreconditionRequired, "idempotency_key_required", "A valid Idempotency-Key header is required")
		return
	}
	details := requestDetails(request)
	digest := sha256.Sum256(body)
	record, leader, err := s.repository.BeginIdempotency(request.Context(), store.IdempotencyRecord{
		TenantID: details.Tenant.ID, Key: key, Method: request.Method, Route: request.URL.Path,
		RequestHash: digest[:], ResourceID: resourceID,
	})
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	if !leader {
		response.Header().Set("Idempotency-Replayed", "true")
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(record.ResponseStatus)
		_, _ = response.Write(append(record.ResponseBody, '\n'))
		return
	}
	status, value, err := operation(record.ResourceID)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	if err := s.repository.CompleteIdempotency(request.Context(), details.Tenant.ID, key, status, encoded); err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, status, value)
}

func pageLimit(request *http.Request) (int, error) {
	value := request.URL.Query().Get("limit")
	if value == "" {
		return 50, nil
	}
	var limit int
	if _, err := fmt.Sscanf(value, "%d", &limit); err != nil || limit <= 0 || limit > 200 {
		return 0, fmt.Errorf("%w: limit must be in [1,200]", store.ErrInvalidArgument)
	}
	return limit, nil
}

func nextTimeCursor[T any](items []T, limit int, value func(T) store.TimeCursor, codec *CursorCodec, kind, tenant string) string {
	if len(items) < limit || len(items) == 0 {
		return ""
	}
	encoded, _ := codec.EncodeTime(kind, tenant, value(items[len(items)-1]))
	return encoded
}
