// Package privateagent exposes a pull-based delivery protocol for agents that
// run inside tenant networks. Agents only make outbound HTTPS connections;
// the public service never relaxes its SSRF policy to reach private targets.
package privateagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/notify"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

const (
	maximumRequestBody = 64 << 10
	maximumLongPoll    = 25 * time.Second
	claimPollInterval  = 500 * time.Millisecond
)

type Repository interface {
	AuthenticatePrivateAgent(context.Context, string, string, string) (store.PrivateAgent, error)
	ClaimPrivateAgentDeliveries(context.Context, string, string, int, time.Duration) ([]store.DeliveryLease, error)
	CompleteDeliveryAttempt(context.Context, store.CompleteDeliveryParams) error
}

type Handler struct {
	repository Repository
	now        func() time.Time
}

func NewHandler(repository Repository) (*Handler, error) {
	if repository == nil {
		return nil, errors.New("private agent: repository is required")
	}
	return &Handler{repository: repository, now: func() time.Time { return time.Now().UTC() }}, nil
}

type ClaimRequest struct {
	Limit       int `json:"limit"`
	WaitSeconds int `json:"wait_seconds"`
}

type WorkItem struct {
	DeliveryID     string                `json:"delivery_id"`
	AttemptNumber  int                   `json:"attempt_number"`
	LeaseToken     string                `json:"lease_token"`
	LeaseUntil     time.Time             `json:"lease_until"`
	Lane           store.DeliveryLane    `json:"lane"`
	EndpointID     string                `json:"endpoint_id"`
	EndpointConfig json.RawMessage       `json:"endpoint_config"`
	KeyID          string                `json:"key_id"`
	SecretVersion  int                   `json:"secret_version"`
	Event          notify.CanonicalEvent `json:"event"`
}

type ClaimResponse struct {
	Deliveries []WorkItem `json:"deliveries"`
}

