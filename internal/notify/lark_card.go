package notify

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Custom bot cards use plain text for upstream content and explicit URL buttons.
func (d *Lark) renderCard(event CanonicalEvent, endpoint Endpoint) (Payload, error) {
	details := parseEventDetails(event.Data)
	title := strings.TrimSpace(event.Subject)
	if title == "" {
		title = strings.TrimSpace(details.Current.Name)
	}
	if title == "" {
		title = string(event.Kind)
	}
	title, degraded := truncateRunes("StatusHub · "+title, 180)

	plain := func(text string) map[string]string { return map[string]string{"tag": "plain_text", "content": text} }
	elements := []any{}
	fields := []any{}
	field := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		text, cut := truncateRunes(label+"\n"+value, 400)
		degraded = degraded || cut
		fields = append(fields, map[string]any{"is_short": true, "text": plain(text)})
	}
	field("通知类型", larkLabel(string(event.Kind)))
	field("服务状态", statusChange(larkLabel(details.Previous.Status), larkLabel(details.Current.Status)))
	field("事件阶段", statusChange(larkLabel(details.Previous.Phase), larkLabel(details.Current.Phase)))
	field("影响程度", larkLabel(details.Current.Impact))
	elements = append(elements, map[string]any{"tag": "div", "fields": fields})
	body := latestBody(details.Current.Updates)
	if body == "" {
		body = details.Current.Description
	}
	if body == "" && details.Current.Status == "" && details.Current.Phase == "" {
		body = event.Summary
	}
	if strings.TrimSpace(body) != "" {
		body, cut := truncateRunes(body, 2400)
		degraded = degraded || cut
		elements = append(elements, map[string]any{"tag": "hr"}, map[string]any{"tag": "div", "text": plain("最新进展\n" + body)})
	}
	if !event.Time.IsZero() {
		elements = append(elements, map[string]any{"tag": "note", "elements": []any{plain("采集时间 · " + event.Time.UTC().Format("2006-01-02 15:04:05") + " UTC")}})
	}
	buttons := []any{}
	button := func(label, link, kind string) {
		u, err := url.Parse(strings.TrimSpace(link))
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(link) > 2048 {
			return
		}
		buttons = append(buttons, map[string]any{"tag": "button", "text": plain(label), "type": kind, "url": u.String()})
	}
	label := "在 StatusHub 查看事件"
	if !event.ConsoleIsIncident {
		label = "在 StatusHub 查看服务事件"
	}
	button(label, event.ConsoleURL, "primary")
	button("查看官方状态页", details.Current.URL, "default")
	if len(buttons) > 0 {
		elements = append(elements, map[string]any{"tag": "action", "actions": buttons})
	}
	color := "blue"
	switch {
	case details.Current.Phase == "resolved" || details.Current.Phase == "completed" || details.Current.Status == "operational":
		color = "green"
	case details.Current.Status == "major_outage" || details.Current.Impact == "critical":
		color = "red"
	case details.Current.Phase == "monitoring":
		color = "turquoise"
	case details.Current.Phase == "investigating" || details.Current.Phase == "identified" || details.Current.Status == "degraded_performance" || details.Current.Status == "partial_outage":
		color = "orange"
	}
	timestamp := strconv.FormatInt(d.now().Unix(), 10)
	envelope := map[string]any{"timestamp": timestamp, "sign": larkSignature(timestamp, endpoint.Secret), "msg_type": "interactive", "card": map[string]any{"config": map[string]bool{"wide_screen_mode": true}, "header": map[string]any{"template": color, "title": plain(title)}, "elements": elements}}
	maximum := endpoint.MaxPayloadBytes
	if maximum <= 0 || maximum > 20*1024 {
		maximum = 20 * 1024
	}
	payload, err := marshalBounded(ChannelLark, event.ID, maximum, envelope)
	// A bounded text representation also covers providers rejecting a large card with HTTP 413.
	text := chatText(event)
	var fallback []byte
	for limit := 2000; limit > 0; limit /= 2 {
		short, _ := truncateRunes(text, limit)
		fallback, _ = json.Marshal(map[string]any{"timestamp": timestamp, "sign": larkSignature(timestamp, endpoint.Secret), "msg_type": "text", "content": map[string]string{"text": "StatusHub\n" + short + "\n" + event.ConsoleURL}})
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

func larkLabel(value string) string {
	labels := map[string]string{
		"incident.created": "新事件", "incident.updated": "事件更新", "incident.resolved": "事件已恢复", "component.status_changed": "服务状态变化",
		"maintenance.scheduled": "维护预告", "maintenance.started": "维护开始", "maintenance.completed": "维护完成", "source.degraded": "采集异常", "source.recovered": "采集恢复",
		"operational": "正常运行", "degraded_performance": "性能下降", "partial_outage": "部分中断", "major_outage": "严重中断", "under_maintenance": "维护中",
		"investigating": "调查中", "identified": "已定位", "monitoring": "恢复观察中", "resolved": "已解决", "scheduled": "计划维护", "in_progress": "维护中", "verifying": "验证中", "completed": "已完成",
		"none": "无影响", "minor": "轻微", "major": "严重", "critical": "重大", "unknown": "未知",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}
