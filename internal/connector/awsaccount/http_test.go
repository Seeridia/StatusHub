package awsaccount

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/callback"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type fakeRepository struct {
	connector store.AWSAccountConnector
	commits   []store.CommitConnectorEventParams
}

func (f *fakeRepository) LoadAWSAccountConnector(context.Context, string) (store.AWSAccountConnector, error) {
	return f.connector, nil
}
func (f *fakeRepository) CommitConnectorEvent(_ context.Context, params store.CommitConnectorEventParams) (store.ConnectorCommitResult, error) {
	f.commits = append(f.commits, params)
	return store.ConnectorCommitResult{Inserted: true}, nil
}

type fakeVerifier struct {
	envelope  callback.SNSEnvelope
	confirmed bool
}

func (f *fakeVerifier) Verify(context.Context, []byte) (callback.SNSEnvelope, error) {
	return f.envelope, nil
}
func (f *fakeVerifier) ConfirmSubscription(context.Context, callback.SNSEnvelope) error {
	f.confirmed = true
	return nil
}

func TestHandlerPersistsVerifiedTopic(t *testing.T) {
	topic := "arn:aws:sns:us-east-1:123456789012:health"
	repository := &fakeRepository{connector: store.AWSAccountConnector{ID: "connector", SourceID: "source",
		ExternalAccountID: "123456789012", SNSTopicARN: topic, Enabled: true}}
	verifier := &fakeVerifier{envelope: callback.SNSEnvelope{Type: "Notification", TopicARN: topic, Message: testEvent}}
	handler, _ := NewHandler(repository, verifier)
	request := httptest.NewRequest(http.MethodPost, "/v1/connectors/aws-health/connector", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || len(repository.commits) != 1 {
		t.Fatalf("status=%d commits=%d body=%q", response.Code, len(repository.commits), response.Body.String())
	}
}

func TestHandlerConfirmsOnlyBoundTopic(t *testing.T) {
	topic := "arn:aws:sns:us-east-1:123456789012:health"
	repository := &fakeRepository{connector: store.AWSAccountConnector{ID: "connector", SourceID: "source",
		ExternalAccountID: "123456789012", SNSTopicARN: topic, Enabled: true}}
	verifier := &fakeVerifier{envelope: callback.SNSEnvelope{Type: "SubscriptionConfirmation", TopicARN: topic}}
	handler, _ := NewHandler(repository, verifier)
	request := httptest.NewRequest(http.MethodPost, "/v1/connectors/aws-health/connector", strings.NewReader(`{}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !verifier.confirmed {
		t.Fatalf("status=%d confirmed=%v", response.Code, verifier.confirmed)
	}

	verifier.envelope.TopicARN = "arn:aws:sns:us-east-1:123456789012:other"
	verifier.confirmed = false
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || verifier.confirmed {
		t.Fatalf("mismatched topic status=%d confirmed=%v", response.Code, verifier.confirmed)
	}
}
