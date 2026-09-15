// Package notifier owns delivery leases, rendering, retry policy and durable
// attempt bookkeeping. Channel drivers perform exactly one provider attempt.
package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/notify"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

type Store interface {
	ClaimDeliveryLane(context.Context, string, store.DeliveryLane, int, int, time.Duration) ([]store.DeliveryLease, error)
	CompleteDeliveryAttempt(context.Context, store.CompleteDeliveryParams) error
}

type Observer interface {
	RecordNotification(context.Context, string, string, time.Duration, time.Duration)
	RecordEligibleFirstAttempt(context.Context, string, time.Duration)
}

type laneObserver interface {
	RecordDeliveryLane(context.Context, string, int, int)
}

type ConfigDecoder interface {
	Decode(context.Context, store.DeliveryLease) (notify.Endpoint, error)
}

type JSONConfigDecoder struct{}

type ConfigOpener interface {
	Open(context.Context, []byte, []byte, string) ([]byte, error)
}

// EnvelopeConfigDecoder decrypts endpoint configuration using an injected
// key-management boundary. This keeps provider drivers unaware of storage
// encryption and binds ciphertext to the endpoint ID and secret version.
type EnvelopeConfigDecoder struct {
	Opener ConfigOpener
}

type endpointConfig struct {
	URL              string `json:"url"`
	Secret           string `json:"secret"`
	SigningKeyID     string `json:"signing_key_id"`
	AccountSID       string `json:"account_sid"`
	From             string `json:"from"`
	To               string `json:"to"`
	ConfigurationSet string `json:"configuration_set"`
	CallbackURL      string `json:"callback_url"`
	MaxPayloadBytes  int    `json:"max_payload_bytes"`
}

func (JSONConfigDecoder) Decode(_ context.Context, lease store.DeliveryLease) (notify.Endpoint, error) {
	return decodeEndpointConfig(lease, lease.EncryptedConfig)
}

func (decoder EnvelopeConfigDecoder) Decode(ctx context.Context, lease store.DeliveryLease) (notify.Endpoint, error) {
	if decoder.Opener == nil {
		return notify.Endpoint{}, errors.New("notifier: endpoint config opener is required")
	}
	plaintext, err := decoder.Opener.Open(ctx, lease.EncryptedConfig, endpointAssociatedData(lease.EndpointID, lease.SecretVersion), lease.KeyID)
	if err != nil {
		return notify.Endpoint{}, errors.New("notifier: endpoint configuration cannot be decrypted")
	}
	defer clear(plaintext)
	return decodeEndpointConfig(lease, plaintext)
}

func decodeEndpointConfig(lease store.DeliveryLease, raw []byte) (notify.Endpoint, error) {
	var config endpointConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return notify.Endpoint{}, errors.New("notifier: endpoint configuration cannot be decoded")
	}
	if config.URL == "" && lease.Channel == string(notify.ChannelPagerDuty) {
		config.URL = notify.PagerDutyEventsURL
	}
	if config.URL == "" && lease.Channel == string(notify.ChannelTwilioSMS) && validPathToken(config.AccountSID) {
		config.URL = "https://api.twilio.com/2010-04-01/Accounts/" + config.AccountSID + "/Messages.json"
	}
	signingKeyID := config.SigningKeyID
	if signingKeyID == "" {
		signingKeyID = lease.KeyID
	}
	return notify.Endpoint{ID: lease.EndpointID, Channel: notify.Channel(lease.Channel), URL: config.URL,
		KeyID: signingKeyID, Secret: []byte(config.Secret), MaxPayloadBytes: config.MaxPayloadBytes,
		AccountSID: config.AccountSID, From: config.From, To: config.To,
		ConfigurationSet: config.ConfigurationSet, CallbackURL: config.CallbackURL}, nil
}

func endpointAssociatedData(endpointID string, version int) []byte {
	return []byte(fmt.Sprintf("statushub.endpoint.v1:%s:%d", endpointID, version))
}

func validPathToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			continue
		}
		return false
	}
	return true
}

