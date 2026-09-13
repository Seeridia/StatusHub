package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter/shadow"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/audit"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/auth"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

func TestIntegrationDeliveryLanesAreDisjoint(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))
	event := integrationEvent("m4-lanes-event", "m4-lanes-incident")
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, OutboxMessage{
		ID: integrationUUID(1200), Subject: "statusmon.events.critical", Payload: []byte(`{}`),
	}); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	tenantID := integrationUUID(1201)
	if _, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'m4-lanes','M4 Lanes')`, tenantID); err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		name       string
		deliveryID string
		priority   int
		status     string
	}
	fixtures := []fixture{
		{name: "critical", deliveryID: integrationUUID(1210), priority: 100, status: "queued"},
		{name: "bulk", deliveryID: integrationUUID(1220), priority: 20, status: "queued"},
		{name: "retry", deliveryID: integrationUUID(1230), priority: 100, status: "retry_wait"},
	}
	for index, item := range fixtures {
		subscriptionID, endpointID := integrationUUID(1240+index), integrationUUID(1250+index)
		if _, err := database.pool.Exec(ctx, `INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,$3,'{}')`, subscriptionID, tenantID, item.name); err != nil {
			t.Fatalf("insert %s subscription: %v", item.name, err)
		}
		if _, err := database.pool.Exec(ctx, `
INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id)
VALUES($1,$2,'generic_webhook',$3,$4,'key')`, endpointID, tenantID, item.name,
			[]byte(`{"url":"https://hooks.example.test/status"}`)); err != nil {
			t.Fatalf("insert %s endpoint: %v", item.name, err)
		}
		if _, err := database.pool.Exec(ctx, `
