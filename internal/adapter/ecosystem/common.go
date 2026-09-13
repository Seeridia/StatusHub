package ecosystem

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const (
	defaultMaximumBody = int64(8 << 20)
	AdapterVersion     = "ecosystem/1"
	adapterVersion     = AdapterVersion
	normalizerVersion  = "ecosystem/1"
)

var (
	ErrNotRecognized = errors.New("target does not expose a supported status-page ecosystem API")
	ErrInvalid       = errors.New("invalid status-page ecosystem payload")
	ErrUnsupported   = errors.New("status-page ecosystem operation is unsupported")
)

type decoder func([]byte, domain.Source, time.Time, domain.ResourceKind) (domain.Snapshot, error)

func decodeStrict(body []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func sourceForEngine(source domain.Source, engine string) domain.Source {
	if source.Provider == "" || isEcosystemEngine(source.Provider) {
		source.Provider = engine
	}
	if source.Kind == "" {
		source.Kind = domain.SourceKindStatusPage
	}
	return source
}

func parseTime(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	formats := []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05", "2006-01-02 15:04:05 -0700 MST",
		"2006-01-02 15:04:05 -0700 -0700", "2006-01-02",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func stableID(namespace string, parts ...string) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte(namespace))
	for _, part := range parts {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(strings.TrimSpace(part)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeComponentStatus(raw string) domain.ComponentStatus {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(normalized)
	switch normalized {
	case "up", "ok", "operational", "available", "healthy", "online", "passing", "success", "100", "1":
		return domain.ComponentStatusOperational
	case "degraded", "degraded_performance", "hasissues", "has_issues", "warning", "warn", "info", "minor", "200", "2":
		return domain.ComponentStatusDegraded
	case "partial", "partial_outage", "disrupted", "300", "3":
		return domain.ComponentStatusPartialOutage
	case "down", "major", "major_outage", "outage", "unavailable", "failed", "failure", "500", "4":
		return domain.ComponentStatusMajorOutage
	case "maintenance", "under_maintenance", "notice", "400":
		return domain.ComponentStatusUnderMaintenance
	default:
		return domain.ComponentStatusUnknown
	}
}

func normalizePhase(raw string, resolved bool) domain.IncidentPhase {
	if resolved {
		return domain.IncidentPhaseResolved
	}
	normalized := strings.ToLower(strings.TrimSpace(raw))
	normalized = strings.NewReplacer(" ", "_", "-", "_").Replace(normalized)
	switch normalized {
	case "investigating", "open", "started", "reported", "1":
		return domain.IncidentPhaseInvestigating
	case "identified", "acknowledged", "scheduled", "upcoming", "maintenance_scheduled", "2":
		return domain.IncidentPhaseIdentified
	case "monitoring", "watching", "in_progress", "maintenance_in_progress", "3":
		return domain.IncidentPhaseMonitoring
	case "resolved", "fixed", "completed", "complete", "closed", "maintenance_complete", "4":
		return domain.IncidentPhaseResolved
	default:
		return domain.IncidentPhaseUnknown
	}
}

func normalizeImpact(raw string) domain.Impact {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch normalized {
	case "none", "operational", "resolved", "0":
		return domain.ImpactNone
	case "minor", "degraded", "warning", "1":
		return domain.ImpactMinor
	case "major", "partial_outage", "2":
		return domain.ImpactMajor
	case "critical", "major_outage", "down", "3", "4":
		return domain.ImpactCritical
	default:
		return domain.ImpactUnknown
	}
}

func impactFromStatus(status domain.ComponentStatus) domain.Impact {
	switch status {
	case domain.ComponentStatusDegraded:
		return domain.ImpactMinor
	case domain.ComponentStatusPartialOutage:
		return domain.ImpactMajor
	case domain.ComponentStatusMajorOutage:
		return domain.ImpactCritical
	case domain.ComponentStatusOperational:
		return domain.ImpactNone
	default:
		return domain.ImpactUnknown
	}
}

func worstStatus(values ...domain.ComponentStatus) domain.ComponentStatus {
	rank := map[domain.ComponentStatus]int{
		domain.ComponentStatusUnknown: 0, domain.ComponentStatusOperational: 1,
		domain.ComponentStatusUnderMaintenance: 2, domain.ComponentStatusDegraded: 3,
		domain.ComponentStatusPartialOutage: 4, domain.ComponentStatusMajorOutage: 5,
	}
	result := domain.ComponentStatusUnknown
	for _, value := range values {
		if rank[value] > rank[result] {
			result = value
		}
	}
	return result
}

func computedStatus(components []domain.Component) domain.ComponentStatus {
	values := make([]domain.ComponentStatus, 0, len(components))
	for _, component := range components {
		values = append(values, component.Status)
	}
	if len(values) == 0 {
		return domain.ComponentStatusUnknown
	}
	return worstStatus(values...)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, found := seen[value]; found {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateAbsoluteHTTP(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return parsed.String()
}

func schemaHash(body []byte) (string, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return "", fmt.Errorf("schema fingerprint: %w", err)
	}
	shape := make([]string, 0, 64)
	walkShape(value, "$", &shape, 0)
	sort.Strings(shape)
	hash := sha256.Sum256([]byte(strings.Join(shape, "\n")))
	return hex.EncodeToString(hash[:]), nil
}

func walkShape(value any, path string, shape *[]string, depth int) {
	if depth > 32 {
		*shape = append(*shape, path+":depth_limit")
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		*shape = append(*shape, path+":object")
		for key, child := range typed {
			walkShape(child, path+"."+key, shape, depth+1)
		}
	case []any:
		*shape = append(*shape, path+":array")
		for index, child := range typed {
			if index >= 3 {
				break
			}
			walkShape(child, path+"[]", shape, depth+1)
		}
	case nil:
		*shape = append(*shape, path+":null")
	case bool:
		*shape = append(*shape, path+":bool")
	case string:
		*shape = append(*shape, path+":string")
	case float64, json.Number:
		*shape = append(*shape, path+":number")
	}
}
