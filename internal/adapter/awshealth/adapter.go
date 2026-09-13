// Package awshealth implements the experimental AWS public Health Dashboard
// adapter. The undocumented UTF-16 currentevents feed is preferred for low
// latency and the documented RSS feed is used as a compatibility fallback.
package awshealth

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	adaptercontract "github.com/Seeridia/StatusHub/internal/adapter"
	"github.com/Seeridia/StatusHub/internal/domain"
	"github.com/Seeridia/StatusHub/internal/transport"
)

const (
	Engine            = "aws-public-health"
	Version           = "experimental-v1"
	AdapterVersion    = "aws-public-health/1"
	NormalizerVersion = "aws-public-health/1"
	CurrentEventsURL  = "https://health.aws.amazon.com/public/currentevents"
	RSSURL            = "https://status.aws.amazon.com/rss/all.rss"
)

type Adapter struct {
	client transport.Client
	now    func() time.Time
}

var _ adaptercontract.Adapter = (*Adapter)(nil)

func New(client transport.Client) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("aws health adapter: transport client is required")
	}
	return &Adapter{client: client, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (a *Adapter) Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error) {
	response, err := a.client.Get(ctx, CurrentEventsURL, transport.Conditional{})
	endpoint := CurrentEventsURL
	if err != nil || response.StatusCode != http.StatusOK {
		response, err = a.client.Get(ctx, RSSURL, transport.Conditional{})
		endpoint = RSSURL
	}
	if err != nil {
		return domain.Capabilities{}, err
	}
	if response.StatusCode != http.StatusOK {
		return domain.Capabilities{}, fmt.Errorf("aws health probe: HTTP %d", response.StatusCode)
	}
	if endpoint == CurrentEventsURL {
		_, err = DecodeCurrentEvents(response.Body, source(target), response.ObservedAt)
	} else {
		_, err = DecodeRSS(response.Body, source(target), response.ObservedAt)
	}
	if err != nil {
		return domain.Capabilities{}, err
	}
	schemaID := "aws-currentevents/v1"
	if endpoint == RSSURL {
		schemaID = "aws-rss/v1"
	}
	return domain.Capabilities{Engine: Engine, Version: Version, Confidence: .8,
		Endpoints: map[domain.ResourceKind]domain.EndpointCapability{
			domain.ResourceUnresolvedIncidents: {Resource: domain.ResourceUnresolvedIncidents, Path: CurrentEventsURL,
				Completeness: domain.CompletenessComplete, AuthoritativeFor: []domain.ResourceKind{domain.ResourceUnresolvedIncidents},
				Cache:      domain.CacheCapability{SupportsETag: response.ETag != "", SupportsLastModified: response.LastModified != "", DefaultTTL: 15 * time.Second},
				Pagination: domain.PaginationCapability{Kind: domain.PaginationNone}, Authentication: "none",
				MaximumBodyBytes: 8 << 20, ExpectedMediaType: "application/json; charset=utf-16be"},
		}, SupportsETag: response.ETag != "", SupportsModified: response.LastModified != "",
		SnapshotCompleteness: domain.CompletenessComplete, SchemaHash: schemaID, ExpiresAt: a.now().Add(6 * time.Hour)}, nil
}

func (a *Adapter) Fetch(ctx context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	if request.ResourceKind != domain.ResourceUnresolvedIncidents {
		return domain.Snapshot{}, domain.FetchMeta{}, fmt.Errorf("aws health adapter: unsupported resource %q", request.ResourceKind)
	}
	response, primaryErr := a.client.Get(ctx, CurrentEventsURL, conditional(request))
	if primaryErr == nil && response.StatusCode == http.StatusNotModified {
		return unchanged(request, response), metadata(CurrentEventsURL, response), nil
	}
	if primaryErr == nil && response.StatusCode == http.StatusOK {
		snapshot, decodeErr := DecodeCurrentEvents(response.Body, request.Source, response.ObservedAt)
		if decodeErr == nil {
			meta := metadata(CurrentEventsURL, response)
			meta.SchemaHash = "aws-currentevents/v1"
			return snapshot, meta, nil
		}
		primaryErr = decodeErr
	}
	fallback, fallbackErr := a.client.Get(ctx, RSSURL, transport.Conditional{})
	if fallbackErr != nil {
		return domain.Snapshot{}, domain.FetchMeta{}, errors.Join(primaryErr, fallbackErr)
	}
	if fallback.StatusCode != http.StatusOK {
		return domain.Snapshot{}, metadata(RSSURL, fallback), errors.Join(primaryErr, fmt.Errorf("AWS RSS fallback: HTTP %d", fallback.StatusCode))
	}
	snapshot, err := DecodeRSS(fallback.Body, request.Source, fallback.ObservedAt)
	meta := metadata(RSSURL, fallback)
	meta.SchemaHash = "aws-rss/v1"
	return snapshot, meta, err
}

