package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/notify"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/secret"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type fakeDeliveryStore struct {
	mutex       sync.Mutex
	leases      []store.DeliveryLease
	done        []store.CompleteDeliveryParams
	claimedLane store.DeliveryLane
	claimLimit  int
}

func TestEnvelopeConfigDecoder(t *testing.T) {
	envelope, err := secret.NewStaticEnvelope("test-v1", make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	lease := deliveryLease()
	lease.KeyID = envelope.KeyID()
	lease.SecretVersion = 2
	lease.EncryptedConfig, err = envelope.Seal(context.Background(), []byte(`{"url":"https://hooks.slack.com/services/test"}`), secret.EndpointAssociatedData(lease.EndpointID, lease.SecretVersion))
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := (EnvelopeConfigDecoder{Opener: envelope}).Decode(context.Background(), lease)
	if err != nil || endpoint.URL != "https://hooks.slack.com/services/test" {
		t.Fatalf("endpoint=%#v err=%v", endpoint, err)
	}
	lease.EndpointID = "another-endpoint"
	if _, err := (EnvelopeConfigDecoder{Opener: envelope}).Decode(context.Background(), lease); err == nil {
		t.Fatal("expected associated-data rejection")
	}
}

func (f *fakeDeliveryStore) ClaimDeliveryLane(_ context.Context, _ string, lane store.DeliveryLane, limit, _ int, _ time.Duration) ([]store.DeliveryLease, error) {
	f.claimedLane = lane
	f.claimLimit = limit
	return f.leases, nil
}
func (f *fakeDeliveryStore) CompleteDeliveryAttempt(_ context.Context, value store.CompleteDeliveryParams) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.done = append(f.done, value)
	return nil
}

type stubDriver struct{ sendErr error }

func (d stubDriver) Validate(context.Context, notify.Endpoint) error { return nil }
func (d stubDriver) Render(_ context.Context, event notify.CanonicalEvent, _ notify.Endpoint) (notify.Payload, error) {
	return notify.Payload{Channel: notify.ChannelSlack, EventID: event.ID, Body: []byte(`{}`), ContentType: "application/json"}, nil
}
func (d stubDriver) Send(_ context.Context, delivery notify.Delivery, payload notify.Payload) (notify.Receipt, error) {
	if d.sendErr != nil {
		return notify.Receipt{}, d.sendErr
	}
	return notify.Receipt{Status: notify.StatusProviderAccepted, ProviderMessageID: "provider"}, nil
}
func (d stubDriver) Classify(err error) notify.RetryDecision { return notify.ClassifyError(err) }

func deliveryLease() store.DeliveryLease {
	return store.DeliveryLease{ID: "delivery", EventID: "event", EndpointID: "endpoint", Channel: "slack",
		EncryptedConfig: []byte(`{"url":"https://hooks.slack.com/services/test"}`), AttemptNumber: 1, LeaseToken: "token",
		EligibleAt: time.Now().Add(-time.Second), EventSource: "https://status.example.test", EventKind: domain.EventKindIncidentCreated,
		EventEntityID: "incident", EventRevision: 1, EventSchemaVersion: "v1", EventPayload: json.RawMessage(`{"current":{"name":"API","impact":"major"}}`), EventObservedAt: time.Now()}
}

func TestWorkerCommitsProviderAcceptance(t *testing.T) {
	repository := &fakeDeliveryStore{leases: []store.DeliveryLease{deliveryLease()}}
	worker, err := New(repository, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{})}, JSONConfigDecoder{}, nil, DefaultConfig("test"))
	if err != nil {
		t.Fatal(err)
	}
	stats, err := worker.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Accepted != 1 || len(repository.done) != 1 || repository.done[0].Status != "accepted" || repository.done[0].ProviderMessageID != "provider" {
		t.Fatalf("stats=%#v done=%#v", stats, repository.done)
	}
	if repository.claimedLane != store.DeliveryLaneBulk || repository.claimLimit != 16 {
		t.Fatalf("claim lane=%s limit=%d", repository.claimedLane, repository.claimLimit)
	}
}

