package bus

import (
	"errors"
	"fmt"
	"strings"
)

const ContentTypeJSON = "application/json"

// Message is the compact durable-bus representation of an outbox row. Data
// should contain identifiers and canonical metadata, never an upstream raw
// response body.
type Message struct {
	ID          string
	Subject     string
	ContentType string
	Data        []byte
}

type PublishResult struct {
	Stream    string
	Sequence  uint64
	Duplicate bool
}

func (m Message) Validate(maxBytes int) error {
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("bus message ID is required")
	}
	if err := ValidateSubject(m.Subject); err != nil {
		return err
	}
	if len(m.Data) == 0 {
		return errors.New("bus message data is required")
	}
	if maxBytes > 0 && len(m.Data) > maxBytes {
		return fmt.Errorf("bus message is %d bytes; maximum is %d", len(m.Data), maxBytes)
	}
	return nil
}

func ValidateSubject(subject string) error {
	if strings.TrimSpace(subject) == "" {
		return errors.New("bus subject is required")
	}
	if subject != strings.TrimSpace(subject) || strings.ContainsAny(subject, " \t\r\n*> ") {
		return fmt.Errorf("invalid publish subject %q", subject)
	}
	for _, token := range strings.Split(subject, ".") {
		if token == "" {
			return fmt.Errorf("invalid publish subject %q", subject)
		}
	}
	return nil
}
