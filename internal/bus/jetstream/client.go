// Package jetstream provides the durable internal event bus. It deliberately
// exposes explicit acknowledgement and redelivery metadata rather than hiding
// JetStream behind an exactly-once abstraction.
package jetstream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	natsjs "github.com/nats-io/nats.go/jetstream"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
)

const messageIDHeader = "Statusmon-Message-Id"

type Config struct {
	StreamName      string
	Subjects        []string
	MaxAge          time.Duration
	MaxBytes        int64
	MaxMessageSize  int32
	DuplicateWindow time.Duration
	Replicas        int
}

func DefaultConfig() Config {
	return Config{
		StreamName:      "STATUSMON_EVENTS",
		Subjects:        []string{"statusmon.events.>"},
		MaxAge:          7 * 24 * time.Hour,
		MaxBytes:        1 << 30,
		MaxMessageSize:  64 << 10,
		DuplicateWindow: 10 * time.Minute,
		Replicas:        1,
	}
}

func (c Config) validate() error {
	if strings.TrimSpace(c.StreamName) == "" || strings.ContainsAny(c.StreamName, " .*>") {
		return errors.New("JetStream stream name is invalid")
	}
	if len(c.Subjects) == 0 {
		return errors.New("JetStream subjects are required")
	}
	for _, subject := range c.Subjects {
		if strings.TrimSpace(subject) == "" || strings.ContainsAny(subject, " \t\r\n") {
			return fmt.Errorf("invalid JetStream stream subject %q", subject)
		}
	}
	if c.MaxAge <= 0 || c.MaxBytes <= 0 || c.MaxMessageSize <= 0 || c.DuplicateWindow <= 0 {
		return errors.New("JetStream limits and duplicate window must be positive")
	}
	if c.Replicas < 1 || c.Replicas > 5 {
		return errors.New("JetStream replicas must be between 1 and 5")
	}
	return nil
}

type Client struct {
	connection *nats.Conn
	stream     natsjs.Stream
	js         natsjs.JetStream
	config     Config
}

func Connect(ctx context.Context, url string, config Config, options ...nats.Option) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("JetStream context is nil")
	}
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("NATS URL is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	connection, err := nats.Connect(url, options...)
	if err != nil {
		return nil, fmt.Errorf("connect NATS: %w", err)
	}
	client, err := FromConnection(ctx, connection, config)
	if err != nil {
		connection.Close()
		return nil, err
	}
	return client, nil
}

