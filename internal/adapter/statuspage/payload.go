package statuspage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// Statuspage's public v2 API is deliberately decoded into private wire types.
// Keeping these types private prevents the provider schema from leaking into
// the canonical domain model while still allowing new upstream fields.
type pagePayload struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	TimeZone  string    `json:"time_zone"`
	UpdatedAt timestamp `json:"updated_at"`
}

type summaryPayload struct {
	Page                  *pagePayload       `json:"page"`
	Status                *statusPayload     `json:"status"`
	Components            []componentPayload `json:"components"`
	Incidents             []incidentPayload  `json:"incidents"`
	ScheduledMaintenances json.RawMessage    `json:"scheduled_maintenances"`
}

type incidentsPayload struct {
	Page      *pagePayload      `json:"page"`
	Incidents []incidentPayload `json:"incidents"`
}

type statusPayload struct {
	Indicator   string `json:"indicator"`
	Description string `json:"description"`
}

type componentPayload struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	CreatedAt          timestamp `json:"created_at"`
	UpdatedAt          timestamp `json:"updated_at"`
	Position           int       `json:"position"`
	Description        string    `json:"description"`
	Showcase           bool      `json:"showcase"`
	StartDate          string    `json:"start_date"`
	GroupID            *string   `json:"group_id"`
	PageID             string    `json:"page_id"`
	Group              bool      `json:"group"`
	OnlyShowIfDegraded bool      `json:"only_show_if_degraded"`
}

type incidentPayload struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Status            string                  `json:"status"`
	CreatedAt         timestamp               `json:"created_at"`
	UpdatedAt         timestamp               `json:"updated_at"`
	MonitoringAt      timestamp               `json:"monitoring_at"`
	ResolvedAt        timestamp               `json:"resolved_at"`
	Impact            string                  `json:"impact"`
	Shortlink         string                  `json:"shortlink"`
	StartedAt         timestamp               `json:"started_at"`
	PageID            string                  `json:"page_id"`
	IncidentUpdates   []incidentUpdatePayload `json:"incident_updates"`
	Components        []componentPayload      `json:"components"`
	ReminderIntervals json.RawMessage         `json:"reminder_intervals"`
}

type incidentUpdatePayload struct {
	ID                   string                     `json:"id"`
	Status               string                     `json:"status"`
	Body                 string                     `json:"body"`
	IncidentID           string                     `json:"incident_id"`
	CreatedAt            timestamp                  `json:"created_at"`
	UpdatedAt            timestamp                  `json:"updated_at"`
	DisplayAt            timestamp                  `json:"display_at"`
	AffectedComponents   []affectedComponentPayload `json:"affected_components"`
	DeliverNotifications bool                       `json:"deliver_notifications"`
}

type affectedComponentPayload struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	OldStatus string `json:"old_status"`
	NewStatus string `json:"new_status"`
}

// timestamp accepts the nullable RFC3339 timestamps emitted by Statuspage.
// A malformed, non-empty timestamp remains a hard schema error instead of
// being silently replaced with the collector's clock.
type timestamp struct {
	time.Time
	Valid bool
}

func (t *timestamp) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		t.Time = time.Time{}
		t.Valid = false
		return nil
	}

	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("statuspage timestamp must be a string or null: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return fmt.Errorf("invalid statuspage timestamp %q: %w", raw, err)
	}
	t.Time = parsed
	t.Valid = true
	return nil
}

func decodeJSON(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("decode statuspage JSON: %w", err)
	}
	var extra any
	switch err := decoder.Decode(&extra); {
	case err == nil:
		return fmt.Errorf("decode statuspage JSON: multiple values")
	case err != io.EOF:
		return fmt.Errorf("decode statuspage JSON trailing data: %w", err)
	}
	return nil
}

// schemaHash fingerprints JSON structure, not values. It is useful for drift
// telemetry and is stable across map iteration order and changing incident IDs.
func schemaHash(data []byte) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode schema: %w", err)
	}

	pathSet := make(map[string]struct{}, 32)
	collectSchemaPaths("$", value, pathSet)
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(fmt.Sprintf("statuspage-v2\n%s", joinLines(paths))))
	return hex.EncodeToString(sum[:]), nil
}

func collectSchemaPaths(path string, value any, paths map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		paths[path+":object"] = struct{}{}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			collectSchemaPaths(path+"."+key, typed[key], paths)
		}
	case []any:
		paths[path+":array"] = struct{}{}
		// Union the shapes of all elements. This handles nullable fields and
		// heterogeneous provider extensions without depending on item order.
		for _, item := range typed {
			collectSchemaPaths(path+"[]", item, paths)
		}
	case nil:
		paths[path+":null"] = struct{}{}
	case string:
		paths[path+":string"] = struct{}{}
	case json.Number:
		paths[path+":number"] = struct{}{}
	case bool:
		paths[path+":boolean"] = struct{}{}
	default:
		paths[path+":unknown"] = struct{}{}
	}
}

func joinLines(values []string) string {
	var buffer bytes.Buffer
	for _, value := range values {
		buffer.WriteString(value)
		buffer.WriteByte('\n')
	}
	return buffer.String()
}
