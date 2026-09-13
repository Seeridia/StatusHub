package subscription

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

func TestEvaluateQuietHoursAndCriticalBypass(t *testing.T) {
	now := time.Date(2026, 9, 10, 16, 30, 0, 0, time.UTC) // 00:30 Asia/Shanghai
	rule := json.RawMessage(`{"minimum_impact":"major","quiet_hours":{"timezone":"Asia/Shanghai","from":"23:00","to":"08:00"},"delivery_policy":{"critical_bypass_quiet_hours":true}}`)
	major, err := Evaluate(rule, Event{Kind: domain.EventKindIncidentUpdated, Payload: json.RawMessage(`{"current":{"name":"API errors","impact":"major"}}`)}, now)
	if err != nil || !major.Matched || !major.EligibleAt.Equal(time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("major decision = %#v, err=%v", major, err)
	}
	critical, err := Evaluate(rule, Event{Kind: domain.EventKindIncidentCreated, Payload: json.RawMessage(`{"current":{"impact":"critical"}}`)}, now)
	if err != nil || !critical.Matched || !critical.EligibleAt.Equal(now) || critical.Priority != 100 {
		t.Fatalf("critical decision = %#v, err=%v", critical, err)
	}
}

func TestEvaluateMinimumImpactAndKeywords(t *testing.T) {
	rule := json.RawMessage(`{"minimum_impact":"major","include_keywords":["api"],"exclude_keywords":["sandbox"]}`)
	decision, err := Evaluate(rule, Event{Kind: domain.EventKindIncidentUpdated, Payload: json.RawMessage(`{"current":{"name":"API latency","impact":"minor"}}`)}, time.Now())
	if err != nil || decision.Matched {
		t.Fatalf("minor decision = %#v, err=%v", decision, err)
	}
	decision, err = Evaluate(rule, Event{Kind: domain.EventKindIncidentUpdated, Payload: json.RawMessage(`{"current":{"name":"API latency","impact":"major"}}`)}, time.Now())
	if err != nil || !decision.Matched {
		t.Fatalf("major decision = %#v, err=%v", decision, err)
	}
}