type CompleteRequest struct {
	DeliveryID        string `json:"delivery_id"`
	AttemptNumber     int    `json:"attempt_number"`
	LeaseToken        string `json:"lease_token"`
	Outcome           string `json:"outcome"`
	HTTPStatus        int    `json:"http_status,omitempty"`
	ProviderMessageID string `json:"provider_message_id,omitempty"`
	ErrorSummary      string `json:"error_summary,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "private-agents" {
		http.NotFound(response, request)
		return
	}
	agentID, operation := parts[2], parts[3]
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	agent, err := h.repository.AuthenticatePrivateAgent(request.Context(), agentID, token, request.Header.Get("X-Statusmon-Agent-Version"))
	if err != nil || !agent.Enabled {
		http.Error(response, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch operation {
	case "claim":
		h.claim(response, request, agent)
	case "complete":
		h.complete(response, request, agent)
	default:
		http.NotFound(response, request)
	}
}

func (h *Handler) claim(response http.ResponseWriter, request *http.Request, agent store.PrivateAgent) {
	var input ClaimRequest
	if err := decodeBody(response, request, &input); err != nil {
		http.Error(response, "invalid claim request", http.StatusBadRequest)
		return
	}
	if input.Limit == 0 {
		input.Limit = 16
	}
	if input.Limit < 1 || input.Limit > 100 || input.WaitSeconds < 0 || input.WaitSeconds > int(maximumLongPoll/time.Second) {
		http.Error(response, "invalid claim limits", http.StatusBadRequest)
		return
	}
	deadline := h.now().Add(time.Duration(input.WaitSeconds) * time.Second)
	for {
		leases, err := h.repository.ClaimPrivateAgentDeliveries(request.Context(), agent.ID, "private-agent/"+agent.ID, input.Limit, 30*time.Second)
		if err != nil {
			http.Error(response, "claim failed", http.StatusInternalServerError)
			return
		}
		if len(leases) > 0 || !h.now().Before(deadline) {
			items, err := workItems(leases)
			if err != nil {
				http.Error(response, "invalid endpoint configuration", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Cache-Control", "no-store")
			response.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(response).Encode(ClaimResponse{Deliveries: items})
			return
		}
		timer := time.NewTimer(claimPollInterval)
		select {
		case <-request.Context().Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (h *Handler) complete(response http.ResponseWriter, request *http.Request, agent store.PrivateAgent) {
	var input CompleteRequest
	if err := decodeBody(response, request, &input); err != nil || input.DeliveryID == "" || input.LeaseToken == "" || input.AttemptNumber <= 0 {
		http.Error(response, "invalid completion request", http.StatusBadRequest)
		return
	}
	if input.HTTPStatus < 0 || input.HTTPStatus > 599 || input.RetryAfterSeconds < 0 || input.RetryAfterSeconds > 3600 {
		http.Error(response, "invalid completion metadata", http.StatusBadRequest)
		return
	}
	params := store.CompleteDeliveryParams{DeliveryID: input.DeliveryID, LeaseToken: input.LeaseToken,
		AttemptNumber: input.AttemptNumber, ProviderMessageID: input.ProviderMessageID,
		HTTPStatus: input.HTTPStatus, FinishedAt: h.now(), AgentID: agent.ID}
	switch input.Outcome {
	case "accepted":
		params.Status = "accepted"
	case "retryable":
		params.Status, params.ErrorClass = "retry_wait", string(notify.ErrorClassRetryable)
		delay := time.Duration(input.RetryAfterSeconds) * time.Second
		if delay == 0 {
			delay = 5 * time.Second
		}
		retryAt := h.now().Add(delay)
		params.RetryAt = &retryAt
	case "permanent":
		params.Status, params.ErrorClass = "failed", string(notify.ErrorClassPermanent)
	default:
		http.Error(response, "invalid completion outcome", http.StatusBadRequest)
		return
	}
	params.ErrorSummary = strings.TrimSpace(input.ErrorSummary)
	if len(params.ErrorSummary) > 1024 {
		params.ErrorSummary = params.ErrorSummary[:1024]
	}
	if err := h.repository.CompleteDeliveryAttempt(request.Context(), params); err != nil {
		http.Error(response, "completion rejected", http.StatusConflict)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func workItems(leases []store.DeliveryLease) ([]WorkItem, error) {
	items := make([]WorkItem, 0, len(leases))
	for _, lease := range leases {
		if !json.Valid(lease.EncryptedConfig) {
			return nil, errors.New("private agent: endpoint config is not JSON")
		}
		items = append(items, WorkItem{DeliveryID: lease.ID, AttemptNumber: lease.AttemptNumber,
			LeaseToken: lease.LeaseToken, LeaseUntil: lease.LeaseUntil, Lane: lease.Lane,
			EndpointID: lease.EndpointID, EndpointConfig: append([]byte(nil), lease.EncryptedConfig...),
			KeyID: lease.KeyID, SecretVersion: lease.SecretVersion,
			Event: notify.CanonicalEvent{ID: domain.CanonicalEventID(lease.EventID), Source: lease.EventSource,
				Kind: lease.EventKind, Subject: eventSubject(lease.EventPayload, lease.EventKind), EntityID: lease.EventEntityID,
				Time: lease.EventObservedAt, Revision: lease.EventRevision, SchemaVersion: lease.EventSchemaVersion,
				Summary: eventSummary(lease.EventPayload), Data: lease.EventPayload},
		})
	}
	return items, nil
}

func eventSubject(payload json.RawMessage, kind domain.EventKind) string {
	var value struct {
		Current struct {
			Name string `json:"name"`
		} `json:"current"`
	}
	if json.Unmarshal(payload, &value) == nil && strings.TrimSpace(value.Current.Name) != "" {
		return value.Current.Name
	}
	return string(kind)
}

func eventSummary(payload json.RawMessage) string {
	var value struct {
		Current struct {
			Name        string `json:"name"`
			Phase       string `json:"phase"`
			Impact      string `json:"impact"`
			Description string `json:"description"`
		} `json:"current"`
	}
	if json.Unmarshal(payload, &value) != nil {
		return string(payload)
	}
	return strings.Trim(strings.Join([]string{value.Current.Name, value.Current.Phase, value.Current.Impact, value.Current.Description}, " — "), " —")
}

func decodeBody(response http.ResponseWriter, request *http.Request, target any) error {
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximumRequestBody))
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("private agent: trailing JSON")
	}
	return nil
}

func bearerToken(header string) (string, bool) {
	prefix := "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return token, token != ""
}

func ParseRetryAfter(response *http.Response) int {
	if response == nil {
		return 0
	}
	seconds, _ := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After")))
	if seconds < 0 || seconds > 3600 {
		return 0
	}
	return seconds
}
