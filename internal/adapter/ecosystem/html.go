package ecosystem

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

const EngineHTMLRecipe = "html-recipe"

// HTMLRecipe is deliberately declarative. It cannot execute script, make
// secondary requests, or infer authority from absence. Recipes are registered
// by operators for an exact host and compiled before the adapter starts.
type HTMLRecipe struct {
	Host                    string `json:"host"`
	Path                    string `json:"path"`
	OverallStatusSelector   string `json:"overall_status_selector,omitempty"`
	ComponentSelector       string `json:"component_selector"`
	ComponentNameSelector   string `json:"component_name_selector"`
	ComponentStatusSelector string `json:"component_status_selector"`
	IncidentSelector        string `json:"incident_selector,omitempty"`
	IncidentTitleSelector   string `json:"incident_title_selector,omitempty"`
	IncidentBodySelector    string `json:"incident_body_selector,omitempty"`
	IncidentStatusSelector  string `json:"incident_status_selector,omitempty"`
	MaximumMatches          int    `json:"maximum_matches,omitempty"`
}

type compiledHTMLRecipe struct {
	host            string
	path            string
	overallStatus   cascadia.SelectorGroup
	component       cascadia.SelectorGroup
	componentName   cascadia.SelectorGroup
	componentStatus cascadia.SelectorGroup
	incident        cascadia.SelectorGroup
	incidentTitle   cascadia.SelectorGroup
	incidentBody    cascadia.SelectorGroup
	incidentStatus  cascadia.SelectorGroup
	maximumMatches  int
}

func compileHTMLRecipe(recipe HTMLRecipe) (compiledHTMLRecipe, error) {
	host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(recipe.Host), "."))
	if host == "" || strings.ContainsAny(host, "/:@?#") {
		return compiledHTMLRecipe{}, errors.New("ecosystem adapter: HTML recipe requires an exact hostname")
	}
	path := strings.TrimSpace(recipe.Path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return compiledHTMLRecipe{}, errors.New("ecosystem adapter: HTML recipe path must be same-origin")
	}
	if strings.TrimSpace(recipe.ComponentSelector) == "" || strings.TrimSpace(recipe.ComponentNameSelector) == "" || strings.TrimSpace(recipe.ComponentStatusSelector) == "" {
		return compiledHTMLRecipe{}, errors.New("ecosystem adapter: HTML recipe component, name and status selectors are required")
	}
	compile := func(name, selector string, optional bool) (cascadia.SelectorGroup, error) {
		selector = strings.TrimSpace(selector)
		if selector == "" && optional {
			return nil, nil
		}
		if len(selector) > 512 {
			return nil, fmt.Errorf("ecosystem adapter: HTML recipe %s selector is too long", name)
		}
		compiled, err := cascadia.ParseGroup(selector)
		if err != nil {
			return nil, fmt.Errorf("ecosystem adapter: invalid HTML recipe %s selector: %w", name, err)
		}
		return compiled, nil
	}
	result := compiledHTMLRecipe{host: host, path: path, maximumMatches: recipe.MaximumMatches}
	if result.maximumMatches == 0 {
		result.maximumMatches = 500
	}
	if result.maximumMatches < 1 || result.maximumMatches > 5000 {
		return compiledHTMLRecipe{}, errors.New("ecosystem adapter: HTML recipe maximum matches must be in [1,5000]")
	}
	var err error
	selectors := []struct {
		name     string
		value    string
		optional bool
		target   *cascadia.SelectorGroup
	}{
		{"overall status", recipe.OverallStatusSelector, true, &result.overallStatus},
		{"component", recipe.ComponentSelector, false, &result.component},
		{"component name", recipe.ComponentNameSelector, false, &result.componentName},
		{"component status", recipe.ComponentStatusSelector, false, &result.componentStatus},
		{"incident", recipe.IncidentSelector, true, &result.incident},
		{"incident title", recipe.IncidentTitleSelector, true, &result.incidentTitle},
		{"incident body", recipe.IncidentBodySelector, true, &result.incidentBody},
		{"incident status", recipe.IncidentStatusSelector, true, &result.incidentStatus},
	}
	for _, selector := range selectors {
		*selector.target, err = compile(selector.name, selector.value, selector.optional)
		if err != nil {
			return compiledHTMLRecipe{}, err
		}
	}
	if result.incident != nil && result.incidentTitle == nil {
		return compiledHTMLRecipe{}, errors.New("ecosystem adapter: HTML incident selector requires a title selector")
	}
	return result, nil
}

