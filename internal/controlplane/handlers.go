package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter/vendorprofile"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/audit"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/notify"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/secret"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/subscription"
)

type page[T any] struct {
	Data       []T    `json:"data"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func (s *Server) handleReady(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()
	if err := s.repository.Ping(ctx); err != nil {
		writeProblemStatus(response, request, http.StatusServiceUnavailable, "not_ready", "PostgreSQL is unavailable")
		return
	}
	if s.broker.ready != nil && !s.broker.ready() {
		writeProblemStatus(response, request, http.StatusServiceUnavailable, "not_ready", "The live event stream is unavailable")
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleOIDCStart(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeProblemStatus(response, request, http.StatusServiceUnavailable, "oidc_unavailable", "Browser OIDC login is not configured")
		return
	}
	if err := s.oidc.Start(response, request, request.PathValue("tenant")); err != nil {
		writeProblem(response, request, err)
	}
}

func (s *Server) handleOIDCCallback(response http.ResponseWriter, request *http.Request) {
	if s.oidc == nil {
		writeProblemStatus(response, request, http.StatusServiceUnavailable, "oidc_unavailable", "Browser OIDC login is not configured")
		return
	}
	if err := s.oidc.Callback(response, request); err != nil {
		writeProblem(response, request, err)
	}
}

func (s *Server) handleSession(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Cache-Control", "no-store")
	details := requestDetails(request)
	session, _ := s.sessions.Read(request)
	writeJSON(response, http.StatusOK, map[string]any{"tenant": details.Tenant, "identity": details.Identity, "csrf_token": session.CSRF})
}

func (s *Server) handleLogout(response http.ResponseWriter, request *http.Request) {
	if s.sessions.Store != nil {
		if session, err := s.sessions.Read(request); err == nil {
			if err = s.sessions.Store.RevokeBrowserSessions(request.Context(), session.Identity, session.ID); err != nil {
				writeProblem(response, request, err)
				return
			}
		}
	}
	s.sessions.Clear(response)
	writeJSON(response, http.StatusOK, map[string]bool{"logged_out": true})
}

func (s *Server) handleVendors(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	items, err := s.repository.ListVendorStatuses(request.Context(), details.Tenant.ID)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, page[store.VendorStatus]{Data: items})
}

func (s *Server) handleVendorStatus(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.VendorStatus(request.Context(), details.Tenant.ID, request.PathValue("vendor"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item)
}

func (s *Server) handleSources(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, cursor, err := s.listArguments(request, "sources")
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	items, err := s.repository.ListSources(request.Context(), details.Tenant.ID, cursor, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := nextTimeCursor(items, limit, func(item store.SourceView) store.TimeCursor {
		return store.TimeCursor{Time: item.UpdatedAt, ID: item.ID}
	}, s.cursors, "sources", details.Tenant.ID)
	writeJSON(response, http.StatusOK, page[store.SourceView]{Data: items, NextCursor: next})
}

func (s *Server) handleSource(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.Source(request.Context(), details.Tenant.ID, request.PathValue("source"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item)
}

type sourceRequest struct {
	DisplayName string `json:"display_name,omitempty"`
	URL         string `json:"url"`
	VendorID    string `json:"vendor_id,omitempty"`
	Provider    string `json:"provider,omitempty"`
	PageID      string `json:"page_id,omitempty"`
	SourceType  string `json:"source_type,omitempty"`
}

type probeResponse struct {
	RequestedURL   string              `json:"requested_url"`
	CanonicalURL   string              `json:"canonical_url"`
	Capabilities   domain.Capabilities `json:"capabilities"`
	Vendor         *sourceVendor       `json:"vendor,omitempty"`
	ExistingSource *store.SourceView   `json:"existing_source,omitempty"`
}

func (s *Server) handleProbeSource(response http.ResponseWriter, request *http.Request) {
	var input sourceRequest
	if _, err := decodeBody(request, &input); err != nil {
		writeProblem(response, request, err)
		return
	}
	result, err := s.probe(request.Context(), input, "probe-"+uuid.NewString())
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) handleCreateSource(response http.ResponseWriter, request *http.Request) {
	var input sourceRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, "", func(resourceID string) (int, any, error) {
		if input.VendorID != "" {
			if _, err := uuid.Parse(input.VendorID); err != nil {
				return 0, nil, fmt.Errorf("%w: vendor_id must be a UUID", store.ErrInvalidArgument)
			}
		}
		result, err := s.probe(request.Context(), input, resourceID)
		if err != nil {
			return 0, nil, err
		}
		if result.ExistingSource != nil {
			return http.StatusOK, result.ExistingSource, nil
		}
		vendorID := input.VendorID
		var vendorSlug, vendorName string
		if vendorID == "" {
			vendorID = result.Vendor.ID
			if result.Vendor.New {
				vendorSlug = result.Vendor.Slug
				vendorName = result.Vendor.Name
			}
		}
		sourceType := domain.SourceKind(input.SourceType)
		if sourceType == "" {
			sourceType = domain.SourceKindStatusPage
		}
		if !sourceType.Valid() {
			return 0, nil, fmt.Errorf("%w: source_type is invalid", store.ErrInvalidArgument)
		}
		item, err := s.repository.CreateSource(request.Context(), store.CreateSourceParams{
			ID: resourceID, TenantID: details.Tenant.ID, VendorID: vendorID,
			AutoVendorSlug: vendorSlug, AutoVendorName: vendorName,
			RequestedURL: result.RequestedURL, FinalURL: result.CanonicalURL, CanonicalURL: result.CanonicalURL,
			SourceType: string(sourceType), AdapterName: result.Capabilities.Engine,
			AdapterVersion: nonEmpty(result.Capabilities.Version, "1"), ActiveRegion: s.config.ServiceRegion,
			Capabilities: result.Capabilities, Actor: actor(request),
		})
		return http.StatusCreated, item, err
	})
}

func (s *Server) probe(parent context.Context, input sourceRequest, sourceID string) (probeResponse, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if len([]rune(input.DisplayName)) > 120 || strings.ContainsAny(input.DisplayName, "\r\n\t") {
		return probeResponse{}, fmt.Errorf("%w: display_name must be at most 120 characters without tabs or line breaks", store.ErrInvalidArgument)
	}
	parsed, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return probeResponse{}, fmt.Errorf("%w: source URL must be an absolute HTTPS URL without credentials or fragment", store.ErrInvalidArgument)
	}
	canonical, _, _, err := vendorprofile.Canonicalize(parsed)
	if err != nil {
		return probeResponse{}, fmt.Errorf("%w: %v", store.ErrInvalidArgument, err)
	}
	ctx, cancel := context.WithTimeout(parent, s.config.ProbeTimeout)
	defer cancel()
	capabilities, err := s.prober.Probe(ctx, domain.Target{URL: parsed, SourceID: sourceID, Provider: input.Provider, PageID: input.PageID})
	if err != nil {
		return probeResponse{}, fmt.Errorf("controlplane: probe source: %w", err)
	}
	if len(capabilities.Endpoints) == 0 || strings.TrimSpace(capabilities.Engine) == "" {
		return probeResponse{}, fmt.Errorf("%w: probe found no supported status resources", store.ErrInvalidArgument)
	}
	result := probeResponse{RequestedURL: parsed.String(), CanonicalURL: canonical.String(), Capabilities: capabilities}
	if err := s.identifySource(parent, &result); err != nil {
		return probeResponse{}, err
	}
	if result.Vendor.New && input.DisplayName != "" {
		result.Vendor.Name = input.DisplayName
	}
	return result, nil
}

func (s *Server) handleUpdateSource(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	body, err := decodeBody(request, &input)
	if err != nil || input.Enabled == nil {
		if err == nil {
			err = fmt.Errorf("%w: enabled is required", store.ErrInvalidArgument)
		}
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("source"), func(_ string) (int, any, error) {
		item, err := s.repository.SetSourceEnabled(request.Context(), details.Tenant.ID, request.PathValue("source"), *input.Enabled, actor(request))
		return http.StatusOK, item, err
	})
}

type rolloutRequest struct {
	CandidateAdapterName    string   `json:"candidate_adapter_name"`
	CandidateAdapterVersion string   `json:"candidate_adapter_version"`
	SampleRate              *float64 `json:"sample_rate,omitempty"`
	MinimumSamples          *int     `json:"minimum_samples,omitempty"`
	MaximumMismatchRate     *float64 `json:"maximum_mismatch_rate,omitempty"`
	MaximumErrorRate        *float64 `json:"maximum_error_rate,omitempty"`
}

func (s *Server) handleCreateRollout(response http.ResponseWriter, request *http.Request) {
	var input rolloutRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	sampleRate := 1.0
	if input.SampleRate != nil {
		sampleRate = *input.SampleRate
	}
	minimumSamples := 100
	if input.MinimumSamples != nil {
		minimumSamples = *input.MinimumSamples
	}
	maximumMismatchRate := 0.0
	if input.MaximumMismatchRate != nil {
		maximumMismatchRate = *input.MaximumMismatchRate
	}
	maximumErrorRate := .01
	if input.MaximumErrorRate != nil {
		maximumErrorRate = *input.MaximumErrorRate
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, "", func(resourceID string) (int, any, error) {
		rollout, err := s.repository.CreateAdapterRollout(request.Context(), store.CreateAdapterRolloutParams{
			ID: resourceID, SourceID: request.PathValue("source"), ScopeTenantID: details.Tenant.ID,
			CandidateAdapterName:    strings.TrimSpace(input.CandidateAdapterName),
			CandidateAdapterVersion: strings.TrimSpace(input.CandidateAdapterVersion),
			SampleRate:              sampleRate, MinimumSamples: minimumSamples,
			MaximumMismatchRate: maximumMismatchRate, MaximumErrorRate: maximumErrorRate,
			CreatedBy: details.Identity.ActorID,
		})
		return http.StatusCreated, rollout, err
	})
}

func (s *Server) handleRollout(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	view, err := s.repository.TenantAdapterRollout(request.Context(), details.Tenant.ID, request.PathValue("rollout"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (s *Server) handleLatestSourceRollout(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	view, err := s.repository.TenantLatestAdapterRollout(request.Context(), details.Tenant.ID, request.PathValue("source"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, view)
}

func (s *Server) handlePromoteRollout(response http.ResponseWriter, request *http.Request) {
	s.handleRolloutDecision(response, request, true)
}

func (s *Server) handleRollbackRollout(response http.ResponseWriter, request *http.Request) {
	s.handleRolloutDecision(response, request, false)
}

func (s *Server) handleRolloutDecision(response http.ResponseWriter, request *http.Request, promote bool) {
	var input struct {
		Reason string `json:"reason"`
	}
	body, err := decodeBody(request, &input)
	if err != nil || strings.TrimSpace(input.Reason) == "" {
		if err == nil {
			err = fmt.Errorf("%w: decision reason is required", store.ErrInvalidArgument)
		}
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("rollout"), func(resourceID string) (int, any, error) {
		params := store.DecideAdapterRolloutParams{RolloutID: resourceID, ScopeTenantID: details.Tenant.ID,
			ChangedBy: details.Identity.ActorID, Reason: strings.TrimSpace(input.Reason)}
		if promote {
			stats, err := s.repository.PromoteAdapterRollout(request.Context(), params)
			return http.StatusOK, stats, err
		}
		stats, err := s.repository.RollbackAdapterRollout(request.Context(), params)
		return http.StatusOK, stats, err
	})
}

func (s *Server) handleIncidents(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, cursor, err := s.listArguments(request, "incidents")
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	var since *time.Time
	if raw := request.URL.Query().Get("since"); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			writeProblem(response, request, fmt.Errorf("%w: since must be RFC3339", store.ErrInvalidArgument))
			return
		}
		since = &parsed
	}
	items, err := s.repository.ListIncidents(request.Context(), details.Tenant.ID,
		request.URL.Query().Get("vendor"), request.URL.Query().Get("phase"), since, cursor, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := nextTimeCursor(items, limit, func(item store.IncidentView) store.TimeCursor {
		return store.TimeCursor{Time: item.UpdatedAt, ID: item.ID}
	}, s.cursors, "incidents", details.Tenant.ID)
	writeJSON(response, http.StatusOK, page[store.IncidentView]{Data: items, NextCursor: next})
}

func (s *Server) handleIncident(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.Incident(request.Context(), details.Tenant.ID, request.PathValue("incident"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item)
}

type subscriptionRequest struct {
	Name                string                         `json:"name"`
	Enabled             *bool                          `json:"enabled,omitempty"`
	ExpectedRuleVersion int                            `json:"expected_rule_version,omitempty"`
	Rule                subscription.Rule              `json:"rule"`
	Scopes              []store.SubscriptionScopeInput `json:"scopes,omitempty"`
	EndpointIDs         []string                       `json:"endpoint_ids"`
}

func (s *Server) handleSubscriptions(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, cursor, err := s.listArguments(request, "subscriptions")
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	items, err := s.repository.ListSubscriptions(request.Context(), details.Tenant.ID, cursor, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := nextTimeCursor(items, limit, func(item store.SubscriptionView) store.TimeCursor {
		return store.TimeCursor{Time: item.UpdatedAt, ID: item.ID}
	}, s.cursors, "subscriptions", details.Tenant.ID)
	writeJSON(response, http.StatusOK, page[store.SubscriptionView]{Data: items, NextCursor: next})
}

func (s *Server) handleSubscription(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.Subscription(request.Context(), details.Tenant.ID, request.PathValue("subscription"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item)
}

func (s *Server) handleCreateSubscription(response http.ResponseWriter, request *http.Request) {
	var input subscriptionRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, "", func(resourceID string) (int, any, error) {
		rule, err := marshalAndValidateRule(input.Rule)
		if err != nil {
			return 0, nil, err
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		item, err := s.repository.CreateSubscription(request.Context(), store.CreateSubscriptionParams{
			ID: resourceID, TenantID: details.Tenant.ID, Name: strings.TrimSpace(input.Name), Enabled: enabled,
			Rule: rule, Scopes: input.Scopes, EndpointIDs: input.EndpointIDs, Actor: actor(request),
		})
		return http.StatusCreated, item, err
	})
}

func (s *Server) handleUpdateSubscription(response http.ResponseWriter, request *http.Request) {
	var input subscriptionRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("subscription"), func(resourceID string) (int, any, error) {
		rule, err := marshalAndValidateRule(input.Rule)
		if err != nil {
			return 0, nil, err
		}
		enabled := true
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		item, err := s.repository.UpdateSubscription(request.Context(), store.UpdateSubscriptionParams{
			CreateSubscriptionParams: store.CreateSubscriptionParams{ID: resourceID, TenantID: details.Tenant.ID,
				Name: strings.TrimSpace(input.Name), Enabled: enabled, Rule: rule, Scopes: input.Scopes,
				EndpointIDs: input.EndpointIDs, Actor: actor(request)}, ExpectedRuleVersion: input.ExpectedRuleVersion,
		})
		return http.StatusOK, item, err
	})
}

func (s *Server) handleDisableSubscription(response http.ResponseWriter, request *http.Request) {
	body := []byte(`{"enabled":false}`)
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("subscription"), func(resourceID string) (int, any, error) {
		item, err := s.repository.SetSubscriptionEnabled(request.Context(), details.Tenant.ID, resourceID, false, actor(request))
		return http.StatusOK, item, err
	})
}

func marshalAndValidateRule(rule subscription.Rule) (json.RawMessage, error) {
	encoded, err := json.Marshal(rule)
	if err != nil {
		return nil, fmt.Errorf("%w: subscription rule cannot be encoded", store.ErrInvalidArgument)
	}
	// Evaluate a valid representative event to validate quiet-hours timezone
	// and clock syntax without duplicating the rule package's semantics.
	_, err = subscription.Evaluate(encoded, subscription.Event{Kind: domain.EventKindIncidentCreated,
		Payload: json.RawMessage(`{"current":{"name":"validation","impact":"major"}}`), ObservedAt: time.Now().UTC()}, time.Now().UTC())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", store.ErrInvalidArgument, err)
	}
	return encoded, nil
}

type endpointConfigInput struct {
	URL              string `json:"url,omitempty"`
	Secret           string `json:"secret,omitempty"`
	SigningKeyID     string `json:"signing_key_id,omitempty"`
	AccountSID       string `json:"account_sid,omitempty"`
	From             string `json:"from,omitempty"`
	To               string `json:"to,omitempty"`
	ConfigurationSet string `json:"configuration_set,omitempty"`
	CallbackURL      string `json:"callback_url,omitempty"`
	MaxPayloadBytes  int    `json:"max_payload_bytes,omitempty"`
}

type endpointRequest struct {
	Name                  string              `json:"name"`
	Channel               notify.Channel      `json:"channel"`
	Enabled               *bool               `json:"enabled,omitempty"`
	ExpectedSecretVersion int                 `json:"expected_secret_version,omitempty"`
	Config                endpointConfigInput `json:"config"`
	RateLimitConfig       json.RawMessage     `json:"rate_limit_config,omitempty"`
}

func (s *Server) handleEndpoints(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, cursor, err := s.listArguments(request, "endpoints")
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	items, err := s.repository.ListEndpoints(request.Context(), details.Tenant.ID, cursor, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := nextTimeCursor(items, limit, func(item store.EndpointView) store.TimeCursor {
		return store.TimeCursor{Time: item.UpdatedAt, ID: item.ID}
	}, s.cursors, "endpoints", details.Tenant.ID)
	writeJSON(response, http.StatusOK, page[store.EndpointView]{Data: items, NextCursor: next})
}

func (s *Server) handleEndpoint(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.Endpoint(request.Context(), details.Tenant.ID, request.PathValue("endpoint"), false)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item.EndpointView)
}

func (s *Server) handleCreateEndpoint(response http.ResponseWriter, request *http.Request) {
	var input endpointRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, "", func(resourceID string) (int, any, error) {
		params, err := s.endpointParams(request.Context(), details.Tenant.ID, resourceID, 1, input)
		if err != nil {
			return 0, nil, err
		}
		params.Actor = actor(request)
		item, err := s.repository.CreateEndpoint(request.Context(), params)
		return http.StatusCreated, item, err
	})
}

func (s *Server) handleUpdateEndpoint(response http.ResponseWriter, request *http.Request) {
	var input endpointRequest
	body, err := decodeBody(request, &input)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("endpoint"), func(resourceID string) (int, any, error) {
		params, err := s.endpointParams(request.Context(), details.Tenant.ID, resourceID, input.ExpectedSecretVersion+1, input)
		if err != nil {
			return 0, nil, err
		}
		params.Actor = actor(request)
		item, err := s.repository.UpdateEndpoint(request.Context(), params, input.ExpectedSecretVersion)
		return http.StatusOK, item, err
	})
}

func (s *Server) handleDisableEndpoint(response http.ResponseWriter, request *http.Request) {
	body := []byte(`{"enabled":false}`)
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("endpoint"), func(resourceID string) (int, any, error) {
		item, err := s.repository.SetEndpointEnabled(request.Context(), details.Tenant.ID, resourceID, false, actor(request))
		return http.StatusOK, item, err
	})
}

func (s *Server) endpointParams(ctx context.Context, tenantID, endpointID string, version int, input endpointRequest) (store.CreateEndpointParams, error) {
	if version <= 0 {
		return store.CreateEndpointParams{}, fmt.Errorf("%w: expected_secret_version is required", store.ErrInvalidArgument)
	}
	if err := validateEndpointInput(ctx, endpointID, input.Channel, input.Config); err != nil {
		return store.CreateEndpointParams{}, err
	}
	plaintext, err := json.Marshal(input.Config)
	if err != nil {
		return store.CreateEndpointParams{}, err
	}
	defer clear(plaintext)
	ciphertext, err := s.sealer.Seal(ctx, plaintext, secret.EndpointAssociatedData(endpointID, version))
	if err != nil {
		return store.CreateEndpointParams{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	rateLimits := input.RateLimitConfig
	if len(rateLimits) == 0 {
		rateLimits = json.RawMessage(`{}`)
	}
	return store.CreateEndpointParams{ID: endpointID, TenantID: tenantID, Channel: string(input.Channel),
		Name: strings.TrimSpace(input.Name), Enabled: enabled, EncryptedConfig: ciphertext,
		KeyID: s.sealer.KeyID(), SecretVersion: version, RateLimits: rateLimits}, nil
}

func validateEndpointInput(ctx context.Context, endpointID string, channel notify.Channel, config endpointConfigInput) error {
	endpoint := notify.Endpoint{ID: endpointID, Channel: channel, URL: strings.TrimSpace(config.URL),
		KeyID: strings.TrimSpace(config.SigningKeyID), Secret: []byte(config.Secret), MaxPayloadBytes: config.MaxPayloadBytes,
		AccountSID: config.AccountSID, From: config.From, To: config.To,
		ConfigurationSet: config.ConfigurationSet, CallbackURL: config.CallbackURL}
	var driver notify.ChannelDriver
	switch channel {
	case notify.ChannelGenericWebhook:
		driver = notify.NewGenericWebhook(nil)
	case notify.ChannelSlack:
		driver = notify.NewSlack(nil)
	default:
		return fmt.Errorf("%w: M5 endpoint management supports generic_webhook and slack", store.ErrInvalidArgument)
	}
	if err := driver.Validate(ctx, endpoint); err != nil {
		return fmt.Errorf("%w: endpoint configuration is invalid: %v", store.ErrInvalidArgument, err)
	}
	return nil
}

func (s *Server) handleTestEndpoint(response http.ResponseWriter, request *http.Request) {
	body := []byte(`{}`)
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, "", func(resourceID string) (int, any, error) {
		job, err := s.repository.CreateEndpointTest(request.Context(), details.Tenant.ID, request.PathValue("endpoint"), resourceID, actor(request))
		return http.StatusAccepted, job, err
	})
}

func (s *Server) handleEndpointTest(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	job, err := s.repository.EndpointTest(request.Context(), details.Tenant.ID, request.PathValue("test"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, job)
}

func (s *Server) handleDeliveries(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, cursor, err := s.listArguments(request, "deliveries")
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	items, err := s.repository.ListDeliveries(request.Context(), details.Tenant.ID, request.URL.Query().Get("status"), cursor, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := nextTimeCursor(items, limit, func(item store.DeliveryView) store.TimeCursor {
		return store.TimeCursor{Time: item.CreatedAt, ID: item.ID}
	}, s.cursors, "deliveries", details.Tenant.ID)
	writeJSON(response, http.StatusOK, page[store.DeliveryView]{Data: items, NextCursor: next})
}

func (s *Server) handleDelivery(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	item, err := s.repository.Delivery(request.Context(), details.Tenant.ID, request.PathValue("delivery"))
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	writeJSON(response, http.StatusOK, item)
}

func (s *Server) handleRetryDelivery(response http.ResponseWriter, request *http.Request) {
	body := []byte(`{}`)
	details := requestDetails(request)
	s.executeIdempotent(response, request, body, request.PathValue("delivery"), func(resourceID string) (int, any, error) {
		replayed, err := s.repository.ReplayTenantDelivery(request.Context(), details.Tenant.ID, resourceID, time.Now().UTC(), actor(request))
		return http.StatusAccepted, map[string]any{"delivery_id": resourceID, "replayed": replayed}, err
	})
}

func (s *Server) handleAuditEvents(response http.ResponseWriter, request *http.Request) {
	details := requestDetails(request)
	limit, err := pageLimit(request)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	after, err := s.cursors.DecodeSequence(request.URL.Query().Get("cursor"), "audit", details.Tenant.ID)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	items, err := s.repository.ExportAuditEvents(request.Context(), details.Tenant.ID, after, limit)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	next := ""
	if len(items) == limit && len(items) > 0 {
		next, _ = s.cursors.EncodeSequence("audit", details.Tenant.ID, items[len(items)-1].Sequence)
	}
	writeJSON(response, http.StatusOK, page[audit.Event]{Data: items, NextCursor: next})
}

func (s *Server) listArguments(request *http.Request, kind string) (int, *store.TimeCursor, error) {
	limit, err := pageLimit(request)
	if err != nil {
		return 0, nil, err
	}
	details := requestDetails(request)
	cursor, err := s.cursors.DecodeTime(request.URL.Query().Get("cursor"), kind, details.Tenant.ID)
	return limit, cursor, err
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func parsePositiveInt(value string, fallback int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, errors.New("expected a positive integer")
	}
	return parsed, nil
}
