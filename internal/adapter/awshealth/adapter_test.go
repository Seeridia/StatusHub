package awshealth

import (
	"encoding/binary"
	"net/url"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

func TestDecodeCurrentEventsUTF16BE(t *testing.T) {
	raw := `[{"date":"1700000000","arn":"arn:aws:health:us-east-1::event/EC2/test","region_name":"N. Virginia","status":"3","service":"ec2-us-east-1","service_name":"EC2","summary":"Elevated errors","event_log":[{"summary":"Elevated errors","message":"Investigating","status":1,"timestamp":1700000000}]}]`
	words := utf16.Encode([]rune(raw))
	body := []byte{0xfe, 0xff}
	for _, word := range words {
		var encoded [2]byte
		binary.BigEndian.PutUint16(encoded[:], word)
		body = append(body, encoded[:]...)
	}
	base, _ := url.Parse("https://health.aws.amazon.com/health/status")
	snapshot, err := DecodeCurrentEvents(body, domain.Source{ID: "source", CanonicalURL: base}, time.Unix(1700000010, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Incidents) != 1 || snapshot.Incidents[0].Impact != domain.ImpactCritical ||
		snapshot.Incidents[0].Phase != domain.IncidentPhaseInvestigating {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestDecodeRSSFallback(t *testing.T) {
	body := []byte(`<rss><channel><item><title>Service disruption</title><link>https://status.aws.amazon.com/</link><pubDate>Wed, 09 Sep 2026 13:09:19 PDT</pubDate><guid>event-1</guid><description>Investigating</description></item></channel></rss>`)
	snapshot, err := DecodeRSS(body, domain.Source{ID: "source"}, time.Now())
	if err != nil || len(snapshot.Incidents) != 1 || snapshot.Incidents[0].ID != "event-1" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}
