// Package subscription evaluates the exact, non-indexable part of a
// subscription rule after PostgreSQL has performed the coarse scope match.
package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

type QuietHours struct {
	Timezone string `json:"timezone"`
	From     string `json:"from"`
	To       string `json:"to"`
}

type DeliveryPolicy struct {
	CriticalBypassQuietHours bool `json:"critical_bypass_quiet_hours"`
}

type Rule struct {
	MinimumImpact   domain.Impact  `json:"minimum_impact,omitempty"`
	IncludeKeywords []string       `json:"include_keywords,omitempty"`
	ExcludeKeywords []string       `json:"exclude_keywords,omitempty"`
	QuietHours      *QuietHours    `json:"quiet_hours,omitempty"`
	DeliveryPolicy  DeliveryPolicy `json:"delivery_policy,omitempty"`
}

type Event struct {
	Kind       domain.EventKind
	Payload    json.RawMessage
	ObservedAt time.Time
}

type Decision struct {
	Matched    bool
	EligibleAt time.Time
	Priority   int16
	Impact     domain.Impact
}

func Evaluate(raw json.RawMessage, event Event, now time.Time) (Decision, error) {
	if !event.Kind.Valid() || len(event.Payload) == 0 || !json.Valid(event.Payload) {
		return Decision{}, errors.New("subscription: invalid event")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var rule Rule
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &rule); err != nil {
			return Decision{}, fmt.Errorf("subscription: decode rule: %w", err)
		}
	}
	impact, searchable := eventImpact(event.Payload, event.Kind)
	if !meetsMinimum(impact, rule.MinimumImpact) {
		return Decision{Impact: impact}, nil
	}
	searchable = strings.ToLower(searchable)
	if !matchesKeywords(searchable, rule.IncludeKeywords, true) ||
		matchesKeywords(searchable, rule.ExcludeKeywords, false) {
		return Decision{Impact: impact}, nil
	}
	decision := Decision{Matched: true, EligibleAt: now.UTC(), Priority: priority(impact), Impact: impact}
	if rule.QuietHours != nil && !(impact == domain.ImpactCritical && rule.DeliveryPolicy.CriticalBypassQuietHours) {
		end, quiet, err := quietHoursEnd(*rule.QuietHours, now)
		if err != nil {
			return Decision{}, err
		}
		if quiet {
			decision.EligibleAt = end.UTC()
		}
	}
	return decision, nil
}

func eventImpact(payload json.RawMessage, kind domain.EventKind) (domain.Impact, string) {
	var object map[string]any
	_ = json.Unmarshal(payload, &object)
	current, _ := object["current"].(map[string]any)
	raw, _ := current["impact"].(string)
	impact := domain.Impact(raw)
	if !impact.Valid() || impact == domain.ImpactUnknown {
		status, _ := current["status"].(string)
		switch domain.ComponentStatus(status) {
		case domain.ComponentStatusMajorOutage:
			impact = domain.ImpactCritical
		case domain.ComponentStatusPartialOutage:
			impact = domain.ImpactMajor
		case domain.ComponentStatusDegraded:
			impact = domain.ImpactMinor
		default:
			if kind == domain.EventKindIncidentResolved || kind == domain.EventKindSourceRecovered {
				impact = domain.ImpactNone
			} else {
				impact = domain.ImpactUnknown
			}
		}
	}
	return impact, string(payload)
}

func meetsMinimum(actual, minimum domain.Impact) bool {
	if minimum == "" || minimum == domain.ImpactUnknown {
		return true
	}
	rank := map[domain.Impact]int{
		domain.ImpactUnknown: 0, domain.ImpactNone: 1, domain.ImpactMinor: 2,
		domain.ImpactMajor: 3, domain.ImpactCritical: 4,
	}
	return rank[actual] >= rank[minimum]
}

func matchesKeywords(text string, values []string, emptyMatches bool) bool {
	matchedAny := false
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" && strings.Contains(text, value) {
			matchedAny = true
			break
		}
	}
	if len(values) == 0 {
		return emptyMatches
	}
	return matchedAny
}

func priority(impact domain.Impact) int16 {
	switch impact {
	case domain.ImpactCritical:
		return 100
	case domain.ImpactMajor:
		return 50
	case domain.ImpactMinor:
		return 20
	default:
		return 0
	}
}

func quietHoursEnd(rule QuietHours, now time.Time) (time.Time, bool, error) {
	location, err := time.LoadLocation(strings.TrimSpace(rule.Timezone))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("subscription: quiet-hours timezone: %w", err)
	}
	fromHour, fromMinute, err := parseClock(rule.From)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("subscription: quiet-hours from: %w", err)
	}
	toHour, toMinute, err := parseClock(rule.To)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("subscription: quiet-hours to: %w", err)
	}
	local := now.In(location)
	start := time.Date(local.Year(), local.Month(), local.Day(), fromHour, fromMinute, 0, 0, location)
	end := time.Date(local.Year(), local.Month(), local.Day(), toHour, toMinute, 0, 0, location)
	if !end.After(start) {
		if local.Before(end) {
			start = start.AddDate(0, 0, -1)
		} else {
			end = end.AddDate(0, 0, 1)
		}
	}
	return end, !local.Before(start) && local.Before(end), nil
}

func parseClock(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, errors.New("expected HH:MM")
	}
	return parsed.Hour(), parsed.Minute(), nil
}