type Config struct {
	Owner              string
	Lane               store.DeliveryLane
	BatchSize          int
	PerTenant          int
	MinConcurrency     int
	InitialConcurrency int
	MaxConcurrency     int
	ClaimMultiplier    int
	LeaseDuration      time.Duration
	AttemptTimeout     time.Duration
	RetryBase          time.Duration
	RetryMaximum       time.Duration
	MaxAttempts        int
}

func DefaultConfig(owner string) Config {
	return Config{Owner: owner, Lane: store.DeliveryLaneBulk, BatchSize: 64, PerTenant: 8,
		MinConcurrency: 2, InitialConcurrency: 4, MaxConcurrency: 16, ClaimMultiplier: 4,
		LeaseDuration: 30 * time.Second, AttemptTimeout: 10 * time.Second,
		RetryBase: time.Second, RetryMaximum: 15 * time.Minute, MaxAttempts: 10}
}

// DefaultLaneConfigs reserves independent worker capacity for urgent first
// attempts, retries, and bulk first attempts. Each worker adapts only within
// its own bounds, so a throttled provider in the retry lane cannot shrink the
// critical lane.
func DefaultLaneConfigs(owner string) []Config {
	critical := DefaultConfig(owner + "/critical")
	critical.Lane = store.DeliveryLaneCritical
	critical.BatchSize, critical.PerTenant = 128, 16
	critical.MinConcurrency, critical.InitialConcurrency, critical.MaxConcurrency = 8, 16, 32

	retry := DefaultConfig(owner + "/retry")
	retry.Lane = store.DeliveryLaneRetry
	retry.BatchSize, retry.PerTenant = 32, 4
	retry.MinConcurrency, retry.InitialConcurrency, retry.MaxConcurrency = 1, 2, 8

	bulk := DefaultConfig(owner + "/bulk")
	bulk.Lane = store.DeliveryLaneBulk
	return []Config{critical, retry, bulk}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.Owner) == "" || !c.Lane.Valid() || c.BatchSize <= 0 || c.PerTenant <= 0 ||
		c.MinConcurrency <= 0 || c.InitialConcurrency < c.MinConcurrency ||
		c.MaxConcurrency < c.InitialConcurrency || c.MaxConcurrency > c.BatchSize || c.ClaimMultiplier <= 0 {
		return errors.New("notifier: invalid owner or concurrency limits")
	}
	if c.LeaseDuration <= 0 || c.AttemptTimeout <= 0 || c.AttemptTimeout >= c.LeaseDuration ||
		c.RetryBase <= 0 || c.RetryMaximum < c.RetryBase || c.MaxAttempts <= 0 {
		return errors.New("notifier: invalid lease, timeout, or retry policy")
	}
	return nil
}

type Worker struct {
	store    Store
	drivers  map[notify.Channel]notify.ChannelDriver
	decoder  ConfigDecoder
	observer Observer
	config   Config
	now      func() time.Time
	random   *rand.Rand
	randomMu sync.Mutex
	limitMu  sync.Mutex
	limit    int
}

