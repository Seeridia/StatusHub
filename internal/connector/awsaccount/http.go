package awsaccount

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/callback"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

const maximumBodyBytes = 1 << 20

type Repository interface {
	LoadAWSAccountConnector(context.Context, string) (store.AWSAccountConnector, error)
	CommitConnectorEvent(context.Context, store.CommitConnectorEventParams) (store.ConnectorCommitResult, error)
}

type SNSVerifier interface {
	Verify(context.Context, []byte) (callback.SNSEnvelope, error)
	ConfirmSubscription(context.Context, callback.SNSEnvelope) error
}

type Handler struct {
	repository Repository
	verifier   SNSVerifier
	now        func() time.Time
}

func NewHandler(repository Repository, verifier SNSVerifier) (*Handler, error) {
	if repository == nil || verifier == nil {
		return nil, errors.New("aws account health: repository and SNS verifier are required")
	}
	return &Handler{repository: repository, verifier: verifier, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "connectors" || parts[2] != "aws-health" {
		http.NotFound(response, request)
		return
	}
	connector, err := h.repository.LoadAWSAccountConnector(request.Context(), parts[3])
	if err != nil || !connector.Enabled {
		http.Error(response, "connector not found", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximumBodyBytes))
	if err != nil {
		http.Error(response, "invalid body", http.StatusBadRequest)
		return
	}
	envelope, err := h.verifier.Verify(request.Context(), body)
	if err != nil {
		http.Error(response, "invalid signature", http.StatusUnauthorized)
		return
	}
	if envelope.TopicARN != connector.SNSTopicARN {
		http.Error(response, "topic not allowed", http.StatusUnauthorized)
		return
	}
	if envelope.Type == "SubscriptionConfirmation" {
		if err := h.verifier.ConfirmSubscription(request.Context(), envelope); err != nil {
			http.Error(response, "subscription confirmation failed", http.StatusBadGateway)
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if envelope.Type != "Notification" {
		http.Error(response, "unsupported SNS message type", http.StatusBadRequest)
		return
	}
	event, subject, err := Decode([]byte(envelope.Message), Config{
		SourceID: connector.SourceID, ExternalAccountID: connector.ExternalAccountID,
		AllowedRegions: connector.AllowedRegions, AllowedServices: connector.AllowedServices,
	}, h.now())
	if errors.Is(err, ErrFiltered) {
		response.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(response, "invalid AWS Health event", http.StatusBadRequest)
		return
	}
	semanticHash, err := domain.SemanticHash(NormalizerVersion, json.RawMessage(event.Payload))
	if err != nil {
		http.Error(response, "invalid AWS Health projection", http.StatusBadRequest)
		return
	}
	if _, err := h.repository.CommitConnectorEvent(request.Context(), store.CommitConnectorEventParams{
		Event: event, Subject: subject, SemanticHash: semanticHash,
	}); err != nil {
		http.Error(response, "connector persistence failed", http.StatusInternalServerError)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