func TestWorkerSchedulesRetryableFailure(t *testing.T) {
	repository := &fakeDeliveryStore{leases: []store.DeliveryLease{deliveryLease()}}
	failure := &notify.Error{Channel: notify.ChannelSlack, Operation: "send", Class: notify.ErrorClassRetryable, Err: errors.New("temporary")}
	worker, err := New(repository, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{sendErr: failure})}, JSONConfigDecoder{}, nil, DefaultConfig("test"))
	if err != nil {
		t.Fatal(err)
	}
	stats, err := worker.RunOnce(context.Background())
	if err == nil || stats.Retried != 1 || len(repository.done) != 1 || repository.done[0].Status != "retry_wait" || repository.done[0].RetryAt == nil {
		t.Fatalf("stats=%#v done=%#v err=%v", stats, repository.done, err)
	}
}

func TestWorkerPersistsProviderErrorMetadata(t *testing.T) {
	repository := &fakeDeliveryStore{leases: []store.DeliveryLease{deliveryLease()}}
	failure := &notify.Error{Channel: notify.ChannelSlack, Operation: "send", Class: notify.ErrorClassPermanent,
		StatusCode: 400, ProviderCode: "invalid_payload", Err: errors.New("rejected")}
	worker, err := New(repository, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{sendErr: failure})}, JSONConfigDecoder{}, nil, DefaultConfig("test"))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = worker.RunOnce(context.Background())
	if len(repository.done) != 1 || repository.done[0].HTTPStatus != 400 || repository.done[0].ProviderCode != "invalid_payload" {
		t.Fatalf("completion=%#v", repository.done)
	}
}

func TestWorkerDeadLettersRetryableFailureAtAttemptLimit(t *testing.T) {
	lease := deliveryLease()
	lease.AttemptNumber = 3
	repository := &fakeDeliveryStore{leases: []store.DeliveryLease{lease}}
	failure := &notify.Error{Channel: notify.ChannelSlack, Operation: "send", Class: notify.ErrorClassRetryable, Err: errors.New("still unavailable")}
	config := DefaultConfig("test")
	config.MaxAttempts = 3
	worker, err := New(repository, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{sendErr: failure})}, JSONConfigDecoder{}, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := worker.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected the provider failure to be returned")
	}
	if stats.DeadLettered != 1 || stats.Retried != 0 || len(repository.done) != 1 {
		t.Fatalf("stats=%#v done=%#v", stats, repository.done)
	}
	completion := repository.done[0]
	if completion.Status != "dead_letter" || completion.RetryAt != nil || completion.DeadLetterReason == "" {
		t.Fatalf("completion=%#v", completion)
	}
}

func TestAdaptiveConcurrencyIsIsolatedPerWorker(t *testing.T) {
	acceptedStore := &fakeDeliveryStore{}
	for index := 0; index < 4; index++ {
		lease := deliveryLease()
		lease.ID = fmt.Sprintf("delivery-%d", index)
		acceptedStore.leases = append(acceptedStore.leases, lease)
	}
	config := DefaultConfig("critical")
	config.Lane = store.DeliveryLaneCritical
	config.MinConcurrency, config.InitialConcurrency, config.MaxConcurrency = 1, 2, 4
	config.BatchSize, config.ClaimMultiplier = 4, 2
	worker, err := New(acceptedStore, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{})}, JSONConfigDecoder{}, nil, config)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := worker.RunOnce(context.Background())
	if err != nil || stats.Concurrency != 2 || stats.NextConcurrency != 3 {
		t.Fatalf("accepted adaptive stats=%#v err=%v", stats, err)
	}

	retryStore := &fakeDeliveryStore{leases: []store.DeliveryLease{deliveryLease()}}
	retryConfig := config
	retryConfig.Owner = "retry"
	retryConfig.Lane = store.DeliveryLaneRetry
	retryConfig.InitialConcurrency = 4
	retryWorker, err := New(retryStore, []notify.ChannelDriver{Bind(notify.ChannelSlack, stubDriver{sendErr: &notify.Error{
		Channel: notify.ChannelSlack, Operation: "send", Class: notify.ErrorClassRetryable, Err: errors.New("limited"),
	}})}, JSONConfigDecoder{}, nil, retryConfig)
	if err != nil {
		t.Fatal(err)
	}
	stats, err = retryWorker.RunOnce(context.Background())
	if err == nil || stats.Concurrency != 4 || stats.NextConcurrency != 2 || retryStore.claimedLane != store.DeliveryLaneRetry {
		t.Fatalf("retry adaptive stats=%#v lane=%s err=%v", stats, retryStore.claimedLane, err)
	}
	if worker.currentLimit() != 3 {
		t.Fatalf("critical worker limit changed by retry lane: %d", worker.currentLimit())
	}
}
