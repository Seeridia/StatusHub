package notify

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/domain"
)

type eventState struct {
	Name        string                  `json:"name"`
	Status      string                  `json:"status"`
	Phase       string                  `json:"phase"`
	Impact      string                  `json:"impact"`
	Description string                  `json:"description"`
	URL         string                  `json:"url"`
	Updates     []domain.IncidentUpdate `json:"updates"`
}
type eventDetails struct {
	Current  eventState `json:"current"`
	Previous eventState `json:"previous"`
}

func parseEventDetails(data json.RawMessage) eventDetails {
	var details eventDetails
	_ = json.Unmarshal(data, &details)
	return details
}
func latestBody(updates []domain.IncidentUpdate) string {
	var body string
	var latest time.Time
	for _, update := range updates {
		if strings.TrimSpace(update.Body) == "" {
			continue
		}
		var at time.Time
		if update.SourceUpdatedAt != nil {
			at = *update.SourceUpdatedAt
		} else if update.SourceCreatedAt != nil {
			at = *update.SourceCreatedAt
		}
		if body == "" || !at.Before(latest) {
			body, latest = strings.TrimSpace(update.Body), at
		}
	}
	return body
}
func statusChange(before, after string) string {
	before, after = strings.TrimSpace(before), strings.TrimSpace(after)
	if after == "" {
		return ""
	}
	if before != "" && before != after {
		return before + " → " + after
	}
	return after
}

// EventSummary handles both component changes and incident updates without empty separators.
func EventSummary(data json.RawMessage) string {
	d := parseEventDetails(data)
	parts := []string{d.Current.Name, statusChange(d.Previous.Status, d.Current.Status), statusChange(d.Previous.Phase, d.Current.Phase), d.Current.Impact, d.Current.Description, latestBody(d.Current.Updates)}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			filtered = append(filtered, part)
		}
	}
	return strings.Join(filtered, " — ")
}
