package privateagent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type fakeRepository struct {
	authToken  string
	leases     []store.DeliveryLease
	completion store.CompleteDeliveryParams
}

func (f *fakeRepository) AuthenticatePrivateAgent(_ context.Context, id, token, _ string) (store.PrivateAgent, error) {
	f.authToken = token
	return store.PrivateAgent{ID: id, Enabled: true}, nil
}
func (f *fakeRepository) ClaimPrivateAgentDeliveries(context.Context, string, string, int, time.Duration) ([]store.DeliveryLease, error) {
	return f.leases, nil
}
func (f *fakeRepository) CompleteDeliveryAttempt(_ context.Context, params store.CompleteDeliveryParams) error {
	f.completion = params
	return nil
}

func TestClaimAndCompleteProtocol(t *testing.T) {
	repository := &fakeRepository{leases: []store.DeliveryLease{{
		ID: "delivery-1", EventID: "event-1", EndpointID: "endpoint-1", AttemptNumber: 2,
		LeaseToken: "lease-token", LeaseUntil: time.Now().Add(time.Minute), Lane: store.DeliveryLaneRetry,
		EncryptedConfig: []byte(`{"url":"http://internal/status","secret":"secret"}`), KeyID: "key-1", SecretVersion: 2,
		EventSource: "https://status.example.test", EventKind: domain.EventKindIncidentUpdated,
		EventEntityID: "incident-1", EventRevision: 2, EventSchemaVersion: "v1",
		EventPayload: []byte(`{"current":{"name":"API","impact":"major"}}`), EventObservedAt: time.Now(),
	}}}
	handler, _ := NewHandler(repository)
	request := httptest.NewRequest(http.MethodPost, "/v1/private-agents/agent-1/claim", strings.NewReader(`{"limit":1}`))
	request.Header.Set("Authorization", "Bearer token-value")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || repository.authToken != "token-value" {
		t.Fatalf("claim status=%d body=%q", response.Code, response.Body.String())
	}
	var claimed ClaimResponse
	if err := json.Unmarshal(response.Body.Bytes(), &claimed); err != nil || len(claimed.Deliveries) != 1 || claimed.Deliveries[0].Lane != store.DeliveryLaneRetry {
		t.Fatalf("claim=%#v err=%v", claimed, err)
	}

	request = httptest.NewRequest(http.MethodPost, "/v1/private-agents/agent-1/complete", strings.NewReader(
		`{"delivery_id":"delivery-1","attempt_number":2,"lease_token":"lease-token","outcome":"retryable","http_status":503,"retry_after_seconds":10}`))
	request.Header.Set("Authorization", "Bearer token-value")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || repository.completion.AgentID != "agent-1" ||
		repository.completion.Status != "retry_wait" || repository.completion.RetryAt == nil {
		t.Fatalf("complete status=%d params=%#v body=%q", response.Code, repository.completion, response.Body.String())
	}
}

func TestPrivateAgentRequiresBearerToken(t *testing.T) {
	handler, _ := NewHandler(&fakeRepository{})
	request := httptest.NewRequest(http.MethodPost, "/v1/private-agents/agent/claim", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
}
