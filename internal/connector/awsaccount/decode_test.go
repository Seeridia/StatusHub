package awsaccount

import (
	"errors"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

const testEvent = `{
  "version":"0",
  "id":"7bf73129-1428-4cd3-a780-95db273d1602",
  "detail-type":"AWS Health Event",
  "source":"aws.health",
  "account":"123456789012",
  "time":"2026-09-10T01:02:03Z",
  "region":"us-east-1",
  "resources":["arn:aws:ec2:us-east-1:123456789012:instance/i-123"],
  "detail":{
    "eventArn":"arn:aws:health:us-east-1::event/EC2/AWS_EC2_OPERATIONAL_ISSUE/test",
    "service":"EC2",
    "eventTypeCode":"AWS_EC2_OPERATIONAL_ISSUE",
    "eventTypeCategory":"issue",
    "eventScopeCode":"ACCOUNT_SPECIFIC",
    "statusCode":"open",
    "eventRegion":"us-east-1",
    "startTime":"2026-09-10T01:00:00Z",
    "lastUpdatedTime":"2026-09-10T01:02:00Z",
    "eventDescription":[{"language":"en_US","latestDescription":"Instance connectivity is degraded"}],
    "affectedEntities":[{"entityValue":"i-123","statusCode":"IMPAIRED","lastUpdatedTime":"2026-09-10T01:02:00Z"}]
  }
}`

func TestDecodeAWSHealthEventBridge(t *testing.T) {
	observed := time.Date(2026, 9, 10, 1, 2, 4, 0, time.UTC)
	event, subject, err := Decode([]byte(testEvent), Config{SourceID: "source-1", ExternalAccountID: "123456789012",
		AllowedRegions: []string{"us-east-1"}, AllowedServices: []string{"ec2"}}, observed)
	if err != nil {
		t.Fatal(err)
	}
	if event.Provider != Provider || event.Kind != domain.EventKindIncidentCreated || event.EntityKind != domain.EntityIncident ||
		event.EntityID == "" || event.SourceEventKey == "" || event.SourceUpdatedAt == nil || subject != "statusmon.events.normal" {
		t.Fatalf("event=%#v subject=%q", event, subject)
	}
	if string(event.Payload) == string([]byte(testEvent)) {
		t.Fatal("canonical payload must not retain the transport envelope")
	}
}

func TestDecodeAWSHealthEventBridgeFiltersAndMapsMaintenance(t *testing.T) {
	if _, _, err := Decode([]byte(testEvent), Config{SourceID: "source", ExternalAccountID: "123456789012",
		AllowedServices: []string{"RDS"}}, time.Now()); !errors.Is(err, ErrFiltered) {
		t.Fatalf("filter error=%v", err)
	}
	maintenance := []byte(`{"version":"0","id":"event-2","detail-type":"AWS Health Event","source":"aws.health","account":"123456789012","time":"2026-09-10T01:02:03Z","region":"us-east-1","resources":[],"detail":{"eventArn":"arn:aws:health:global::event/MAINTENANCE/test","service":"RDS","eventTypeCode":"MAINTENANCE","eventTypeCategory":"scheduledChange","statusCode":"closed","lastUpdatedTime":"2026-09-10T01:02:00Z"}}`)
	event, _, err := Decode(maintenance, Config{SourceID: "source", ExternalAccountID: "123456789012"}, time.Now())
	if err != nil || event.Kind != domain.EventKindMaintenanceCompleted || event.EntityKind != domain.EntityMaintenance {
		t.Fatalf("maintenance=%#v err=%v", event, err)
	}
}
