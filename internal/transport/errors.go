package transport

import (
	"errors"
	"fmt"
)

// ErrorKind is a stable, machine-readable failure category. Callers should use
// KindOf or errors.Is instead of matching error strings.
type ErrorKind string

const (
	KindInvalidURL          ErrorKind = "invalid_url"
	KindUnsupportedScheme   ErrorKind = "unsupported_scheme"
	KindUnsafeTarget        ErrorKind = "unsafe_target"
	KindDNS                 ErrorKind = "dns"
	KindConnect             ErrorKind = "connect"
	KindTLS                 ErrorKind = "tls"
	KindTimeout             ErrorKind = "timeout"
	KindCanceled            ErrorKind = "canceled"
	KindTooManyRedirects    ErrorKind = "too_many_redirects"
	KindBodyTooLarge        ErrorKind = "body_too_large"
	KindUnsupportedEncoding ErrorKind = "unsupported_encoding"
	KindReadBody            ErrorKind = "read_body"
	KindTransport           ErrorKind = "transport"
)

var (
	ErrInvalidURL          = errors.New("transport: invalid URL")
	ErrUnsupportedScheme   = errors.New("transport: unsupported URL scheme")
	ErrUnsafeTarget        = errors.New("transport: unsafe target")
	ErrDNS                 = errors.New("transport: DNS failure")
	ErrConnect             = errors.New("transport: connection failure")
	ErrTLS                 = errors.New("transport: TLS failure")
	ErrTimeout             = errors.New("transport: timeout")
	ErrCanceled            = errors.New("transport: canceled")
	ErrTooManyRedirects    = errors.New("transport: too many redirects")
	ErrBodyTooLarge        = errors.New("transport: response body too large")
	ErrUnsupportedEncoding = errors.New("transport: unsupported content encoding")
	ErrReadBody            = errors.New("transport: response body read failure")
	ErrTransport           = errors.New("transport: request failure")
)

// Error contains a stable category and a redacted request URL. Query strings
// and user information are deliberately omitted because they can contain
// credentials.
type Error struct {
	Kind ErrorKind
	Op   string
	URL  string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := "transport"
	if e.Op != "" {
		prefix += " " + e.Op
	}
	if e.URL != "" {
		prefix += " " + e.URL
	}
	if e.Err == nil {
		return fmt.Sprintf("%s: %s", prefix, e.Kind)
	}
	return fmt.Sprintf("%s: %s: %v", prefix, e.Kind, e.Err)
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
	return target == sentinelFor(e.Kind)
}

// KindOf returns the first transport error category in err's unwrap chain.
func KindOf(err error) (ErrorKind, bool) {
	var transportErr *Error
	if errors.As(err, &transportErr) {
		return transportErr.Kind, true
	}
	return "", false
}

func sentinelFor(kind ErrorKind) error {
	switch kind {
	case KindInvalidURL:
		return ErrInvalidURL
	case KindUnsupportedScheme:
		return ErrUnsupportedScheme
	case KindUnsafeTarget:
		return ErrUnsafeTarget
	case KindDNS:
		return ErrDNS
	case KindConnect:
		return ErrConnect
	case KindTLS:
		return ErrTLS
	case KindTimeout:
		return ErrTimeout
	case KindCanceled:
		return ErrCanceled
	case KindTooManyRedirects:
		return ErrTooManyRedirects
	case KindBodyTooLarge:
		return ErrBodyTooLarge
	case KindUnsupportedEncoding:
		return ErrUnsupportedEncoding
	case KindReadBody:
		return ErrReadBody
	case KindTransport:
		return ErrTransport
	default:
		return nil
	}
}