INSERT INTO deliveries(id,event_id,subscription_id,endpoint_id,template_version,status,priority,next_attempt_at)
VALUES($1,$2,$3,$4,1,$5,$6,statement_timestamp())`, item.deliveryID, eventID, subscriptionID, endpointID, item.status, item.priority); err != nil {
			t.Fatalf("insert %s lane fixture: %v", item.name, err)
		}
	}

	for _, expected := range []struct {
		lane DeliveryLane
		id   string
	}{{DeliveryLaneCritical, fixtures[0].deliveryID}, {DeliveryLaneRetry, fixtures[2].deliveryID}, {DeliveryLaneBulk, fixtures[1].deliveryID}} {
		leases, err := database.store.ClaimDeliveryLane(ctx, "m4-"+string(expected.lane), expected.lane, 10, 10, time.Minute)
		if err != nil || len(leases) != 1 || leases[0].ID != expected.id || leases[0].Lane != expected.lane {
			t.Fatalf("lane=%s leases=%#v err=%v", expected.lane, leases, err)
		}
	}
	if _, err := database.store.ClaimDeliveryLane(ctx, "invalid", DeliveryLane("unknown"), 1, 1, time.Minute); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid lane error=%v", err)
	}
}

func TestIntegrationAWSConnectorCommitIsIdempotentAndOrdered(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	tenantID := integrationUUID(1300)
	if _, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'aws-account','AWS Account')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO vendors(id,slug,name,canonical_domain) VALUES($1,'aws','AWS','health.aws.amazon.com')`, integrationUUID(1301)); err != nil {
		t.Fatal(err)
	}
	connector, err := database.store.CreateAWSAccountConnector(ctx, CreateAWSAccountConnectorParams{
		ID: integrationUUID(1302), SourceID: integrationUUID(1303), TenantID: tenantID,
		ExternalAccountID: "123456789012", SNSTopicARN: "arn:aws:sns:us-east-1:123456789012:statusmon-health",
		AllowedRegions: []string{"us-east-1"}, AllowedServices: []string{"ec2"},
	})
	if err != nil || !connector.Enabled || len(connector.AllowedServices) != 1 {
		t.Fatalf("connector=%#v err=%v", connector, err)
	}
	entityID := "arn:aws:health:us-east-1::event/EC2/test"
	updatedAt := time.Date(2026, 9, 10, 2, 0, 0, 0, time.UTC)
	makeEvent := func(key, name string, at time.Time) domain.CanonicalEvent {
		return domain.CanonicalEvent{SourceID: connector.SourceID, Provider: "aws-account-health",
			Kind: domain.EventKindIncidentCreated, EntityKind: domain.EntityIncident, EntityID: entityID,
			SourceEventKey: domain.SourceEventKey(key), NormalizerVersion: "aws-account-health/1",
			SchemaVersion: "aws-health-eventbridge/v1", Payload: []byte(`{"current":{"name":"` + name + `"}}`),
			SourceUpdatedAt: &at, ObservedAt: at.Add(time.Second)}
	}
	first := CommitConnectorEventParams{Event: makeEvent("upstream-1", "first", updatedAt), Subject: "statusmon.events.normal", SemanticHash: "hash-1"}
	result, err := database.store.CommitConnectorEvent(ctx, first)
	if err != nil || !result.Inserted || result.Revision != 1 {
		t.Fatalf("first commit=%#v err=%v", result, err)
	}
	duplicate, err := database.store.CommitConnectorEvent(ctx, first)
	if err != nil || duplicate.Inserted || duplicate.Revision != 1 {
		t.Fatalf("duplicate commit=%#v err=%v", duplicate, err)
	}
	secondAt := updatedAt.Add(time.Minute)
	second := CommitConnectorEventParams{Event: makeEvent("upstream-2", "second", secondAt), Subject: "statusmon.events.normal", SemanticHash: "hash-2"}
	result, err = database.store.CommitConnectorEvent(ctx, second)
	if err != nil || !result.Inserted || result.Revision != 2 {
		t.Fatalf("second commit=%#v err=%v", result, err)
	}
	staleAt := updatedAt.Add(-time.Minute)
	stale := CommitConnectorEventParams{Event: makeEvent("upstream-3", "stale", staleAt), Subject: "statusmon.events.normal", SemanticHash: "hash-stale"}
	result, err = database.store.CommitConnectorEvent(ctx, stale)
	if err != nil || !result.Inserted || result.Revision != 3 {
		t.Fatalf("stale commit=%#v err=%v", result, err)
	}
	var revision int
	var latestName string
	if err := database.pool.QueryRow(ctx, `
SELECT revision,latest_payload->'current'->>'name'
FROM connector_entity_states WHERE source_id=$1 AND entity_id=$2`, connector.SourceID, entityID).Scan(&revision, &latestName); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || latestName != "second" {
		t.Fatalf("state revision=%d latest=%q", revision, latestName)
	}
	var updatedKinds int
	if err := database.pool.QueryRow(ctx, `
SELECT count(*) FROM canonical_events
WHERE source_id=$1 AND event_kind='incident.updated'`, connector.SourceID).Scan(&updatedKinds); err != nil {
		t.Fatal(err)
	}
	if updatedKinds != 2 {
		t.Fatalf("updated event count=%d want=2", updatedKinds)
	}
}

