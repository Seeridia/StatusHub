// Package vendorprofile applies vendor-specific endpoint and identity policy
// while delegating engine parsing to reusable adapters.
package vendorprofile

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	adaptercontract "github.com/Seeridia/StatusHub/internal/adapter"
	"github.com/Seeridia/StatusHub/internal/domain"
)

type Profile struct {
	Slug             string
	CanonicalHost    string
	Aliases          []string
	Engine           string
	MaximumBodyBytes int64
	CapabilityTTL    time.Duration
	Unsupported      map[domain.ResourceKind]bool
}

var builtins = []Profile{
	{Slug: "github", CanonicalHost: "www.githubstatus.com", Aliases: []string{"githubstatus.com"}, Engine: "atlassian-statuspage", MaximumBodyBytes: 2 << 20, CapabilityTTL: 24 * time.Hour},
	{Slug: "cloudflare", CanonicalHost: "www.cloudflarestatus.com", Aliases: []string{"cloudflarestatus.com"}, Engine: "atlassian-statuspage", MaximumBodyBytes: 8 << 20, CapabilityTTL: 6 * time.Hour},
	{Slug: "openai", CanonicalHost: "status.openai.com", Engine: "incident-io-statuspage-compat", MaximumBodyBytes: 2 << 20, CapabilityTTL: time.Hour,
		Unsupported: map[domain.ResourceKind]bool{domain.ResourceUnresolvedIncidents: true, domain.ResourceScheduledMaintenances: true}},
	{Slug: "anthropic", CanonicalHost: "status.claude.com", Aliases: []string{"status.anthropic.com"}, Engine: "atlassian-statuspage", MaximumBodyBytes: 2 << 20, CapabilityTTL: 24 * time.Hour},
	{Slug: "aws", CanonicalHost: "health.aws.amazon.com", Aliases: []string{"status.aws.amazon.com"}, Engine: "aws-public-health", MaximumBodyBytes: 8 << 20, CapabilityTTL: 6 * time.Hour},
}

func Builtins() []Profile { return append([]Profile(nil), builtins...) }

func Lookup(host string) (Profile, bool) {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	for _, profile := range builtins {
		if host == profile.CanonicalHost {
			return profile, true
		}
		for _, alias := range profile.Aliases {
			if host == alias {
				return profile, true
			}
		}
	}
	return Profile{}, false
}

func Canonicalize(input *url.URL) (*url.URL, Profile, bool, error) {
	if input == nil || input.Scheme == "" || input.Hostname() == "" {
		return nil, Profile{}, false, errors.New("vendor profile: absolute URL is required")
	}
	profile, found := Lookup(input.Hostname())
	if !found {
		clone := *input
		return &clone, Profile{}, false, nil
	}
	clone := *input
	clone.Scheme = "https"
	clone.Host = profile.CanonicalHost
	clone.Path, clone.RawPath, clone.RawQuery, clone.Fragment = "", "", "", ""
	return &clone, profile, true, nil
}

type Adapter struct {
	statuspage adaptercontract.Adapter
	aws        adaptercontract.Adapter
	ecosystem  adaptercontract.Adapter
}

var _ adaptercontract.Adapter = (*Adapter)(nil)

func New(statuspage, aws adaptercontract.Adapter, ecosystem ...adaptercontract.Adapter) (*Adapter, error) {
	if statuspage == nil || aws == nil {
		return nil, errors.New("vendor profile: statuspage and AWS adapters are required")
	}
	if len(ecosystem) > 1 {
		return nil, errors.New("vendor profile: at most one ecosystem fallback is allowed")
	}
	result := &Adapter{statuspage: statuspage, aws: aws}
	if len(ecosystem) == 1 {
		if ecosystem[0] == nil {
			return nil, errors.New("vendor profile: ecosystem fallback is nil")
		}
		result.ecosystem = ecosystem[0]
	}
	return result, nil
}

