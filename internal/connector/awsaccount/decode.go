// Package awsaccount verifies and normalizes tenant-scoped AWS Health events
// delivered by EventBridge through a signed SNS HTTPS subscription.
package awsaccount

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const (
	Provider          = "aws-account-health"
	SchemaVersion     = "aws-health-eventbridge/v1"
	NormalizerVersion = "aws-account-health/1"
)

type Config struct {
	SourceID          string
	ExternalAccountID string
	AllowedRegions    []string
	AllowedServices   []string
}

type eventBridgeEnvelope struct {
	Version    string          `json:"version"`
	ID         string          `json:"id"`
	DetailType string          `json:"detail-type"`
	Source     string          `json:"source"`
	Account    string          `json:"account"`
	Time       string          `json:"time"`
	Region     string          `json:"region"`
	Resources  []string        `json:"resources"`
	Detail     awsHealthDetail `json:"detail"`
}

type awsHealthDetail struct {
	EventARN          string             `json:"eventArn"`
	Service           string             `json:"service"`
	EventTypeCode     string             `json:"eventTypeCode"`
	EventTypeCategory string             `json:"eventTypeCategory"`
	EventScopeCode    string             `json:"eventScopeCode"`
	StatusCode        string             `json:"statusCode"`
	EventRegion       string             `json:"eventRegion"`
	StartTime         string             `json:"startTime"`
	EndTime           string             `json:"endTime"`
	LastUpdatedTime   string             `json:"lastUpdatedTime"`
	EventDescription  []eventDescription `json:"eventDescription"`
	AffectedEntities  []affectedEntity   `json:"affectedEntities"`
	EventMetadata     map[string]string  `json:"eventMetadata"`
}

type eventDescription struct {
	Language          string `json:"language"`
	LatestDescription string `json:"latestDescription"`
}

type affectedEntity struct {
	EntityValue     string `json:"entityValue"`
	StatusCode      string `json:"statusCode"`
	LastUpdatedTime string `json:"lastUpdatedTime"`
}

type projection struct {
	Current struct {
		Name              string           `json:"name"`
		Phase             string           `json:"phase"`
		Impact            string           `json:"impact"`
		Service           string           `json:"service"`
		Region            string           `json:"region"`
		AccountID         string           `json:"account_id"`
		EventARN          string           `json:"event_arn"`
		EventTypeCode     string           `json:"event_type_code"`
		EventTypeCategory string           `json:"event_type_category"`
		EventScopeCode    string           `json:"event_scope_code,omitempty"`
		Description       string           `json:"description,omitempty"`
		Resources         []string         `json:"resources,omitempty"`
		AffectedEntities  []affectedEntity `json:"affected_entities,omitempty"`
		StartedAt         *time.Time       `json:"started_at,omitempty"`
		EndedAt           *time.Time       `json:"ended_at,omitempty"`
	} `json:"current"`
	Upstream struct {
		EventBridgeID string `json:"eventbridge_id"`
	} `json:"upstream"`
}

