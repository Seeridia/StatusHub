package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/adapter/awshealth"
	"github.com/Seeridia/StatusHub/internal/adapter/ecosystem"
	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
	"github.com/Seeridia/StatusHub/internal/adapter/vendorprofile"
	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/transport"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("statushub", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var (
		rawURL     string
		operation  string
		resource   string
		sourceID   string
		provider   string
		pageID     string
		recipeFile string
		timeout    time.Duration
		iterations int
		interval   time.Duration
	)
	flags.StringVar(&rawURL, "url", "", "base URL of a public status page")
	flags.StringVar(&operation, "operation", "probe", "probe or fetch")
	flags.StringVar(&resource, "resource", "summary", "summary, status, components, incidents, unresolved_incidents, or scheduled_maintenances")
	flags.StringVar(&sourceID, "source-id", "cli", "stable source identifier")
	flags.StringVar(&provider, "provider", "", "optional engine hint (for example cachet, gatus, status-io, incident-io)")
	flags.StringVar(&pageID, "page-id", "", "provider page identifier (required for non-API Status.io URLs)")
	flags.StringVar(&recipeFile, "html-recipes-file", "", "JSON file containing controlled HTML extraction recipes")
	flags.DurationVar(&timeout, "timeout", 15*time.Second, "overall command timeout")
	flags.IntVar(&iterations, "iterations", 1, "canary cycles; 0 runs until timeout")
	flags.DurationVar(&interval, "interval", 5*time.Minute, "delay between canary cycles")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(rawURL) == "" {
		return errors.New("-url is required")
	}
	if timeout <= 0 {
		return errors.New("-timeout must be positive")
	}
	if iterations < 0 {
		return errors.New("-iterations cannot be negative")
	}
	if interval <= 0 {
		return errors.New("-interval must be positive")
	}

	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parse -url: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return errors.New("-url must be an absolute HTTP(S) URL")
	}

	transportConfig := transport.DefaultConfig()
	transportConfig.MaxBodyBytes = 8 << 20
	httpClient, err := transport.New(transportConfig)
	if err != nil {
		return fmt.Errorf("create HTTP transport: %w", err)
	}
	defer httpClient.CloseIdleConnections()

	statusAdapter, err := statuspage.New(httpClient)
	if err != nil {
		return fmt.Errorf("create Statuspage adapter: %w", err)
	}
	awsAdapter, err := awshealth.New(httpClient)
	if err != nil {
		return fmt.Errorf("create AWS Health adapter: %w", err)
	}
	recipeOptions, err := loadHTMLRecipeOptions(recipeFile)
	if err != nil {
		return err
	}
	ecosystemAdapter, err := ecosystem.New(httpClient, recipeOptions...)
	if err != nil {
		return fmt.Errorf("create ecosystem adapter: %w", err)
	}
	profiledAdapter, err := vendorprofile.New(statusAdapter, awsAdapter, ecosystemAdapter)
	if err != nil {
		return fmt.Errorf("create vendor profile adapter: %w", err)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	target := domain.Target{URL: baseURL, SourceID: sourceID, Provider: provider, PageID: pageID}
	switch operation {
	case "probe":
		capabilities, probeErr := profiledAdapter.Probe(ctx, target)
		if probeErr != nil {
			return fmt.Errorf("probe %s: %w", baseURL.Redacted(), probeErr)
		}
		return writeJSON(output, capabilities)
	case "fetch":
		resourceKind, parseErr := parseResource(resource)
		if parseErr != nil {
			return parseErr
		}
		snapshot, meta, fetchErr := profiledAdapter.Fetch(ctx, domain.FetchRequest{
			Target:       target,
			Source:       domain.Source{ID: sourceID, Provider: provider, PageID: pageID, Kind: domain.SourceKindStatusPage, RequestedURL: baseURL},
			ResourceKind: resourceKind,
		})
		if fetchErr != nil {
			return fmt.Errorf("fetch %s: %w", baseURL.Redacted(), fetchErr)
		}
		return writeJSON(output, struct {
			Snapshot domain.Snapshot  `json:"snapshot"`
			Meta     domain.FetchMeta `json:"meta"`
		}{Snapshot: snapshot, Meta: meta})
	case "canary":
		return runCanary(ctx, profiledAdapter, target, iterations, interval, output)
	default:
		return fmt.Errorf("unsupported -operation %q; want probe, fetch, or canary", operation)
	}
}

