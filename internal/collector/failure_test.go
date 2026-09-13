package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
)

func TestFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"limit", &fetchFailure{errors.New("private body"), 429}, "rate_limited"},
		{"probe", fmt.Errorf("probe: %w", &statuspage.HTTPStatusError{StatusCode: 403}), "access_denied"},
		{"server", &fetchFailure{errors.New("server"), 503}, "upstream_server"},
		{"http", &fetchFailure{errors.New("missing"), 404}, "upstream_http"},
		{"timeout", fmt.Errorf("request: %w", context.DeadlineExceeded), "timeout"},
		{"dns", &net.DNSError{Err: "no such host", Name: "private.example"}, "network"},
		{"json", &json.SyntaxError{}, "invalid_payload"},
		{"cancel", context.Canceled, "cancelled"},
		{"unknown", errors.New("HTTP 429 secret in arbitrary text"), "unclassified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := failureCode(tc.err); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
	a, b := &fetchFailure{errors.New("limit"), 429}, context.DeadlineExceeded
	if failureCode(errors.Join(a, b)) != failureCode(errors.Join(b, a)) {
		t.Fatal("classification depends on completion order")
	}
}