func Decode(message []byte, config Config, observedAt time.Time) (domain.CanonicalEvent, string, error) {
	if strings.TrimSpace(config.SourceID) == "" || strings.TrimSpace(config.ExternalAccountID) == "" {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: connector source and account are required")
	}
	if len(message) == 0 || len(message) > 1<<20 {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: event body is empty or too large")
	}
	var envelope eventBridgeEnvelope
	if err := json.Unmarshal(message, &envelope); err != nil {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: invalid EventBridge JSON")
	}
	if envelope.Version != "0" || envelope.ID == "" || envelope.Source != "aws.health" ||
		!strings.HasPrefix(envelope.DetailType, "AWS Health Event") || envelope.Account != config.ExternalAccountID {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: EventBridge identity does not match connector")
	}
	detail := envelope.Detail
	if detail.EventARN == "" || detail.Service == "" || detail.EventTypeCode == "" || detail.EventTypeCategory == "" {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: required event detail is missing")
	}
	if len(envelope.Resources) > 1000 || len(detail.AffectedEntities) > 1000 {
		return domain.CanonicalEvent{}, "", errors.New("aws account health: resource match limit exceeded")
	}
	region := detail.EventRegion
	if region == "" {
		region = envelope.Region
	}
	if !allowed(config.AllowedRegions, region) || !allowed(config.AllowedServices, detail.Service) {
		return domain.CanonicalEvent{}, "", ErrFiltered
	}
	phase, kind, entityKind, impact, err := normalize(detail)
	if err != nil {
		return domain.CanonicalEvent{}, "", err
	}
	updatedAt, err := parseRequiredTime(firstNonempty(detail.LastUpdatedTime, envelope.Time))
	if err != nil {
		return domain.CanonicalEvent{}, "", fmt.Errorf("aws account health: invalid update time: %w", err)
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	var current projection
	current.Current.Name = detail.EventTypeCode
	current.Current.Phase = string(phase)
	current.Current.Impact = string(impact)
	current.Current.Service = strings.ToUpper(detail.Service)
	current.Current.Region = strings.ToLower(region)
	current.Current.AccountID = envelope.Account
	current.Current.EventARN = detail.EventARN
	current.Current.EventTypeCode = detail.EventTypeCode
	current.Current.EventTypeCategory = detail.EventTypeCategory
	current.Current.EventScopeCode = detail.EventScopeCode
	current.Current.Description = preferredDescription(detail.EventDescription)
	current.Current.Resources = append([]string(nil), envelope.Resources...)
	current.Current.AffectedEntities = append([]affectedEntity(nil), detail.AffectedEntities...)
	current.Current.StartedAt = parseOptionalTime(detail.StartTime)
	current.Current.EndedAt = parseOptionalTime(detail.EndTime)
	current.Upstream.EventBridgeID = envelope.ID
	payload, err := domain.CanonicalJSON(current)
	if err != nil {
		return domain.CanonicalEvent{}, "", err
	}
	semanticHash, err := domain.SemanticHash(NormalizerVersion, current)
	if err != nil {
		return domain.CanonicalEvent{}, "", err
	}
	identity := domain.SourceEventIdentity{SourceID: config.SourceID, Provider: Provider,
		Kind: kind, EntityKind: entityKind, EntityID: detail.EventARN, UpstreamEventID: envelope.ID,
		SourceUpdatedAt: &updatedAt, NormalizerVersion: NormalizerVersion, SemanticProjectionHash: semanticHash}
	key, err := identity.Key()
	if err != nil {
		return domain.CanonicalEvent{}, "", err
	}
	subject := "statusmon.events.normal"
	if impact == domain.ImpactCritical {
		subject = "statusmon.events.critical"
	}
	return domain.CanonicalEvent{SourceID: config.SourceID, Provider: Provider, Kind: kind,
		EntityKind: entityKind, EntityID: detail.EventARN, SourceEventKey: key,
		NormalizerVersion: NormalizerVersion, SchemaVersion: SchemaVersion, Payload: payload,
		SourceUpdatedAt: &updatedAt, ObservedAt: observedAt.UTC()}, subject, nil
}

var ErrFiltered = errors.New("aws account health: event is outside connector allowlist")

func normalize(detail awsHealthDetail) (domain.IncidentPhase, domain.EventKind, domain.EntityKind, domain.Impact, error) {
	status := strings.ToLower(strings.TrimSpace(detail.StatusCode))
	category := strings.ToLower(strings.TrimSpace(detail.EventTypeCategory))
	severity := strings.ToLower(firstNonempty(detail.EventMetadata["severity"], detail.EventMetadata["eventSeverity"]))
	impact := domain.ImpactMajor
	if category == "accountnotification" || category == "scheduledchange" {
		impact = domain.ImpactMinor
	}
	if severity == "critical" {
		impact = domain.ImpactCritical
	}
	if category == "scheduledchange" {
		switch status {
		case "upcoming":
			return domain.IncidentPhaseIdentified, domain.EventKindMaintenanceScheduled, domain.EntityMaintenance, impact, nil
		case "open":
			return domain.IncidentPhaseMonitoring, domain.EventKindMaintenanceStarted, domain.EntityMaintenance, impact, nil
		case "closed":
			return domain.IncidentPhaseResolved, domain.EventKindMaintenanceCompleted, domain.EntityMaintenance, domain.ImpactNone, nil
		default:
			return "", "", "", "", errors.New("aws account health: unsupported scheduled event status")
		}
	}
	if category != "issue" && category != "investigation" && category != "accountnotification" {
		return "", "", "", "", errors.New("aws account health: unsupported event category")
	}
	switch status {
	case "open", "upcoming":
		return domain.IncidentPhaseInvestigating, domain.EventKindIncidentCreated, domain.EntityIncident, impact, nil
	case "closed":
		return domain.IncidentPhaseResolved, domain.EventKindIncidentResolved, domain.EntityIncident, domain.ImpactNone, nil
	default:
		return "", "", "", "", errors.New("aws account health: unsupported event status")
	}
}

func allowed(allowlist []string, value string) bool {
	if len(allowlist) == 0 {
		return true
	}
	value = strings.ToUpper(strings.TrimSpace(value))
	for _, allowedValue := range allowlist {
		if strings.ToUpper(strings.TrimSpace(allowedValue)) == value {
			return true
		}
	}
	return false
}

func preferredDescription(descriptions []eventDescription) string {
	for _, description := range descriptions {
		if strings.EqualFold(description.Language, "en_US") && strings.TrimSpace(description.LatestDescription) != "" {
			return description.LatestDescription
		}
	}
	for _, description := range descriptions {
		if strings.TrimSpace(description.LatestDescription) != "" {
			return description.LatestDescription
		}
	}
	return ""
}

func parseRequiredTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseOptionalTime(value string) *time.Time {
	parsed, err := parseRequiredTime(value)
	if err != nil {
		return nil
	}
	return &parsed
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
