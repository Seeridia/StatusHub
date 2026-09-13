package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

func TestRunValidatesArgumentsBeforeNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing URL", args: nil, want: "-url is required"},
		{name: "relative URL", args: []string{"-url", "/status"}, want: "absolute HTTP(S) URL"},
		{name: "unknown operation", args: []string{"-url", "https://example.com", "-operation", "remove"}, want: "unsupported -operation"},
		{name: "unknown resource", args: []string{"-url", "https://example.com", "-operation", "fetch", "-resource", "everything"}, want: "unsupported -resource"},
		{name: "invalid timeout", args: []string{"-url", "https://example.com", "-timeout", "0s"}, want: "-timeout must be positive"},
		{name: "invalid canary iterations", args: []string{"-url", "https://example.com", "-iterations", "-1"}, want: "-iterations cannot be negative"},
		{name: "invalid canary interval", args: []string{"-url", "https://example.com", "-interval", "0s"}, want: "-interval must be positive"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var output bytes.Buffer
			err := run(ctx, test.args, &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

type fakeCanaryAdapter struct {
	fetches []domain.ResourceKind
	failOn  domain.ResourceKind
}

func (adapter *fakeCanaryAdapter) Probe(context.Context, domain.Target) (domain.Capabilities, error) {
	return domain.Capabilities{
		Engine: "statuspage", Confidence: 1,
		Endpoints: map[domain.ResourceKind]domain.EndpointCapability{
			domain.ResourceSummary:             {Resource: domain.ResourceSummary, Path: "/summary"},
			domain.ResourceUnresolvedIncidents: {Resource: domain.ResourceUnresolvedIncidents, Path: "/unresolved"},
		},
	}, nil
}

func (adapter *fakeCanaryAdapter) Fetch(_ context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	adapter.fetches = append(adapter.fetches, request.ResourceKind)
	if request.ResourceKind == adapter.failOn {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.New("fixture failure")
	}
	return domain.Snapshot{
		ResourceKind: request.ResourceKind,
		Incidents:    []domain.Incident{{ID: "active"}},
	}, domain.FetchMeta{Endpoint: request.Endpoint.Path, StatusCode: 200, SchemaHash: "hash", ObservedAt: time.Now().UTC()}, nil
}

func TestCanaryChecksUnresolvedAndSummary(t *testing.T) {
	adapter := &fakeCanaryAdapter{}
	targetURL, _ := url.Parse("https://status.example.com")
	report, err := canaryOnce(context.Background(), adapter, domain.Target{URL: targetURL, SourceID: "source"})
	if err != nil {
		t.Fatalf("canaryOnce() error = %v", err)
	}
	if !report.Healthy || !report.ActiveIncident || len(report.Endpoints) != 2 {
		t.Fatalf("canary report = %+v", report)
	}
	want := []domain.ResourceKind{domain.ResourceUnresolvedIncidents, domain.ResourceSummary}
	for index := range want {
		if adapter.fetches[index] != want[index] {
			t.Fatalf("fetch order = %v, want %v", adapter.fetches, want)
		}
	}
}

func TestParseResource(t *testing.T) {
	t.Parallel()

	for _, resource := range []string{"summary", "status", "components", "incidents", "unresolved_incidents", "scheduled_maintenances"} {
		if _, err := parseResource(resource); err != nil {
			t.Fatalf("parseResource(%q) error = %v", resource, err)
		}
	}
	if _, err := parseResource("unknown"); err == nil {
		t.Fatal("parseResource(unknown) returned nil error")
	}
}
