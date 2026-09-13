package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/notify"
	notifierpipeline "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/pipeline/notifier"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type EndpointTestRepository interface {
	ClaimEndpointTests(context.Context, string, int, time.Duration) ([]store.EndpointTestLease, error)
	CompleteEndpointTest(context.Context, store.CompleteEndpointTestParams) error
}

type EndpointTestWorker struct {
	repository EndpointTestRepository
	decoder    notifierpipeline.ConfigDecoder
	drivers    map[notify.Channel]notify.ChannelDriver
	owner      string
	timeout    time.Duration
}

func NewEndpointTestWorker(repository EndpointTestRepository, decoder notifierpipeline.ConfigDecoder,
	drivers map[notify.Channel]notify.ChannelDriver, owner string, timeout time.Duration) (*EndpointTestWorker, error) {
	if repository == nil || decoder == nil || len(drivers) == 0 || owner == "" || timeout <= 0 {
		return nil, errors.New("controlplane: endpoint test worker dependencies are invalid")
	}
	return &EndpointTestWorker{repository: repository, decoder: decoder, drivers: drivers, owner: owner, timeout: timeout}, nil
}

func (w *EndpointTestWorker) RunOnce(ctx context.Context) (int, error) {
	leases, err := w.repository.ClaimEndpointTests(ctx, w.owner, 16, 30*time.Second)
	if err != nil {
		return 0, err
	}
	var failures []error
	for _, lease := range leases {
		if err := w.process(ctx, lease); err != nil {
			failures = append(failures, err)
		}
	}
	return len(leases), errors.Join(failures...)
}

func (w *EndpointTestWorker) process(parent context.Context, lease store.EndpointTestLease) error {
	deliveryLease := store.DeliveryLease{EndpointID: lease.EndpointID, Channel: lease.Channel,
		EncryptedConfig: lease.EncryptedConfig, KeyID: lease.KeyID, SecretVersion: lease.SecretVersion}
	endpoint, err := w.decoder.Decode(parent, deliveryLease)
	if err != nil {
		return w.completeFailure(parent, lease, nil, err)
	}
	driver := w.drivers[endpoint.Channel]
	if driver == nil {
		return w.completeFailure(parent, lease, nil, fmt.Errorf("endpoint testing is unsupported for channel %s", endpoint.Channel))
	}
	ctx, cancel := context.WithTimeout(parent, w.timeout)
	defer cancel()
	eventID := domain.CanonicalEventID("endpoint-test-" + lease.ID)
	event := notify.CanonicalEvent{ID: eventID, Source: "statusmon://endpoint-test",
		Kind: domain.EventKindIncidentCreated, Subject: "StatusMon endpoint test",
		EntityID: "endpoint-test", Time: time.Now().UTC(), Revision: 1, SchemaVersion: "v1",
		Summary: "This is a test notification from Vendor Status Monitoring.",
		Data:    json.RawMessage(`{"current":{"name":"StatusMon endpoint test","impact":"minor"}}`)}
	payload, err := driver.Render(ctx, event, endpoint)
	if err != nil {
		return w.completeFailure(parent, lease, driver, err)
	}
	receipt, err := driver.Send(ctx, notify.Delivery{ID: lease.ID, EventID: eventID, Endpoint: endpoint}, payload)
	if err != nil {
		return w.completeFailure(parent, lease, driver, err)
	}
	return w.repository.CompleteEndpointTest(parent, store.CompleteEndpointTestParams{ID: lease.ID,
		LeaseToken: lease.LeaseToken, Succeeded: true, ProviderMessageID: receipt.ProviderMessageID,
		HTTPStatus: receipt.HTTPStatus})
}

func (w *EndpointTestWorker) completeFailure(ctx context.Context, lease store.EndpointTestLease, driver notify.ChannelDriver, failure error) error {
	decision := notify.RetryDecision{Class: notify.ErrorClassPermanent}
	if driver != nil {
		decision = driver.Classify(failure)
	}
	class := string(decision.Class)
	if class == "" {
		class = string(notify.ErrorClassPermanent)
	}
	completionErr := w.repository.CompleteEndpointTest(ctx, store.CompleteEndpointTestParams{ID: lease.ID,
		LeaseToken: lease.LeaseToken, ErrorClass: class, ErrorSummary: failure.Error()})
	return errors.Join(failure, completionErr)
}
