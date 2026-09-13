// Package audit implements the portable, tamper-evident audit event format.
// The hash algorithm is deliberately independent from PostgreSQL so exports
// can be verified without trusting the running service.
package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const hashDomain = "statushub.audit.v1"

var ZeroHash = make([]byte, sha256.Size)

type Event struct {
	TenantID     string          `json:"tenant_id"`
	Sequence     int64           `json:"sequence"`
	OccurredAt   time.Time       `json:"occurred_at"`
	ActorType    string          `json:"actor_type"`
	ActorID      string          `json:"actor_id"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id"`
	Outcome      string          `json:"outcome"`
	RequestID    string          `json:"request_id,omitempty"`
	Metadata     json.RawMessage `json:"metadata"`
	PreviousHash string          `json:"previous_hash"`
	EventHash    string          `json:"event_hash"`
}

type AppendInput struct {
	OccurredAt   time.Time
	ActorType    string
	ActorID      string
	Action       string
	ResourceType string
	ResourceID   string
	Outcome      string
	RequestID    string
	Metadata     json.RawMessage
}

func NewEvent(tenantID string, sequence int64, previous []byte, input AppendInput) (Event, []byte, error) {
	metadata, err := CanonicalJSON(input.Metadata)
	if err != nil {
		return Event{}, nil, err
	}
	if len(previous) != sha256.Size {
		return Event{}, nil, errors.New("audit: previous hash must be 32 bytes")
	}
	event := Event{
		TenantID: tenantID, Sequence: sequence, OccurredAt: input.OccurredAt.UTC().Truncate(time.Microsecond),
		ActorType: input.ActorType, ActorID: input.ActorID, Action: input.Action,
		ResourceType: input.ResourceType, ResourceID: input.ResourceID,
		Outcome: input.Outcome, RequestID: input.RequestID, Metadata: metadata,
		PreviousHash: hex.EncodeToString(previous),
	}
	hash, err := Hash(event, previous)
	if err != nil {
		return Event{}, nil, err
	}
	event.EventHash = hex.EncodeToString(hash)
	return event, hash, nil
}

func Hash(event Event, previous []byte) ([]byte, error) {
	if event.Sequence <= 0 || event.OccurredAt.IsZero() || len(previous) != sha256.Size {
		return nil, errors.New("audit: invalid sequence, timestamp, or previous hash")
	}
	metadata, err := CanonicalJSON(event.Metadata)
	if err != nil {
		return nil, err
	}
	hasher := sha256.New()
	writeField(hasher, []byte(hashDomain))
	writeField(hasher, previous)
	var sequence [8]byte
	binary.BigEndian.PutUint64(sequence[:], uint64(event.Sequence))
	writeField(hasher, sequence[:])
	for _, field := range []string{
		event.TenantID,
		event.OccurredAt.UTC().Format(time.RFC3339Nano),
		event.ActorType,
		event.ActorID,
		event.Action,
		event.ResourceType,
		event.ResourceID,
		event.Outcome,
		event.RequestID,
	} {
		writeField(hasher, []byte(field))
	}
	writeField(hasher, metadata)
	return hasher.Sum(nil), nil
}

func Verify(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	previous := append([]byte(nil), ZeroHash...)
	var tenantID string
	for index, event := range events {
		if index == 0 {
			tenantID = event.TenantID
		}
		if event.TenantID != tenantID {
			return fmt.Errorf("audit: sequence %d belongs to a different tenant", event.Sequence)
		}
		wantSequence := int64(index + 1)
		if event.Sequence != wantSequence {
			return fmt.Errorf("audit: sequence gap: got %d, want %d", event.Sequence, wantSequence)
		}
		encodedPrevious, err := hex.DecodeString(event.PreviousHash)
		if err != nil || !bytes.Equal(encodedPrevious, previous) {
			return fmt.Errorf("audit: invalid previous hash at sequence %d", event.Sequence)
		}
		actual, err := Hash(event, previous)
		if err != nil {
			return fmt.Errorf("audit: hash sequence %d: %w", event.Sequence, err)
		}
		encodedEvent, err := hex.DecodeString(event.EventHash)
		if err != nil || !bytes.Equal(encodedEvent, actual) {
			return fmt.Errorf("audit: invalid event hash at sequence %d", event.Sequence)
		}
		previous = actual
	}
	return nil
}

func CanonicalJSON(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage(`{}`), nil
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("audit: decode metadata: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("audit: metadata contains multiple JSON values")
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("audit: encode metadata: %w", err)
	}
	return canonical, nil
}

type fieldWriter interface {
	Write([]byte) (int, error)
}

func writeField(writer fieldWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}