func (a *Adapter) Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error) {
	// An explicit engine is operator intent and must win over a built-in host
	// profile. Route before canonicalization so an engine-specific URL path is
	// not discarded by a vendor profile.
	if a.ecosystem != nil && isEcosystemProvider(target.Provider) {
		return a.ecosystem.Probe(ctx, target)
	}
	canonical, profile, found, err := Canonicalize(target.URL)
	if err != nil {
		return domain.Capabilities{}, err
	}
	target.URL = canonical
	delegate := a.statuspage
	if found && profile.Slug == "aws" {
		delegate = a.aws
	}
	capabilities, err := delegate.Probe(ctx, target)
	if err != nil && !found && a.ecosystem != nil {
		ecosystemCapabilities, ecosystemErr := a.ecosystem.Probe(ctx, target)
		if ecosystemErr == nil {
			return ecosystemCapabilities, nil
		}
		return domain.Capabilities{}, errors.Join(err, ecosystemErr)
	}
	if err != nil {
		return domain.Capabilities{}, err
	}
	if found {
		capabilities.Engine = profile.Engine
		capabilities.ExpiresAt = time.Now().UTC().Add(profile.CapabilityTTL)
		for resource, endpoint := range capabilities.Endpoints {
			if profile.Unsupported[resource] {
				delete(capabilities.Endpoints, resource)
				continue
			}
			if profile.MaximumBodyBytes > endpoint.MaximumBodyBytes {
				endpoint.MaximumBodyBytes = profile.MaximumBodyBytes
				capabilities.Endpoints[resource] = endpoint
			}
		}
		if profile.Slug == "openai" {
			capabilities.Endpoints[domain.ResourceIncidents] = domain.EndpointCapability{
				Resource: domain.ResourceIncidents, Path: "/api/v2/incidents.json",
				Completeness:   domain.CompletenessPartial,
				Cache:          domain.CacheCapability{DefaultTTL: 5 * time.Second},
				Pagination:     domain.PaginationCapability{Kind: domain.PaginationNone},
				Authentication: "none", MaximumBodyBytes: profile.MaximumBodyBytes,
				ExpectedMediaType: "application/json",
			}
		}
	}
	return capabilities, nil
}

func (a *Adapter) Fetch(ctx context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	if a.ecosystem != nil && (isEcosystemProvider(request.Source.Provider) || isEcosystemProvider(request.Target.Provider)) {
		return a.ecosystem.Fetch(ctx, request)
	}
	canonical, profile, found, err := Canonicalize(request.Target.URL)
	if err != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, err
	}
	request.Target.URL = canonical
	request.Source.CanonicalURL = canonical
	if found {
		request.Source.Provider = profile.Slug
	}
	if found && profile.Slug == "aws" {
		return a.aws.Fetch(ctx, request)
	}
	if found && profile.Unsupported[request.ResourceKind] {
		return domain.Snapshot{}, domain.FetchMeta{}, fmt.Errorf("vendor profile %s: resource %s is unsupported", profile.Slug, request.ResourceKind)
	}
	return a.statuspage.Fetch(ctx, request)
}

func isEcosystemProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "incident-io-widget", "incident.io", "incident-io", "instatus-v3", "instatus",
		"better-stack-status-page", "betterstack", "better-stack", "status-io-v1", "status.io", "status-io",
		"cachet-v2-v3", "cachet-v2", "cachet-v3", "cachet", "gatus-synthetic", "gatus",
		"cstate-v2", "cstate", "html-recipe":
		return true
	default:
		return false
	}
}

func (a *Adapter) DecodeWebhook(ctx context.Context, request domain.WebhookRequest) ([]domain.SourceEvent, error) {
	if request.Source.CanonicalURL != nil {
		if profile, found := Lookup(request.Source.CanonicalURL.Hostname()); found && profile.Slug == "aws" {
			return a.aws.DecodeWebhook(ctx, request)
		}
	}
	return a.statuspage.DecodeWebhook(ctx, request)
}
