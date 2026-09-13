package controlplane

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/notify"
	notifierpipeline "github.com/Seeridia/StatusHub/internal/pipeline/notifier"
	"github.com/Seeridia/StatusHub/internal/secret"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type endpointTestRepositoryStub struct {
	lease      store.EndpointTestLease
	completion store.CompleteEndpointTestParams
}

func (r *endpointTestRepositoryStub) ClaimEndpointTests(context.Context, string, int, time.Duration) ([]store.EndpointTestLease, error) {
	return []store.EndpointTestLease{r.lease}, nil
}
func (r *endpointTestRepositoryStub) CompleteEndpointTest(_ context.Context, completion store.CompleteEndpointTestParams) error {
	r.completion = completion
	return nil
}

type endpointTestDriver struct{}

func (endpointTestDriver) Validate(context.Context, notify.Endpoint) error { return nil }
func (endpointTestDriver) Render(_ context.Context, event notify.CanonicalEvent, _ notify.Endpoint) (notify.Payload, error) {
	return notify.Payload{Channel: notify.ChannelSlack, EventID: event.ID, ContentType: "application/json", Body: []byte(`{}`)}, nil
}
func (endpointTestDriver) Send(_ context.Context, _ notify.Delivery, _ notify.Payload) (notify.Receipt, error) {
	return notify.Receipt{Status: notify.StatusProviderAccepted, HTTPStatus: 200, ProviderMessageID: "message-1"}, nil
}
func (endpointTestDriver) Classify(error) notify.RetryDecision { return notify.RetryDecision{} }

func TestEndpointTestWorkerDecryptsAndPersistsReceipt(t *testing.T) {
	envelope, err := secret.NewStaticEnvelope("test-v1", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	endpointID := "70000000-0000-0000-0000-000000000001"
	ciphertext, err := envelope.Seal(context.Background(), []byte(`{"url":"https://hooks.slack.com/services/test"}`), secret.EndpointAssociatedData(endpointID, 1))
	if err != nil {
		t.Fatal(err)
	}
	repository := &endpointTestRepositoryStub{lease: store.EndpointTestLease{EndpointTestJob: store.EndpointTestJob{
		ID: "71000000-0000-0000-0000-000000000001", EndpointID: endpointID}, LeaseToken: "lease",
		Channel: string(notify.ChannelSlack), KeyID: envelope.KeyID(), SecretVersion: 1, EncryptedConfig: ciphertext}}
	worker, err := NewEndpointTestWorker(repository, notifierpipeline.EnvelopeConfigDecoder{Opener: envelope},
		map[notify.Channel]notify.ChannelDriver{notify.ChannelSlack: endpointTestDriver{}}, "worker", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	count, err := worker.RunOnce(context.Background())
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if !repository.completion.Succeeded || repository.completion.HTTPStatus != 200 || repository.completion.ProviderMessageID != "message-1" {
		encoded, _ := json.Marshal(repository.completion)
		t.Fatalf("completion=%s", encoded)
	}
}