func TestIntegrationPrivateAgentOwnsItsDeliveryLease(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))
	event := integrationEvent("m4-private-event", "m4-private-incident")
	if inserted, err := database.store.InsertEventWithOutbox(ctx, event, OutboxMessage{
		ID: integrationUUID(1400), Subject: "statusmon.events.critical", Payload: []byte(`{}`),
	}); err != nil || !inserted {
		t.Fatalf("insert event: inserted=%v err=%v", inserted, err)
	}
	var eventID string
	if err := database.pool.QueryRow(ctx, `SELECT id FROM canonical_events WHERE source_event_key=$1`, string(event.SourceEventKey)).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	tenantID, subscriptionID, endpointID := integrationUUID(1401), integrationUUID(1402), integrationUUID(1403)
	if _, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'private-agent','Private Agent')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `INSERT INTO subscriptions(id,tenant_id,name,rule) VALUES($1,$2,'Private','{}')`, subscriptionID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `
INSERT INTO endpoints(id,tenant_id,channel,name,encrypted_config,key_id)
VALUES($1,$2,'private_agent','Internal webhook',$3,'private-key')`, endpointID, tenantID,
		[]byte(`{"url":"http://internal.service/status","secret":"local-secret"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `
INSERT INTO deliveries(id,event_id,subscription_id,endpoint_id,template_version,priority,next_attempt_at)
VALUES($1,$2,$3,$4,1,100,statement_timestamp())`, integrationUUID(1404), eventID, subscriptionID, endpointID); err != nil {
		t.Fatal(err)
	}
	credential, err := database.store.CreatePrivateAgent(ctx, tenantID, "primary")
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.store.CreatePrivateAgent(ctx, tenantID, "other")
	if err != nil {
		t.Fatal(err)
	}
	if bound, err := database.store.BindPrivateAgentEndpoint(ctx, credential.Agent.ID, endpointID); err != nil || !bound {
		t.Fatalf("bind=%v err=%v", bound, err)
	}
	if _, err := database.store.AuthenticatePrivateAgent(ctx, credential.Agent.ID, "wrong-token-value-that-is-long-enough", "test"); err == nil {
		t.Fatal("wrong private agent token authenticated")
	}
	if _, err := database.store.AuthenticatePrivateAgent(ctx, credential.Agent.ID, credential.Token, "test"); err != nil {
		t.Fatal(err)
	}
	public, err := database.store.ClaimDeliveryLane(ctx, "public-worker", DeliveryLaneCritical, 10, 10, time.Minute)
	if err != nil || len(public) != 0 {
		t.Fatalf("public worker claimed private deliveries: %#v err=%v", public, err)
	}
	leases, err := database.store.ClaimPrivateAgentDeliveries(ctx, credential.Agent.ID, "agent-worker", 10, time.Minute)
	if err != nil || len(leases) != 1 || leases[0].Lane != DeliveryLaneCritical {
		t.Fatalf("private leases=%#v err=%v", leases, err)
	}
	completion := CompleteDeliveryParams{DeliveryID: leases[0].ID, LeaseToken: leases[0].LeaseToken,
		AttemptNumber: leases[0].AttemptNumber, Status: "accepted", FinishedAt: time.Now(), AgentID: other.Agent.ID}
	if err := database.store.CompleteDeliveryAttempt(ctx, completion); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong agent completion error=%v", err)
	}
	completion.AgentID = credential.Agent.ID
	if err := database.store.CompleteDeliveryAttempt(ctx, completion); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationIdentityServiceAccountAndImmutableAuditChain(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	tenantID := integrationUUID(1500)
	if _, err := database.pool.Exec(ctx, `INSERT INTO tenants(id,slug,name) VALUES($1,'m4-identity','M4 Identity')`, tenantID); err != nil {
		t.Fatal(err)
	}
	provider, err := database.store.UpsertOIDCProvider(ctx, UpsertOIDCProviderParams{
		ID: integrationUUID(1501), TenantID: tenantID, Issuer: "https://identity.example.test",
		ClientID: "statusmon", JWKSURI: "https://identity.example.test/jwks.json",
		AllowedDomains: []string{"Example.Test", "example.test"}, Enabled: true,
		Actor: AuditActor{Type: "user", ID: "bootstrap-owner"},
	})
	if err != nil || provider.ID != integrationUUID(1501) || len(provider.AllowedDomains) != 1 {
		t.Fatalf("provider=%#v err=%v", provider, err)
	}
	readProvider, err := database.store.OIDCProvider(ctx, tenantID, provider.Issuer)
	if err != nil || readProvider.JWKSURI != provider.JWKSURI {
		t.Fatalf("read provider=%#v err=%v", readProvider, err)
	}
	member, err := database.store.SetTenantMembership(ctx, SetTenantMembershipParams{
		TenantID: tenantID, PrincipalID: integrationUUID(1502), Issuer: provider.Issuer,
		Subject: "subject-1", Email: "admin@example.test", DisplayName: "Admin",
		Role: auth.RoleAdmin, Actor: AuditActor{Type: "user", ID: "bootstrap-owner"},
	})
	if err != nil || member.Role != auth.RoleAdmin {
		t.Fatalf("member=%#v err=%v", member, err)
	}
	resolved, err := database.store.ResolveOIDCIdentity(ctx, tenantID, auth.Claims{
		Issuer: provider.Issuer, Subject: member.Subject, Email: member.Email, DisplayName: member.DisplayName,
	})
	if err != nil || resolved.ActorID != member.ActorID || resolved.Role != auth.RoleAdmin {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
	credential, err := database.store.CreateServiceAccount(ctx, CreateServiceAccountParams{
		ID: integrationUUID(1503), TenantID: tenantID, Name: "deployment", Role: auth.RoleOperator,
		Actor: AuditActor{Type: "user", ID: member.ActorID},
	})
	if err != nil || credential.Token == "" {
		t.Fatalf("credential=%#v err=%v", credential, err)
	}
	if _, err := database.store.AuthenticateServiceAccount(ctx, tenantID, credential.Token+"wrong"); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("wrong token error=%v", err)
	}
	authenticated, err := database.store.AuthenticateServiceAccount(ctx, tenantID, credential.Token)
	if err != nil || authenticated.ActorID != credential.ServiceAccount.ID || authenticated.Role != auth.RoleOperator {
		t.Fatalf("authenticated=%#v err=%v", authenticated, err)
	}

	const concurrentEvents = 12
	start := make(chan struct{})
	errorsChannel := make(chan error, concurrentEvents)
	var group sync.WaitGroup
	for index := 0; index < concurrentEvents; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			metadata, _ := json.Marshal(map[string]any{"index": index})
			_, appendErr := database.store.AppendAuditEvent(ctx, tenantID, audit.AppendInput{
				OccurredAt: time.Date(2026, 9, 10, 10, 0, index, 0, time.UTC),
				ActorType:  "service_account", ActorID: credential.ServiceAccount.ID,
				Action: "test.concurrent", ResourceType: "test", ResourceID: integrationUUID(1520 + index),
				Outcome: "success", Metadata: metadata,
			})
			errorsChannel <- appendErr
		}(index)
	}
	close(start)
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := database.store.ExportAuditEvents(ctx, tenantID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3+concurrentEvents {
		t.Fatalf("audit events=%d want=%d", len(events), 3+concurrentEvents)
	}
	if err := audit.Verify(events); err != nil {
		t.Fatalf("verify audit chain: %v", err)
	}
	if _, err := database.pool.Exec(ctx, `UPDATE audit_events SET action='tampered' WHERE tenant_id=$1 AND sequence=1`, tenantID); err == nil {
		t.Fatal("audit UPDATE was not rejected")
	}
	if _, err := database.pool.Exec(ctx, `DELETE FROM audit_events WHERE tenant_id=$1 AND sequence=1`, tenantID); err == nil {
		t.Fatal("audit DELETE was not rejected")
	}
}

func TestIntegrationRegionalOwnershipEpochFencesRecoveredWriter(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(-time.Minute))
	for _, region := range []string{"cn-east", "cn-west"} {
		if _, err := database.store.HeartbeatRegion(ctx, region, json.RawMessage(`{"test":true}`)); err != nil {
			t.Fatal(err)
		}
	}
	if inserted, err := database.store.BootstrapSourceOwnership(ctx, "cn-east"); err != nil || inserted != 1 {
		t.Fatalf("bootstrap inserted=%d err=%v", inserted, err)
	}
	eastLeases, err := database.store.AcquireDueSourcesInRegion(ctx, "cn-east", "east-worker", 1, time.Minute)
	if err != nil || len(eastLeases) != 1 || eastLeases[0].OwnershipEpoch != 1 {
		t.Fatalf("east leases=%#v err=%v", eastLeases, err)
	}
	if westLeases, err := database.store.AcquireDueSourcesInRegion(ctx, "cn-west", "west-worker", 1, time.Minute); err != nil || len(westLeases) != 0 {
		t.Fatalf("standby region leases=%#v err=%v", westLeases, err)
	}
	ownership, err := database.store.ChangeSourceRegion(ctx, ChangeSourceRegionParams{
		SourceID: integrationSourceID, TargetRegion: "cn-west", ExpectedEpoch: 1,
		HeartbeatMaxAge: time.Minute, ChangedBy: "operator-1", Reason: "regional disaster exercise",
	})
	if err != nil || ownership.ActiveRegion != "cn-west" || ownership.Epoch != 2 || ownership.State != "failover" {
		t.Fatalf("ownership=%#v err=%v", ownership, err)
	}
	stale := eastLeases[0]
	err = database.store.CompletePoll(ctx, CompletePollParams{
		SourceID: stale.ID, LeaseToken: stale.LeaseToken, NextPollAt: time.Now().Add(time.Minute),
		ActiveRegion: stale.ActiveRegion, OwnershipEpoch: stale.OwnershipEpoch,
	})
	if !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale regional completion error=%v", err)
	}
	westLeases, err := database.store.AcquireDueSourcesInRegion(ctx, "cn-west", "west-worker", 1, time.Minute)
	if err != nil || len(westLeases) != 1 || westLeases[0].OwnershipEpoch != 2 {
		t.Fatalf("west leases=%#v err=%v", westLeases, err)
	}
	current := westLeases[0]
	if err := database.store.CompletePoll(ctx, CompletePollParams{
		SourceID: current.ID, LeaseToken: current.LeaseToken, NextPollAt: time.Now().Add(time.Hour),
		ActiveRegion: current.ActiveRegion, OwnershipEpoch: current.OwnershipEpoch,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.pool.Exec(ctx, `UPDATE regions SET last_heartbeat_at=statement_timestamp()-interval '10 minutes' WHERE id='cn-east'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.store.ChangeSourceRegion(ctx, ChangeSourceRegionParams{
		SourceID: integrationSourceID, TargetRegion: "cn-east", ExpectedEpoch: 2,
		HeartbeatMaxAge: time.Minute, ChangedBy: "operator-1", Reason: "unsafe early restore",
	}); err == nil || !strings.Contains(err.Error(), "not healthy") {
		t.Fatalf("stale target heartbeat error=%v", err)
	}
	if _, err := database.store.ChangeSourceRegion(ctx, ChangeSourceRegionParams{
		SourceID: integrationSourceID, TargetRegion: "cn-east", ExpectedEpoch: 1,
		HeartbeatMaxAge: time.Minute, ChangedBy: "operator-1", Reason: "stale command",
	}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale region change error=%v", err)
	}
}

