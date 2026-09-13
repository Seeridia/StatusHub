package notify

import (
	"errors"
	"fmt"
	"net"
	"time"
)

var (
	ErrRetryable = errors.New("notify: retryable failure")
	ErrPermanent = errors.New("notify: permanent failure")
)

// Error is safe to persist in a delivery attempt. ResponseBody is bounded by
// the HTTP helper and endpoint URLs/secrets are never included.
type Error struct {
	Channel         Channel
	Operation       string
	Class           ErrorClass
	StatusCode      int
	ProviderCode    string
	ResponseBody    string
	RetryAfter      time.Duration
	DisableEndpoint bool
	Err             error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := "notify"
	if e.Channel != "" {
		message += " " + string(e.Channel)
	}
	if e.Operation != "" {
		message += " " + e.Operation
	}
	if e.StatusCode != 0 {
		message += fmt.Sprintf(": HTTP %d", e.StatusCode)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrRetryable:
		return e.Class == ErrorClassRetryable
	case ErrPermanent:
		return e.Class == ErrorClassPermanent
	default:
		return false
	}
}

func ClassifyError(err error) RetryDecision {
	if err == nil {
		return RetryDecision{}
	}
	var deliveryError *Error
	if errors.As(err, &deliveryError) {
		return RetryDecision{
			Class:           deliveryError.Class,
			RetryAfter:      deliveryError.RetryAfter,
			DisableEndpoint: deliveryError.DisableEndpoint,
		}
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return RetryDecision{Class: ErrorClassRetryable}
	}
	// Unknown errors returned by an injected HTTP sender are conservatively
	// retryable; local validation/render errors are always wrapped as permanent.
	return RetryDecision{Class: ErrorClassRetryable}
}