func (*Adapter) DecodeWebhook(context.Context, domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return nil, errors.New("aws health adapter: public feed has no webhook")
}

type currentEvent struct {
	Date        string `json:"date"`
	ARN         string `json:"arn"`
	RegionName  string `json:"region_name"`
	Status      string `json:"status"`
	Service     string `json:"service"`
	ServiceName string `json:"service_name"`
	Summary     string `json:"summary"`
	EventLog    []struct {
		Summary   string `json:"summary"`
		Message   string `json:"message"`
		Status    int    `json:"status"`
		Timestamp int64  `json:"timestamp"`
	} `json:"event_log"`
}

func DecodeCurrentEvents(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
	decoded, err := decodeUTF16BE(body)
	if err != nil {
		return domain.Snapshot{}, err
	}
	var events []currentEvent
	if err := json.Unmarshal(decoded, &events); err != nil {
		return domain.Snapshot{}, fmt.Errorf("aws health adapter: decode currentevents: %w", err)
	}
	incidents := make([]domain.Incident, 0, len(events))
	for _, item := range events {
		if item.ARN == "" || item.Summary == "" {
			return domain.Snapshot{}, errors.New("aws health adapter: currentevents item is missing arn or summary")
		}
		started := unixPointer(item.Date)
		updates := make([]domain.IncidentUpdate, 0, len(item.EventLog))
		for index, log := range item.EventLog {
			created := time.Unix(log.Timestamp, 0).UTC()
			updates = append(updates, domain.IncidentUpdate{ID: fmt.Sprintf("%s:%d:%d", item.ARN, log.Timestamp, index),
				IncidentID: item.ARN, Body: log.Message, Phase: phase(log.Status), RawPhase: strconv.Itoa(log.Status),
				Impact: impact(item.Status), RawImpact: item.Status, SourceCreatedAt: &created, SourceUpdatedAt: &created})
		}
		componentID := strings.TrimSpace(item.Service)
		incidents = append(incidents, domain.Incident{ID: item.ARN, UpstreamID: item.ARN, Kind: domain.IncidentKindIncident,
			Name: item.Summary, URL: "https://health.aws.amazon.com/health/status", Phase: currentPhase(item.EventLog),
			RawPhase: item.Status, Impact: impact(item.Status), RawImpact: item.Status,
			ComponentIDs: []string{componentID}, Updates: updates, StartedAt: started, SourceCreatedAt: started,
			SourceUpdatedAt: latestUpdate(updates)})
	}
	return snapshot(source, incidents, observedAt), nil
}