func (a *Adapter) htmlDefinition(host string) engineDefinition {
	recipe := a.recipes[host]
	return engineDefinition{name: EngineHTMLRecipe, version: "recipe-v1", confidence: .70, endpoints: func(domain.Target) ([]endpointDefinition, error) {
		return []endpointDefinition{{resource: domain.ResourceSummary, path: recipe.path, required: true,
			completeness: domain.CompletenessPartial, decode: recipe.decode}}, nil
	}}
}

func (recipe compiledHTMLRecipe) decode(body []byte, source domain.Source, observedAt time.Time, resource domain.ResourceKind) (domain.Snapshot, error) {
	document, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe parse: %v", ErrInvalid, err)
	}
	componentNodes := cascadia.QueryAll(document, recipe.component)
	if len(componentNodes) == 0 {
		return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe matched no components", ErrInvalid)
	}
	if len(componentNodes) > recipe.maximumMatches {
		return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe component match limit exceeded", ErrInvalid)
	}
	components := make([]domain.Component, 0, len(componentNodes))
	for index, node := range componentNodes {
		name := selectedText(node, recipe.componentName)
		status := selectedText(node, recipe.componentStatus)
		if name == "" || status == "" {
			return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe component[%d] name/status is empty", ErrInvalid, index)
		}
		id := stableID("html-component", recipe.host, name)
		components = append(components, domain.Component{ID: id, Name: name,
			Status: normalizeComponentStatus(status), RawStatus: status,
			Tags: map[string]string{"origin": "html_recipe", "recipe_host": recipe.host}})
	}
	incidents := make([]domain.Incident, 0)
	if recipe.incident != nil {
		incidentNodes := cascadia.QueryAll(document, recipe.incident)
		if len(incidentNodes) > recipe.maximumMatches {
			return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe incident match limit exceeded", ErrInvalid)
		}
		for index, node := range incidentNodes {
			title := selectedText(node, recipe.incidentTitle)
			if title == "" {
				return domain.Snapshot{}, fmt.Errorf("%w: HTML recipe incident[%d] title is empty", ErrInvalid, index)
			}
			bodyText, rawPhase := selectedText(node, recipe.incidentBody), selectedText(node, recipe.incidentStatus)
			incidentID := stableID("html-incident", recipe.host, title)
			phase := normalizePhase(rawPhase, false)
			if phase == domain.IncidentPhaseUnknown {
				phase = domain.IncidentPhaseInvestigating
			}
			incident := domain.Incident{ID: incidentID, Kind: domain.IncidentKindIncident, Name: title,
				Phase: phase, RawPhase: rawPhase, Impact: domain.ImpactUnknown, RawImpact: "html_recipe"}
			if bodyText != "" {
				incident.Updates = []domain.IncidentUpdate{{ID: stableID("html-update", incidentID, bodyText), IncidentID: incidentID,
					Body: bodyText, Phase: phase, RawPhase: rawPhase, Impact: domain.ImpactUnknown}}
			}
			incidents = append(incidents, incident)
		}
	}
	rawOverall := selectedText(document, recipe.overallStatus)
	overall := normalizeComponentStatus(rawOverall)
	if overall == domain.ComponentStatusUnknown {
		overall = computedStatus(components)
	}
	source = sourceForEngine(source, EngineHTMLRecipe)
	return domain.Snapshot{Source: source, ResourceKind: resource, OverallStatus: overall, RawOverallStatus: rawOverall,
		ComputedStatus: computedStatus(components), Components: components, Incidents: incidents,
		// HTML DOMs are never authoritative: template changes can hide nodes and
		// must not synthesize resolution through absence.
		Completeness: domain.CompletenessPartial, ObservedAt: observedAt,
		SchemaVersion: "recipe-v1", AdapterVersion: adapterVersion, NormalizerVersion: normalizerVersion}, nil
}

func selectedText(root *html.Node, selector cascadia.SelectorGroup) string {
	if root == nil || selector == nil {
		return ""
	}
	node := cascadia.Query(root, selector)
	if node == nil {
		return ""
	}
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if builder.Len() >= 8192 {
			return
		}
		if current.Type == html.TextNode {
			builder.WriteString(current.Data)
			builder.WriteByte(' ')
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return ' '
		}
		return character
	}, builder.String()))
}
