package jetstream

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()
	config := DefaultConfig()
	if err := config.validate(); err != nil {
		t.Fatalf("DefaultConfig().validate() error = %v", err)
	}
	config.MaxMessageSize = 0
	if err := config.validate(); err == nil {
		t.Fatal("validate() error = nil, want invalid limit")
	}
}

func TestJetStreamRedeliveryAndPublishDeduplication(t *testing.T) {
	natsURL := os.Getenv("STATUSMON_TEST_NATS_URL")
	if natsURL == "" {
		t.Skip("set STATUSMON_TEST_NATS_URL to run JetStream integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	subject := "statusmon.test." + suffix + ".normal"
	config := DefaultConfig()
	config.StreamName = "STATUSMON_TEST_" + suffix
	config.Subjects = []string{"statusmon.test." + suffix + ".>"}
	config.MaxAge = time.Minute
	config.MaxBytes = 1 << 20
	config.DuplicateWindow = time.Minute

	client, err := Connect(ctx, natsURL, config)
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer client.Close()
	defer func() { _ = client.js.DeleteStream(context.Background(), config.StreamName) }()

	message := bus.Message{ID: "outbox-1", Subject: subject, Data: []byte(`{"event_id":"event-1"}`)}
	firstAck, err := client.Publish(ctx, message)
	if err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if firstAck.Duplicate {
		t.Fatal("first publish unexpectedly marked duplicate")
	}
	secondAck, err := client.Publish(ctx, message)
	if err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if !secondAck.Duplicate || secondAck.Sequence != firstAck.Sequence {
		t.Fatalf("duplicate ack = %+v, first = %+v", secondAck, firstAck)
	}

	consumerConfig := DefaultConsumerConfig("consumer_"+suffix, subject)
	consumerConfig.AckWait = 250 * time.Millisecond
	consumerConfig.NakDelay = 25 * time.Millisecond
	consumer, err := client.Consumer(ctx, consumerConfig)
	if err != nil {
		t.Fatalf("Consumer() error = %v", err)
	}

	var mutex sync.Mutex
	businessWrites := map[string]struct{}{}
	attempts := 0
	handler := func(_ context.Context, delivery Delivery) error {
		mutex.Lock()
		defer mutex.Unlock()
		attempts++
		businessWrites[delivery.MessageID] = struct{}{}
		if attempts == 1 {
			return errors.New("simulated crash after idempotent write and before ack")
		}
		if delivery.NumDelivered < 2 {
			return fmt.Errorf("redelivery count = %d, want at least 2", delivery.NumDelivered)
		}
		return nil
	}

	processed, processErr := consumer.ProcessBatch(ctx, 1, time.Second, handler)
	if processErr == nil || processed != 0 {
		t.Fatalf("first ProcessBatch() = (%d, %v), want simulated failure", processed, processErr)
	}
	time.Sleep(50 * time.Millisecond)
	processed, err = consumer.ProcessBatch(ctx, 1, 2*time.Second, handler)
	if err != nil || processed != 1 {
		t.Fatalf("second ProcessBatch() = (%d, %v), want (1, nil)", processed, err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if attempts != 2 || len(businessWrites) != 1 {
		t.Fatalf("attempts = %d, logical writes = %d; want 2 and 1", attempts, len(businessWrites))
	}
}