func TestIntegrationRegionalChangeEnforcesTenantScope(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	tenantA, tenantB := integrationUUID(1580), integrationUUID(1581)
	if _, err := database.pool.Exec(ctx, `
INSERT INTO tenants(id,slug,name) VALUES($1,'scope-a','Scope A'),($2,'scope-b','Scope B')`, tenantA, tenantB); err != nil {
		t.Fatal(err)
	}
	sourceID := integrationUUID(1582)
	if _, err := database.pool.Exec(ctx, `
INSERT INTO sources(id,tenant_id,vendor_id,requested_url,canonical_url,source_type,next_poll_at)
VALUES($1,$2,$3,'https://private.example.test/status','https://private.example.test/status','status_page',statement_timestamp())`,
		sourceID, tenantA, integrationVendorID); err != nil {
		t.Fatal(err)
	}
	for _, region := range []string{"scope-east", "scope-west"} {
		if _, err := database.store.HeartbeatRegion(ctx, region, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.store.BootstrapSourceOwnership(ctx, "scope-east"); err != nil {
		t.Fatal(err)
	}
	_, err := database.store.ChangeSourceRegion(ctx, ChangeSourceRegionParams{
		SourceID: sourceID, ScopeTenantID: tenantB, TargetRegion: "scope-west", ExpectedEpoch: 1,
		HeartbeatMaxAge: time.Minute, ChangedBy: "tenant-b-admin", Reason: "cross tenant attempt",
	})
	if err == nil {
		t.Fatal("cross-tenant source failover was accepted")
	}
}

func TestIntegrationShadowRolloutQualityGatePromoteAndRollback(t *testing.T) {
	database := openIntegrationDatabase(t)
	ctx := context.Background()
	database.insertSource(t, integrationSourceID, time.Now().Add(time.Hour))
	rollout, err := database.store.CreateAdapterRollout(ctx, CreateAdapterRolloutParams{
		ID: integrationUUID(1600), SourceID: integrationSourceID,
		CandidateAdapterName: "statuspage-v2", CandidateAdapterVersion: "v2",
		SampleRate: 1, MinimumSamples: 2, MaximumMismatchRate: 0, MaximumErrorRate: 0,
		CreatedBy: "release-controller",
	})
	if err != nil || rollout.State != "shadow" || rollout.BaselineAdapterName != "statuspage" {
		t.Fatalf("rollout=%#v err=%v", rollout, err)
	}
	active, found, err := database.store.ActiveAdapterRollout(ctx, integrationSourceID)
	if err != nil || !found || active.ID != rollout.ID {
		t.Fatalf("active=%#v found=%v err=%v", active, found, err)
	}
	if _, err := database.store.PromoteAdapterRollout(ctx, DecideAdapterRolloutParams{
		RolloutID: rollout.ID, ChangedBy: "release-controller", Reason: "premature promotion",
	}); err == nil || !strings.Contains(err.Error(), "quality gate failed") {
		t.Fatalf("premature promotion error=%v", err)
	}
	digest := bytes.Repeat([]byte{0x42}, 32)
	for index := 0; index < 2; index++ {
		if err := database.store.RecordAdapterComparison(ctx, shadow.Comparison{
			ID: integrationUUID(1601 + index), RolloutID: rollout.ID, SourceID: integrationSourceID,
			ResourceKind: domain.ResourceSummary, ObservedAt: time.Now(), PrimaryDigest: digest,
			CandidateDigest: digest, Equivalent: true, PrimaryDuration: 10 * time.Millisecond,
			CandidateDuration: 12 * time.Millisecond, Difference: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := database.store.PromoteAdapterRollout(ctx, DecideAdapterRolloutParams{
		RolloutID: rollout.ID, ChangedBy: "release-controller", Reason: "quality gate passed",
	})
	if err != nil || stats.Total != 2 || stats.Mismatches != 0 || stats.Errors != 0 {
		t.Fatalf("promotion stats=%#v err=%v", stats, err)
	}
	var adapterName, adapterVersion string
	if err := database.pool.QueryRow(ctx, `SELECT adapter_name,adapter_version FROM sources WHERE id=$1`, integrationSourceID).Scan(&adapterName, &adapterVersion); err != nil {
		t.Fatal(err)
	}
	if adapterName != "statuspage-v2" || adapterVersion != "v2" {
		t.Fatalf("promoted adapter=%s@%s", adapterName, adapterVersion)
	}
	if err := database.store.RecordAdapterComparison(ctx, shadow.Comparison{
		ID: integrationUUID(1603), RolloutID: rollout.ID, SourceID: integrationSourceID,
		ResourceKind: domain.ResourceSummary, ObservedAt: time.Now(), PrimaryDigest: digest,
		CandidateDigest: digest, Equivalent: true,
	}); err != nil {
		t.Fatal(err)
	}
	stats, err = database.store.RollbackAdapterRollout(ctx, DecideAdapterRolloutParams{
		RolloutID: rollout.ID, ChangedBy: "release-controller", Reason: "post-promotion rollback exercise",
	})
	if err != nil || stats.Total != 2 {
		t.Fatalf("rollback stats=%#v err=%v", stats, err)
	}
	if err := database.pool.QueryRow(ctx, `SELECT adapter_name,adapter_version FROM sources WHERE id=$1`, integrationSourceID).Scan(&adapterName, &adapterVersion); err != nil {
		t.Fatal(err)
	}
	if adapterName != "statuspage" || adapterVersion != "v1" {
		t.Fatalf("restored adapter=%s@%s", adapterName, adapterVersion)
	}
}