func loadHTMLRecipeOptions(path string) ([]ecosystem.Option, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HTML recipes: %w", err)
	}
	var recipes []ecosystem.HTMLRecipe
	if err := json.Unmarshal(data, &recipes); err != nil {
		return nil, fmt.Errorf("decode HTML recipes: %w", err)
	}
	return []ecosystem.Option{ecosystem.WithHTMLRecipes(recipes...)}, nil
}

type canaryAdapter interface {
	Probe(context.Context, domain.Target) (domain.Capabilities, error)
	Fetch(context.Context, domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error)
}

type canaryEndpoint struct {
	Resource    domain.ResourceKind `json:"resource"`
	Endpoint    string              `json:"endpoint"`
	StatusCode  int                 `json:"status_code"`
	NotModified bool                `json:"not_modified"`
	Components  int                 `json:"components"`
	Incidents   int                 `json:"incidents"`
	SchemaHash  string              `json:"schema_hash"`
	ObservedAt  time.Time           `json:"observed_at"`
}

type canaryReport struct {
	Healthy        bool             `json:"healthy"`
	Engine         string           `json:"engine"`
	Confidence     float64          `json:"confidence"`
	CheckedAt      time.Time        `json:"checked_at"`
	ActiveIncident bool             `json:"active_incident"`
	Endpoints      []canaryEndpoint `json:"endpoints"`
}

func runCanary(ctx context.Context, statusAdapter canaryAdapter, target domain.Target, iterations int, interval time.Duration, output io.Writer) error {
	for cycle := 0; iterations == 0 || cycle < iterations; cycle++ {
		report, err := canaryOnce(ctx, statusAdapter, target)
		if err != nil {
			return err
		}
		if err := writeJSON(output, report); err != nil {
			return err
		}
		if iterations > 0 && cycle+1 >= iterations {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
	return nil
}

func canaryOnce(ctx context.Context, statusAdapter canaryAdapter, target domain.Target) (canaryReport, error) {
	capabilities, err := statusAdapter.Probe(ctx, target)
	if err != nil {
		return canaryReport{}, fmt.Errorf("canary probe: %w", err)
	}
	report := canaryReport{
		Healthy: true, Engine: capabilities.Engine,
		Confidence: capabilities.Confidence, CheckedAt: time.Now().UTC(),
		Endpoints: make([]canaryEndpoint, 0, 2),
	}
	for _, resource := range []domain.ResourceKind{
		domain.ResourceUnresolvedIncidents, domain.ResourceSummary, domain.ResourceStatus,
		domain.ResourceComponents, domain.ResourceIncidents, domain.ResourceScheduledMaintenances,
	} {
		endpoint, found := capabilities.Endpoints[resource]
		if !found {
			continue
		}
		snapshot, meta, err := statusAdapter.Fetch(ctx, domain.FetchRequest{
			Target:       target,
			Source:       domain.Source{ID: target.SourceID, Provider: capabilities.Engine, PageID: target.PageID, Kind: domain.SourceKindStatusPage, RequestedURL: target.URL},
			ResourceKind: resource,
			Endpoint:     endpoint,
		})
		if err != nil {
			return canaryReport{}, fmt.Errorf("canary fetch %s: %w", resource, err)
		}
		for _, incident := range snapshot.Incidents {
			if incident.Phase != domain.IncidentPhaseResolved {
				report.ActiveIncident = true
				break
			}
		}
		report.Endpoints = append(report.Endpoints, canaryEndpoint{
			Resource: resource, Endpoint: meta.Endpoint, StatusCode: meta.StatusCode,
			NotModified: meta.NotModified, Components: len(snapshot.Components),
			Incidents: len(snapshot.Incidents), SchemaHash: meta.SchemaHash,
			ObservedAt: meta.ObservedAt,
		})
	}
	if len(report.Endpoints) == 0 {
		return canaryReport{}, errors.New("canary: no usable endpoint was discovered")
	}
	return report, nil
}

func parseResource(value string) (domain.ResourceKind, error) {
	switch domain.ResourceKind(value) {
	case domain.ResourceSummary:
		return domain.ResourceSummary, nil
	case domain.ResourceIncidents:
		return domain.ResourceIncidents, nil
	case domain.ResourceUnresolvedIncidents:
		return domain.ResourceUnresolvedIncidents, nil
	case domain.ResourceStatus:
		return domain.ResourceStatus, nil
	case domain.ResourceComponents:
		return domain.ResourceComponents, nil
	case domain.ResourceScheduledMaintenances:
		return domain.ResourceScheduledMaintenances, nil
	default:
		return "", fmt.Errorf("unsupported -resource %q", value)
	}
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write JSON: %w", err)
	}
	return nil
}
