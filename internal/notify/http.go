package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	securetransport "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/transport"
)

const (
	defaultHTTPTimeout     = 10 * time.Second
	maximumResponsePreview = 4 << 10
)

type clock func() time.Time

type driverOptions struct {
	now clock
}

type Option func(*driverOptions)

// WithClock injects the attempt clock used for signatures, accepted timestamps,
// and HTTP-date Retry-After calculations.
func WithClock(now func() time.Time) Option {
	return func(options *driverOptions) {
		options.now = normalizeClock(now)
	}
}

type requestDecorator func(request *http.Request, body []byte, attemptTime time.Time) error

func defaultHTTPDoer() HTTPDoer {
	config := securetransport.DefaultConfig()
	config.RequestTimeout = defaultHTTPTimeout
	config.DisableRedirects = true
	client, err := securetransport.New(config)
	if err != nil {
		return HTTPDoerFunc(func(*http.Request) (*http.Response, error) { return nil, err })
	}
	return client
}

func normalizeClock(now func() time.Time) clock {
	if now == nil {
		return func() time.Time { return time.Now().UTC() }
	}
	return func() time.Time { return now().UTC() }
}

func optionsClock(options []Option) clock {
	configured := driverOptions{now: normalizeClock(nil)}
	for _, option := range options {
		if option != nil {
			option(&configured)
		}
	}
	return configured.now
}

func validateEndpoint(endpoint Endpoint, channel Channel, requireSigningKey bool) error {
	if endpoint.Channel != channel {
		return permanent(channel, "validate endpoint", fmt.Errorf("channel must be %q", channel))
	}
	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return permanent(channel, "validate endpoint", errors.New("endpoint URL is invalid"))
	}
	if parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Opaque != "" {
		return permanent(channel, "validate endpoint", errors.New("endpoint must be an absolute HTTPS URL"))
	}
	if parsed.User != nil {
		return permanent(channel, "validate endpoint", errors.New("endpoint URL userinfo is forbidden"))
	}
	if parsed.Fragment != "" {
		return permanent(channel, "validate endpoint", errors.New("endpoint URL fragment is forbidden"))
	}
	if endpoint.MaxPayloadBytes < 0 {
		return permanent(channel, "validate endpoint", errors.New("maximum payload bytes cannot be negative"))
	}
	if requireSigningKey {
		if !validToken(endpoint.KeyID) {
			return permanent(channel, "validate endpoint", errors.New("key ID is required and may contain only letters, digits, '.', '_' or '-'"))
		}
		if len(endpoint.Secret) == 0 {
			return permanent(channel, "validate endpoint", errors.New("signing secret is required"))
		}
	}
	return nil
}

func validToken(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	return !strings.ContainsAny(value, "\r\n")
}

func sendHTTP(
	ctx context.Context,
	doer HTTPDoer,
	now clock,
	delivery Delivery,
	payload Payload,
	decorate requestDecorator,
) (Receipt, error) {
	if ctx == nil {
		return Receipt{}, permanent(payload.Channel, "send", errors.New("context is nil"))
	}
	if doer == nil {
		return Receipt{}, permanent(payload.Channel, "send", errors.New("HTTP sender is nil"))
	}
	if len(payload.Body) == 0 {
		return Receipt{}, permanent(payload.Channel, "send", errors.New("payload body is empty"))
	}
	if !validHeaderValue(delivery.ID) || !validHeaderValue(string(delivery.EventID)) {
		return Receipt{}, permanent(payload.Channel, "send", errors.New("delivery or event ID contains an invalid header byte"))
	}

	receipt, statusCode, err := sendHTTPBody(ctx, doer, now, delivery, payload.ContentType, payload.Body, decorate)
	if err == nil {
		receipt.Degraded = payload.Degraded
		return receipt, nil
	}

	// A 413 is the only response that triggers an in-driver second request.
	// It is a bounded representation change, not a general retry policy.
	if statusCode == http.StatusRequestEntityTooLarge && len(payload.FallbackBody) > 0 &&
		!bytes.Equal(payload.Body, payload.FallbackBody) {
		fallbackReceipt, _, fallbackErr := sendHTTPBody(
			ctx, doer, now, delivery, payload.ContentType, payload.FallbackBody, decorate,
		)
		if fallbackErr == nil {
			fallbackReceipt.Degraded = true
			return fallbackReceipt, nil
		}
		return Receipt{}, fallbackErr
	}
	return Receipt{}, err
}