func New(repository Store, drivers []notify.ChannelDriver, decoder ConfigDecoder, observer Observer, config Config) (*Worker, error) {
	if repository == nil || decoder == nil {
		return nil, errors.New("notifier: store and endpoint config decoder are required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	registry := make(map[notify.Channel]notify.ChannelDriver)
	for _, driver := range drivers {
		if driver == nil {
			continue
		}
		// Drivers reject a mismatched channel during Validate, so explicit
		// registration is supplied through the small channelDriver wrapper.
		for _, channel := range []notify.Channel{
			notify.ChannelGenericWebhook, notify.ChannelSlack, notify.ChannelEmailSES,
			notify.ChannelPagerDuty, notify.ChannelTwilioSMS, notify.ChannelTeams,
			notify.ChannelDiscord, notify.ChannelTelegram, notify.ChannelLark,
			notify.ChannelDingTalk, notify.ChannelWeCom, notify.ChannelShoutrrr,
		} {
			if identifies(driver, channel) {
				registry[channel] = driver
			}
		}
	}
	return &Worker{store: repository, drivers: registry, decoder: decoder, observer: observer,
		config: config, now: func() time.Time { return time.Now().UTC() },
		random: rand.New(rand.NewSource(time.Now().UnixNano())), limit: config.InitialConcurrency}, nil // #nosec G404 -- retry jitter is not cryptographic.
}

// DriverBinding avoids probing Validate with fake endpoint secrets. Callers
// should wrap every driver with Bind.
type channelDriver struct {
	notify.ChannelDriver
	channel notify.Channel
}

func Bind(channel notify.Channel, driver notify.ChannelDriver) notify.ChannelDriver {
	return channelDriver{ChannelDriver: driver, channel: channel}
}

func identifies(driver notify.ChannelDriver, channel notify.Channel) bool {
	bound, ok := driver.(channelDriver)
	return ok && bound.channel == channel
}

type Stats struct {
	Lane            store.DeliveryLane `json:"lane"`
	Concurrency     int                `json:"concurrency"`
	NextConcurrency int                `json:"next_concurrency"`
	Claimed         int                `json:"claimed"`
	Accepted        int                `json:"accepted"`
	Retried         int                `json:"retried"`
	Failed          int                `json:"failed"`
	DeadLettered    int                `json:"dead_lettered"`
}

type result struct {
	status string
	err    error
}

func (w *Worker) RunOnce(ctx context.Context) (Stats, error) {
	if w == nil || w.store == nil {
		return Stats{}, errors.New("notifier: worker is not initialized")
	}
	concurrency := w.currentLimit()
	claimLimit := min(w.config.BatchSize, concurrency*w.config.ClaimMultiplier)
	stats := Stats{Lane: w.config.Lane, Concurrency: concurrency, NextConcurrency: concurrency}
	leases, err := w.store.ClaimDeliveryLane(ctx, w.config.Owner, w.config.Lane, claimLimit, w.config.PerTenant, w.config.LeaseDuration)
	if err != nil {
		return stats, err
	}
	stats.Claimed = len(leases)
	results := make(chan result, len(leases))
	semaphore := make(chan struct{}, concurrency)
	var group sync.WaitGroup
	for _, lease := range leases {
		lease := lease
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			status, processErr := w.process(ctx, lease)
			results <- result{status: status, err: processErr}
		}()
	}
	group.Wait()
	close(results)
	var failures []error
	for result := range results {
		switch result.status {
		case "accepted":
			stats.Accepted++
		case "retry_wait":
			stats.Retried++
		case "failed":
			stats.Failed++
		case "dead_letter":
			stats.DeadLettered++
		}
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	stats.NextConcurrency = w.adjustLimit(stats, claimLimit)
	if observer, ok := w.observer.(laneObserver); ok {
		observer.RecordDeliveryLane(ctx, string(w.config.Lane), stats.NextConcurrency, stats.Claimed)
	}
	return stats, errors.Join(failures...)
}

func (w *Worker) currentLimit() int {
	w.limitMu.Lock()
	defer w.limitMu.Unlock()
	return w.limit
}

// adjustLimit is additive-increase/multiplicative-decrease. Provider pressure
// in one lane immediately halves only that lane; a fully consumed clean batch
// grows capacity one slot at a time.
func (w *Worker) adjustLimit(stats Stats, claimLimit int) int {
	w.limitMu.Lock()
	defer w.limitMu.Unlock()
	pressure := stats.Retried + stats.Failed + stats.DeadLettered
	if stats.Claimed > 0 && pressure*4 >= stats.Claimed {
		w.limit = max(w.config.MinConcurrency, w.limit/2)
	} else if stats.Claimed == claimLimit && pressure == 0 && w.limit < w.config.MaxConcurrency {
		w.limit++
	}
	return w.limit
}

func (w *Worker) process(parent context.Context, lease store.DeliveryLease) (string, error) {
	started := w.now()
	if lease.AttemptNumber == 1 && w.observer != nil {
		delay := started.Sub(lease.EligibleAt)
		if delay >= 0 {
			w.observer.RecordEligibleFirstAttempt(parent, lease.Channel, delay)
		}
	}
	endpoint, err := w.decoder.Decode(parent, lease)
	if err != nil {
		return w.finishFailure(parent, lease, nil, err, started)
	}
	driver := w.drivers[endpoint.Channel]
	if driver == nil {
		return w.finishFailure(parent, lease, nil, fmt.Errorf("notifier: no driver for channel %q", endpoint.Channel), started)
	}
	event := notify.CanonicalEvent{ID: domain.CanonicalEventID(lease.EventID), Source: lease.EventSource,
		Kind: lease.EventKind, Subject: eventSubject(lease.EventPayload, lease.EventKind), EntityID: lease.EventEntityID,
		Time: lease.EventObservedAt, Revision: lease.EventRevision,
		SchemaVersion: lease.EventSchemaVersion, Summary: eventSummary(lease.EventPayload), Data: lease.EventPayload}
	ctx, cancel := context.WithTimeout(parent, w.config.AttemptTimeout)
	defer cancel()
	payload, err := driver.Render(ctx, event, endpoint)
	if err != nil {
		return w.finishFailure(parent, lease, driver, err, started)
	}
	receipt, err := driver.Send(ctx, notify.Delivery{ID: lease.ID, EventID: domain.CanonicalEventID(lease.EventID), Endpoint: endpoint}, payload)
	if err != nil {
		return w.finishFailure(parent, lease, driver, err, started)
	}
	finished := w.now()
	err = w.store.CompleteDeliveryAttempt(parent, store.CompleteDeliveryParams{
		DeliveryID: lease.ID, LeaseToken: lease.LeaseToken, AttemptNumber: lease.AttemptNumber,
		Status: "accepted", ProviderMessageID: receipt.ProviderMessageID,
		HTTPStatus: receipt.HTTPStatus, FinishedAt: finished,
	})
	w.observe(parent, string(endpoint.Channel), "accepted", finished.Sub(started), 0)
	return "accepted", err
}

func (w *Worker) finishFailure(ctx context.Context, lease store.DeliveryLease, driver notify.ChannelDriver, cause error, started time.Time) (string, error) {
	decision := notify.ClassifyError(cause)
	if driver != nil {
		decision = driver.Classify(cause)
	}
	status := "failed"
	var retryAt *time.Time
	if decision.Class == notify.ErrorClassRetryable && lease.AttemptNumber < w.config.MaxAttempts {
		status = "retry_wait"
		delay := decision.RetryAfter
		if delay <= 0 {
			delay = w.retryDelay(lease.AttemptNumber)
		}
		value := w.now().Add(delay)
		retryAt = &value
	} else if decision.Class == notify.ErrorClassRetryable {
		status = "dead_letter"
	}
	finished := w.now()
	errorSummary := cause.Error()
	if len(errorSummary) > 1024 {
		errorSummary = errorSummary[:1024]
	}
	providerCode := ""
	httpStatus := 0
	var deliveryError *notify.Error
	if errors.As(cause, &deliveryError) {
		providerCode = deliveryError.ProviderCode
		httpStatus = deliveryError.StatusCode
	}
	completeErr := w.store.CompleteDeliveryAttempt(ctx, store.CompleteDeliveryParams{
		DeliveryID: lease.ID, LeaseToken: lease.LeaseToken, AttemptNumber: lease.AttemptNumber,
		Status: status, HTTPStatus: httpStatus, ProviderCode: providerCode,
		ErrorClass: string(decision.Class), ErrorSummary: errorSummary,
		RetryAt: retryAt, DisableEndpoint: decision.DisableEndpoint, FinishedAt: finished,
		DeadLetterReason: errorSummary,
	})
	channel := lease.Channel
	w.observe(ctx, channel, status, finished.Sub(started), decision.RetryAfter)
	return status, errors.Join(cause, completeErr)
}

func (w *Worker) retryDelay(attempt int) time.Duration {
	maximum := w.config.RetryBase
	for current := 1; current < attempt && maximum < w.config.RetryMaximum; current++ {
		if maximum >= w.config.RetryMaximum/2 {
			maximum = w.config.RetryMaximum
			break
		}
		maximum *= 2
	}
	w.randomMu.Lock()
	defer w.randomMu.Unlock()
	return time.Duration(w.random.Int63n(int64(maximum) + 1))
}

func (w *Worker) observe(ctx context.Context, channel, result string, duration, retryAfter time.Duration) {
	if w.observer != nil {
		w.observer.RecordNotification(ctx, channel, result, duration, retryAfter)
	}
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
	return notify.EventSummary(payload)
}
