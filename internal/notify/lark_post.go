package notify

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Custom bots accept rich-text posts without image uploads or application credentials.
func (d *Lark) renderPost(event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	details := parseEventDetails(event.Data)
	title := strings.TrimSpace(event.Subject)
	if title == "" {
		title = strings.TrimSpace(details.Current.Name)
	}
	if title == "" {
		title = string(event.Kind)
	}
	title, degraded := truncateRunes("StatusHub · "+title, 180)
	rows := make([][]map[string]string, 0, 8)
	add := func(label, value string) {
		if value = strings.TrimSpace(value); value == "" {
			return
		}
		text, cut := truncateRunes(label+value, 2000)
		degraded = degraded || cut
		rows = append(rows, []map[string]string{{"tag": "text", "text": text}})
	}
	add("Event: ", string(event.Kind))
	add("Status: ", statusChange(details.Previous.Status, details.Current.Status))
	add("Phase: ", statusChange(details.Previous.Phase, details.Current.Phase))
	add("Impact: ", details.Current.Impact)
	add("", details.Current.Description)
	body := latestBody(details.Current.Updates)
	add("", body)
	if details.Current.Status == "" && details.Current.Phase == "" && details.Current.Description == "" && body == "" {
		add("", event.Summary)
	}
	if !event.Time.IsZero() {
		add("Time (UTC): ", event.Time.UTC().Format(time.RFC3339))
	}
	if link, err := url.Parse(strings.TrimSpace(details.Current.URL)); err == nil && (link.Scheme == "https" || link.Scheme == "http") && link.Host != "" && link.User == nil && len(link.String()) <= 2048 {
		rows = append(rows, []map[string]string{{"tag": "a", "text": "View original update", "href": link.String()}})
	}
	timestamp := strconv.FormatInt(d.now().Unix(), 10)
	envelope := map[string]any{"timestamp": timestamp, "sign": larkSignature(timestamp, endpoint.Secret), "msg_type": "post", "content": map[string]any{"post": map[string]any{"en_us": map[string]any{"title": title, "content": rows}}}}
	maximum := endpoint.MaxPayloadBytes
	if maximum <= 0 || maximum > 20*1024 {
		maximum = 20 * 1024
	}
	payload, err := marshalBounded(ChannelLark, event.ID, maximum, envelope)
	// A bounded text representation also covers providers rejecting a large post with HTTP 413.
	text := chatText(event)
	var fallback []byte
	for limit := 2000; limit > 0; limit /= 2 {
		short, _ := truncateRunes(text, limit)
		fallback, _ = json.Marshal(map[string]any{"timestamp": timestamp, "sign": larkSignature(timestamp, endpoint.Secret), "msg_type": "text", "content": map[string]string{"text": "StatusHub\n" + short}})
		if len(fallback) <= maximum {
			break
		}
	}
	if len(fallback) > maximum {
		payload.Degraded = degraded
		return payload, err
	}
	if err != nil {
		return Payload{Channel: ChannelLark, EventID: event.ID, ContentType: "application/json", Body: fallback, Degraded: true}, nil
	}
	payload.FallbackBody, payload.Degraded = fallback, degraded
	return payload, nil
}