func sendHTTPBody(
	ctx context.Context,
	doer HTTPDoer,
	now clock,
	delivery Delivery,
	contentType string,
	body []byte,
	decorate requestDecorator,
) (Receipt, int, error) {
	attemptTime := now()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.Endpoint.URL, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, 0, permanent(delivery.Endpoint.Channel, "create request", errors.New("endpoint URL is invalid"))
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", "vendor-status-monitoring/1")
	if decorate != nil {
		if err := decorate(request, body, attemptTime); err != nil {
			return Receipt{}, 0, permanent(delivery.Endpoint.Channel, "decorate request", err)
		}
	}

	response, err := doer.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return Receipt{}, 0, &Error{
			Channel:   delivery.Endpoint.Channel,
			Operation: "send",
			Class:     classifyTransportError(err),
			Err:       redactedHTTPError{cause: err},
		}
	}
	if response == nil {
		return Receipt{}, 0, &Error{
			Channel:   delivery.Endpoint.Channel,
			Operation: "send",
			Class:     ErrorClassRetryable,
			Err:       errors.New("HTTP sender returned a nil response"),
		}
	}

	preview, readErr := readResponsePreview(response.Body)
	statusCode := response.StatusCode
	if statusCode >= 200 && statusCode < 300 {
		// Once a 2xx response has been received, a response-body read failure does
		// not downgrade provider acceptance; neither supported protocol requires a
		// success body to establish acceptance.
		return Receipt{
			Status:       StatusProviderAccepted,
			HTTPStatus:   statusCode,
			AcceptedAt:   attemptTime,
			ResponseBody: preview,
		}, statusCode, nil
	}

	class, disable := classifyHTTPStatus(statusCode)
	retryAfter := time.Duration(0)
	if class == ErrorClassRetryable {
		retryAfter = parseRetryAfter(response.Header.Get("Retry-After"), attemptTime)
	}
	requestError := &Error{
		Channel:         delivery.Endpoint.Channel,
		Operation:       "send",
		Class:           class,
		StatusCode:      statusCode,
		ResponseBody:    preview,
		RetryAfter:      retryAfter,
		DisableEndpoint: disable,
	}
	if readErr != nil {
		requestError.Err = fmt.Errorf("read error response: %w", readErr)
	}
	return Receipt{}, statusCode, requestError
}

func classifyTransportError(err error) ErrorClass {
	switch {
	case errors.Is(err, securetransport.ErrInvalidURL),
		errors.Is(err, securetransport.ErrUnsupportedScheme),
		errors.Is(err, securetransport.ErrUnsafeTarget),
		errors.Is(err, securetransport.ErrTooManyRedirects):
		return ErrorClassPermanent
	default:
		return ErrorClassRetryable
	}
}

func readResponsePreview(body io.ReadCloser) (string, error) {
	if body == nil {
		return "", nil
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, maximumResponsePreview+1))
	if len(data) > maximumResponsePreview {
		data = data[:maximumResponsePreview]
	}
	return strings.TrimSpace(string(data)), err
}

func classifyHTTPStatus(statusCode int) (ErrorClass, bool) {
	switch {
	case statusCode == http.StatusRequestTimeout,
		statusCode == http.StatusTooEarly,
		statusCode == http.StatusTooManyRequests,
		statusCode >= 500:
		return ErrorClassRetryable, false
	case statusCode == http.StatusGone:
		return ErrorClassPermanent, true
	default:
		return ErrorClassPermanent, false
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > math.MaxInt64/int64(time.Second) {
			return time.Duration(math.MaxInt64)
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func permanent(channel Channel, operation string, err error) error {
	return &Error{Channel: channel, Operation: operation, Class: ErrorClassPermanent, Err: err}
}

type redactedHTTPError struct {
	cause error
}

func (e redactedHTTPError) Error() string {
	return "HTTP request failed"
}

func (e redactedHTTPError) Unwrap() error {
	return e.cause
}
