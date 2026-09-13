package collector

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net"

	"github.com/Seeridia/StatusHub/internal/adapter/ecosystem"
	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
)

// Only bounded classification codes cross the persistence/API boundary.
// Raw errors may contain credential-bearing URLs or upstream response content.
type fetchFailure struct {
	err    error
	status int
}

func (e *fetchFailure) Error() string { return e.err.Error() }
func (e *fetchFailure) Unwrap() error { return e.err }

func failureCode(err error) string {
	// Join order depends on concurrent fetch completion. Select deterministically.
	codes := map[string]bool{}
	var visit func(error)
	visit = func(e error) {
		if e == nil {
			return
		}
		status := 0
		switch v := e.(type) {
		case *fetchFailure:
			status = v.status
		case *statuspage.HTTPStatusError:
			status = v.StatusCode
		case *ecosystem.HTTPStatusError:
			status = v.StatusCode
		}
		switch {
		case status == 429:
			codes["rate_limited"] = true
		case status == 401 || status == 403:
			codes["access_denied"] = true
		case status >= 500:
			codes["upstream_server"] = true
		case status >= 400:
			codes["upstream_http"] = true
		}
		if e == context.DeadlineExceeded {
			codes["timeout"] = true
		}
		if e == context.Canceled {
			codes["cancelled"] = true
		}
		if n, ok := e.(net.Error); ok {
			if n.Timeout() {
				codes["timeout"] = true
			} else {
				codes["network"] = true
			}
		}
		switch e.(type) {
		case *json.SyntaxError, *json.UnmarshalTypeError, *xml.SyntaxError:
			codes["invalid_payload"] = true
		}
		if e == statuspage.ErrInvalidPayload || e == ecosystem.ErrInvalid {
			codes["invalid_payload"] = true
		}
		if e == statuspage.ErrNotStatuspage || e == ecosystem.ErrNotRecognized || e == statuspage.ErrUnsupported || e == ecosystem.ErrUnsupported {
			codes["unsupported_source"] = true
		}
		if many, ok := e.(interface{ Unwrap() []error }); ok {
			for _, child := range many.Unwrap() {
				visit(child)
			}
		} else {
			visit(errors.Unwrap(e))
		}
	}
	visit(err)
	for _, code := range []string{"rate_limited", "access_denied", "upstream_server", "upstream_http", "timeout", "network", "invalid_payload", "unsupported_source", "cancelled"} {
		if codes[code] {
			return code
		}
	}
	return "unclassified"
}