func FromConnection(ctx context.Context, connection *nats.Conn, config Config) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("JetStream context is nil")
	}
	if connection == nil {
		return nil, errors.New("NATS connection is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	js, err := natsjs.New(connection, natsjs.WithDefaultTimeout(5*time.Second))
	if err != nil {
		return nil, fmt.Errorf("create JetStream client: %w", err)
	}
	stream, err := js.CreateOrUpdateStream(ctx, natsjs.StreamConfig{
		Name:        config.StreamName,
		Description: "Vendor status canonical event references",
		Subjects:    append([]string(nil), config.Subjects...),
		Retention:   natsjs.LimitsPolicy,
		MaxAge:      config.MaxAge,
		MaxBytes:    config.MaxBytes,
		MaxMsgSize:  config.MaxMessageSize,
		Storage:     natsjs.FileStorage,
		Replicas:    config.Replicas,
		Discard:     natsjs.DiscardOld,
		Duplicates:  config.DuplicateWindow,
		Compression: natsjs.S2Compression,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure JetStream stream %s: %w", config.StreamName, err)
	}
	return &Client{connection: connection, stream: stream, js: js, config: config}, nil
}

func (c *Client) Close() {
	if c != nil && c.connection != nil {
		c.connection.Close()
	}
}

// DeleteStream removes this client's configured stream. It is intended for
// isolated test environments and explicit administrative cleanup.
func (c *Client) DeleteStream(ctx context.Context) error {
	if c == nil || c.js == nil {
		return errors.New("JetStream client is not initialized")
	}
	return c.js.DeleteStream(ctx, c.config.StreamName)
}

func (c *Client) Publish(ctx context.Context, message bus.Message) (bus.PublishResult, error) {
	if c == nil || c.js == nil {
		return bus.PublishResult{}, errors.New("JetStream client is not initialized")
	}
	if ctx == nil {
		return bus.PublishResult{}, errors.New("JetStream context is nil")
	}
	if err := message.Validate(int(c.config.MaxMessageSize)); err != nil {
		return bus.PublishResult{}, err
	}
	contentType := message.ContentType
	if contentType == "" {
		contentType = bus.ContentTypeJSON
	}
	natsMessage := nats.NewMsg(message.Subject)
	natsMessage.Data = append([]byte(nil), message.Data...)
	natsMessage.Header.Set("Content-Type", contentType)
	natsMessage.Header.Set(messageIDHeader, message.ID)
	ack, err := c.js.PublishMsg(ctx, natsMessage, natsjs.WithMsgID(message.ID))
	if err != nil {
		return bus.PublishResult{}, fmt.Errorf("publish outbox message %s: %w", message.ID, err)
	}
	return bus.PublishResult{Stream: ack.Stream, Sequence: ack.Sequence, Duplicate: ack.Duplicate}, nil
}

type ConsumerConfig struct {
	Durable       string
	FilterSubject string
	AckWait       time.Duration
	MaxDeliver    int
	MaxAckPending int
	NakDelay      time.Duration
}

func DefaultConsumerConfig(durable, filterSubject string) ConsumerConfig {
	return ConsumerConfig{
		Durable: durable, FilterSubject: filterSubject,
		AckWait: 30 * time.Second, MaxDeliver: 10,
		MaxAckPending: 256, NakDelay: time.Second,
	}
}

func (c ConsumerConfig) validate() error {
	if strings.TrimSpace(c.Durable) == "" || strings.ContainsAny(c.Durable, " .*>") {
		return errors.New("JetStream durable consumer name is invalid")
	}
	if strings.TrimSpace(c.FilterSubject) == "" || strings.ContainsAny(c.FilterSubject, " \t\r\n") {
		return errors.New("JetStream consumer filter subject is invalid")
	}
	if c.AckWait <= 0 || c.MaxDeliver <= 0 || c.MaxAckPending <= 0 || c.NakDelay < 0 {
		return errors.New("JetStream consumer limits are invalid")
	}
	return nil
}

type Consumer struct {
	consumer natsjs.Consumer
	config   ConsumerConfig
}

func (c *Client) Consumer(ctx context.Context, config ConsumerConfig) (*Consumer, error) {
	if c == nil || c.stream == nil {
		return nil, errors.New("JetStream client is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("JetStream context is nil")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	consumer, err := c.stream.CreateOrUpdateConsumer(ctx, natsjs.ConsumerConfig{
		Name:          config.Durable,
		Durable:       config.Durable,
		Description:   "Vendor status event processor",
		DeliverPolicy: natsjs.DeliverAllPolicy,
		AckPolicy:     natsjs.AckExplicitPolicy,
		AckWait:       config.AckWait,
		MaxDeliver:    config.MaxDeliver,
		FilterSubject: config.FilterSubject,
		MaxAckPending: config.MaxAckPending,
		ReplayPolicy:  natsjs.ReplayInstantPolicy,
	})
	if err != nil {
		return nil, fmt.Errorf("ensure JetStream consumer %s: %w", config.Durable, err)
	}
	return &Consumer{consumer: consumer, config: config}, nil
}

type Delivery struct {
	MessageID    string
	Subject      string
	ContentType  string
	Data         []byte
	NumDelivered uint64
}

type Handler func(context.Context, Delivery) error

// ProcessBatch explicitly acks only after the handler succeeds. Handler
// failures are negatively acknowledged and therefore redelivered. A handler
// must still enforce business idempotency in PostgreSQL.
func (c *Consumer) ProcessBatch(ctx context.Context, batchSize int, maxWait time.Duration, handler Handler) (int, error) {
	if c == nil || c.consumer == nil {
		return 0, errors.New("JetStream consumer is not initialized")
	}
	if ctx == nil {
		return 0, errors.New("JetStream context is nil")
	}
	if batchSize <= 0 || maxWait <= 0 {
		return 0, errors.New("batch size and max wait must be positive")
	}
	if handler == nil {
		return 0, errors.New("JetStream handler is required")
	}
	fetchContext, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	batch, err := c.consumer.Fetch(batchSize, natsjs.FetchContext(fetchContext))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, nats.ErrTimeout) {
			return 0, nil
		}
		return 0, fmt.Errorf("fetch JetStream messages: %w", err)
	}
	processed := 0
	var processErrors []error
	for message := range batch.Messages() {
		metadata, metadataErr := message.Metadata()
		if metadataErr != nil {
			_ = message.NakWithDelay(c.config.NakDelay)
			processErrors = append(processErrors, fmt.Errorf("read message metadata: %w", metadataErr))
			continue
		}
		delivery := Delivery{
			MessageID:    message.Headers().Get(messageIDHeader),
			Subject:      message.Subject(),
			ContentType:  message.Headers().Get("Content-Type"),
			Data:         append([]byte(nil), message.Data()...),
			NumDelivered: metadata.NumDelivered,
		}
		if err := handler(ctx, delivery); err != nil {
			_ = message.NakWithDelay(c.config.NakDelay)
			processErrors = append(processErrors, fmt.Errorf("process message %s: %w", delivery.MessageID, err))
			continue
		}
		if err := message.DoubleAck(ctx); err != nil {
			processErrors = append(processErrors, fmt.Errorf("ack message %s: %w", delivery.MessageID, err))
			continue
		}
		processed++
	}
	if err := batch.Error(); err != nil {
		processErrors = append(processErrors, fmt.Errorf("JetStream batch: %w", err))
	}
	return processed, errors.Join(processErrors...)
}