type rssDocument struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			GUID        string `xml:"guid"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

func DecodeRSS(body []byte, source domain.Source, observedAt time.Time) (domain.Snapshot, error) {
	var feed rssDocument
	if err := xml.Unmarshal(body, &feed); err != nil {
		return domain.Snapshot{}, fmt.Errorf("aws health adapter: decode RSS: %w", err)
	}
	incidents := make([]domain.Incident, 0, len(feed.Channel.Items))
	for _, item := range feed.Channel.Items {
		if strings.TrimSpace(item.GUID) == "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		published, _ := time.Parse(time.RFC1123Z, item.PubDate)
		if published.IsZero() {
			published, _ = time.Parse("Mon, 02 Jan 2006 15:04:05 MST", item.PubDate)
		}
		published = published.UTC()
		incidents = append(incidents, domain.Incident{ID: item.GUID, UpstreamID: item.GUID,
			Kind: domain.IncidentKindIncident, Name: item.Title, URL: item.Link,
			Phase: domain.IncidentPhaseInvestigating, RawPhase: "rss-current",
			Impact: domain.ImpactMajor, RawImpact: "rss",
			Updates: []domain.IncidentUpdate{{ID: item.GUID + ":rss", IncidentID: item.GUID, Body: item.Description,
				Phase: domain.IncidentPhaseInvestigating, Impact: domain.ImpactMajor, SourceCreatedAt: &published}},
			StartedAt: &published, SourceCreatedAt: &published, SourceUpdatedAt: &published})
	}
	return snapshot(source, incidents, observedAt), nil
}

func decodeUTF16BE(body []byte) ([]byte, error) {
	if len(body) >= 2 && body[0] == 0xfe && body[1] == 0xff {
		body = body[2:]
	}
	if len(body)%2 != 0 {
		return nil, errors.New("aws health adapter: odd UTF-16BE byte length")
	}
	words := make([]uint16, len(body)/2)
	for index := range words {
		words[index] = uint16(body[index*2])<<8 | uint16(body[index*2+1])
	}
	return []byte(string(utf16.Decode(words))), nil
}

func snapshot(source domain.Source, incidents []domain.Incident, observedAt time.Time) domain.Snapshot {
	if source.Provider == "" {
		source.Provider = Engine
	}
	if source.Kind == "" {
		source.Kind = domain.SourceKindFeed
	}
	return domain.Snapshot{Source: source, ResourceKind: domain.ResourceUnresolvedIncidents,
		OverallStatus: overall(incidents), ComputedStatus: overall(incidents), Incidents: incidents,
		Completeness: domain.CompletenessComplete, AuthoritativeFor: []domain.ResourceKind{domain.ResourceUnresolvedIncidents},
		ObservedAt: observedAt.UTC(), SchemaVersion: Version, AdapterVersion: AdapterVersion, NormalizerVersion: NormalizerVersion}
}

func source(target domain.Target) domain.Source {
	u, _ := url.Parse("https://health.aws.amazon.com/health/status")
	return domain.Source{ID: target.SourceID, Provider: Engine, Kind: domain.SourceKindFeed, RequestedURL: target.URL, CanonicalURL: u}
}

func conditional(request domain.FetchRequest) transport.Conditional {
	value := transport.Conditional{ETag: request.ETag}
	if request.LastModified != nil {
		value.LastModified = request.LastModified.UTC().Format(http.TimeFormat)
	}
	return value
}

func metadata(endpoint string, response *transport.Response) domain.FetchMeta {
	result := domain.FetchMeta{Endpoint: endpoint, StatusCode: response.StatusCode, ETag: response.ETag,
		ObservedAt: response.ObservedAt, NotModified: response.NotModified, ContentType: response.Header.Get("Content-Type"), BodyBytes: int64(len(response.Body))}
	if response.LastModified != "" {
		if parsed, err := http.ParseTime(response.LastModified); err == nil {
			result.LastModified = &parsed
		}
	}
	return result
}

func unchanged(request domain.FetchRequest, response *transport.Response) domain.Snapshot {
	return domain.Snapshot{Source: request.Source, ResourceKind: request.ResourceKind, Completeness: domain.CompletenessUnknown,
		ObservedAt: response.ObservedAt, SchemaVersion: Version, AdapterVersion: AdapterVersion, NormalizerVersion: NormalizerVersion}
}

func unixPointer(value string) *time.Time {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	parsed := time.Unix(seconds, 0).UTC()
	return &parsed
}
func phase(status int) domain.IncidentPhase {
	switch status {
	case 1:
		return domain.IncidentPhaseInvestigating
	case 2:
		return domain.IncidentPhaseIdentified
	default:
		return domain.IncidentPhaseMonitoring
	}
}
func impact(status string) domain.Impact {
	switch status {
	case "3":
		return domain.ImpactCritical
	case "2":
		return domain.ImpactMajor
	default:
		return domain.ImpactMinor
	}
}
func currentPhase(updates []struct {
	Summary   string `json:"summary"`
	Message   string `json:"message"`
	Status    int    `json:"status"`
	Timestamp int64  `json:"timestamp"`
}) domain.IncidentPhase {
	if len(updates) == 0 {
		return domain.IncidentPhaseInvestigating
	}
	return phase(updates[len(updates)-1].Status)
}
func latestUpdate(updates []domain.IncidentUpdate) *time.Time {
	if len(updates) == 0 {
		return nil
	}
	return updates[len(updates)-1].SourceUpdatedAt
}
func overall(incidents []domain.Incident) domain.ComponentStatus {
	result := domain.ComponentStatusOperational
	for _, incident := range incidents {
		if incident.Impact == domain.ImpactCritical {
			return domain.ComponentStatusMajorOutage
		}
		if incident.Impact == domain.ImpactMajor {
			result = domain.ComponentStatusPartialOutage
		} else if incident.Impact == domain.ImpactMinor && result == domain.ComponentStatusOperational {
			result = domain.ComponentStatusDegraded
		}
	}
	return result
}
