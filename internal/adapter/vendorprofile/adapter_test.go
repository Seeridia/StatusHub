package vendorprofile

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type routingAdapter struct {
	engine       string
	probeErr     error
	probes       int
	fetches      int
	lastProbeURL string
	lastFetchURL string
}

func (a *routingAdapter) Probe(_ context.Context, target domain.Target) (domain.Capabilities, error) {
	a.probes++
	if target.URL != nil {
		a.lastProbeURL = target.URL.String()
	}
	return domain.Capabilities{Engine: a.engine}, a.probeErr
}
func (a *routingAdapter) Fetch(_ context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	a.fetches++
	if request.Target.URL != nil {
		a.lastFetchURL = request.Target.URL.String()
	}
	return domain.Snapshot{AdapterVersion: a.engine}, domain.FetchMeta{}, nil
}
func (*routingAdapter) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, nil
}

func TestCanonicalizeClaudeAliasAndOpenAICapability(t *testing.T) {
	input, _ := url.Parse("https://status.anthropic.com/incidents")
	canonical, profile, found, err := Canonicalize(input)
	if err != nil || !found || profile.Slug != "anthropic" || canonical.String() != "https://status.claude.com" {
		t.Fatalf("canonical=%v profile=%#v found=%v err=%v", canonical, profile, found, err)
	}
	openAI, ok := Lookup("status.openai.com")
	if !ok || !openAI.Unsupported[domain.ResourceUnresolvedIncidents] {
		t.Fatalf("OpenAI profile=%#v", openAI)
	}
}

func TestUnknownEngineUsesEcosystemFallbackAndSteadyStateRouting(t *testing.T) {
	statuspage := &routingAdapter{engine: "statuspage", probeErr: errors.New("not statuspage")}
	aws := &routingAdapter{engine: "aws"}
	ecosystem := &routingAdapter{engine: "gatus-synthetic"}
	adapter, err := New(statuspage, aws, ecosystem)
	if err != nil {
		t.Fatal(err)
	}
	targetURL, _ := url.Parse("https://custom.example.test")
	capabilities, err := adapter.Probe(context.Background(), domain.Target{URL: targetURL, Provider: "gatus"})
	if err != nil || capabilities.Engine != "gatus-synthetic" || statuspage.probes != 0 || ecosystem.probes != 1 {
		t.Fatalf("capabilities=%#v statuspage=%#v ecosystem=%#v err=%v", capabilities, statuspage, ecosystem, err)
	}
	_, _, err = adapter.Fetch(context.Background(), domain.FetchRequest{Target: domain.Target{URL: targetURL},
		Source: domain.Source{Provider: "gatus-synthetic"}, ResourceKind: domain.ResourceSummary})
	if err != nil || statuspage.fetches != 0 || ecosystem.fetches != 1 {
		t.Fatalf("statuspage=%#v ecosystem=%#v err=%v", statuspage, ecosystem, err)
	}
}

func TestExplicitEcosystemProviderOverridesBuiltinHostProfile(t *testing.T) {
	statuspage := &routingAdapter{engine: "statuspage"}
	aws := &routingAdapter{engine: "aws"}
	ecosystem := &routingAdapter{engine: "html-recipe"}
	adapter, err := New(statuspage, aws, ecosystem)
	if err != nil {
		t.Fatal(err)
	}
	targetURL, _ := url.Parse("https://www.githubstatus.com/custom-widget?token=opaque")
	capabilities, err := adapter.Probe(context.Background(), domain.Target{URL: targetURL, Provider: "html-recipe"})
	if err != nil || capabilities.Engine != "html-recipe" || statuspage.probes != 0 || ecosystem.probes != 1 || ecosystem.lastProbeURL != targetURL.String() {
		t.Fatalf("capabilities=%#v statuspage=%#v ecosystem=%#v err=%v", capabilities, statuspage, ecosystem, err)
	}
	_, _, err = adapter.Fetch(context.Background(), domain.FetchRequest{
		Target: domain.Target{URL: targetURL, Provider: "html-recipe"},
		Source: domain.Source{Provider: "html-recipe"}, ResourceKind: domain.ResourceSummary,
	})
	if err != nil || statuspage.fetches != 0 || ecosystem.fetches != 1 || ecosystem.lastFetchURL != targetURL.String() {
		t.Fatalf("statuspage=%#v ecosystem=%#v err=%v", statuspage, ecosystem, err)
	}
}
