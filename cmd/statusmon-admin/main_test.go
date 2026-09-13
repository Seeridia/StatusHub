package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/audit"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/auth"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type fakeAdminStore struct {
	tenant          store.CreateTenantParams
	replayedID      string
	connector       store.CreateAWSAccountConnectorParams
	agentTenant     string
	boundAgent      string
	boundEndpoint   string
	oidcProvider    store.UpsertOIDCProviderParams
	membership      store.SetTenantMembershipParams
	serviceAccount  store.CreateServiceAccountParams
	auditEvents     []audit.Event
	regionChange    store.ChangeSourceRegionParams
	rolloutCreate   store.CreateAdapterRolloutParams
	rolloutDecision store.DecideAdapterRolloutParams
}

func (s *fakeAdminStore) CreateTenant(_ context.Context, params store.CreateTenantParams) (store.Tenant, error) {
	s.tenant = params
	return store.Tenant{ID: "tenant-1", Slug: params.Slug, Name: params.Name}, nil
}

func (*fakeAdminStore) ListDeadLetters(context.Context, *time.Time, int) ([]store.DeadLetter, error) {
	return []store.DeadLetter{{ID: "delivery-1", Reason: "timeout"}}, nil
}
func (s *fakeAdminStore) ReplayDeadLetter(_ context.Context, id string, _ time.Time) (bool, error) {
	s.replayedID = id
	return true, nil
}
func (*fakeAdminStore) Close() {}
func (s *fakeAdminStore) CreateAWSAccountConnector(_ context.Context, params store.CreateAWSAccountConnectorParams) (store.AWSAccountConnector, error) {
	s.connector = params
	return store.AWSAccountConnector{ID: "connector-1", SourceID: "source-1", SNSTopicARN: params.SNSTopicARN}, nil
}
func (s *fakeAdminStore) CreatePrivateAgent(_ context.Context, tenantID, _ string) (store.PrivateAgentCredential, error) {
	s.agentTenant = tenantID
	return store.PrivateAgentCredential{Agent: store.PrivateAgent{ID: "agent-1"}, Token: "secret"}, nil
}
func (s *fakeAdminStore) BindPrivateAgentEndpoint(_ context.Context, agentID, endpointID string) (bool, error) {
	s.boundAgent, s.boundEndpoint = agentID, endpointID
	return true, nil
}
func (s *fakeAdminStore) UpsertOIDCProvider(_ context.Context, params store.UpsertOIDCProviderParams) (auth.OIDCProvider, error) {
	s.oidcProvider = params
	return auth.OIDCProvider{ID: "provider-1", TenantID: params.TenantID, Issuer: params.Issuer}, nil
}
func (s *fakeAdminStore) SetTenantMembership(_ context.Context, params store.SetTenantMembershipParams) (auth.Identity, error) {
	s.membership = params
	return auth.Identity{TenantID: params.TenantID, ActorID: "principal-1", Role: params.Role}, nil
}
func (s *fakeAdminStore) CreateServiceAccount(_ context.Context, params store.CreateServiceAccountParams) (store.ServiceAccountCredential, error) {
	s.serviceAccount = params
	return store.ServiceAccountCredential{ServiceAccount: store.ServiceAccount{ID: "sa-1", TenantID: params.TenantID, Role: params.Role}, Token: "secret"}, nil
}
func (s *fakeAdminStore) AppendAuditEvent(_ context.Context, tenantID string, input audit.AppendInput) (audit.Event, error) {
	event, _, err := audit.NewEvent(tenantID, int64(len(s.auditEvents)+1), audit.ZeroHash, input)
	if len(s.auditEvents) > 0 {
		previous, _ := hex.DecodeString(s.auditEvents[len(s.auditEvents)-1].EventHash)
		event, _, err = audit.NewEvent(tenantID, int64(len(s.auditEvents)+1), previous, input)
	}
	s.auditEvents = append(s.auditEvents, event)
	return event, err
}
func (s *fakeAdminStore) ExportAuditEvents(_ context.Context, _ string, after int64, limit int) ([]audit.Event, error) {
	var result []audit.Event
	for _, event := range s.auditEvents {
		if event.Sequence > after && len(result) < limit {
			result = append(result, event)
		}
	}
	return result, nil
}
func (s *fakeAdminStore) ChangeSourceRegion(_ context.Context, params store.ChangeSourceRegionParams) (store.SourceOwnership, error) {
	s.regionChange = params
	return store.SourceOwnership{SourceID: params.SourceID, ActiveRegion: params.TargetRegion, Epoch: params.ExpectedEpoch + 1}, nil
}
func (s *fakeAdminStore) CreateAdapterRollout(_ context.Context, params store.CreateAdapterRolloutParams) (store.AdapterRollout, error) {
	s.rolloutCreate = params
	return store.AdapterRollout{State: "shadow"}, nil
}
func (*fakeAdminStore) AdapterRolloutStatistics(context.Context, string) (store.AdapterRolloutStats, error) {
	return store.AdapterRolloutStats{Total: 100}, nil
}
func (s *fakeAdminStore) PromoteAdapterRollout(_ context.Context, params store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error) {
	s.rolloutDecision = params
	return store.AdapterRolloutStats{Total: 100}, nil
}
func (s *fakeAdminStore) RollbackAdapterRollout(_ context.Context, params store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error) {
	s.rolloutDecision = params
	return store.AdapterRolloutStats{Total: 100}, nil
}

func TestRunDLQCommands(t *testing.T) {
	previous := openStore
	t.Cleanup(func() { openStore = previous })
	fake := &fakeAdminStore{}
	openStore = func(context.Context, string) (adminStore, error) { return fake, nil }
	if err := run(context.Background(), []string{"dlq-list", "-database-url", "postgres://test"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"dlq-replay", "-database-url", "postgres://test", "-delivery-id", "delivery-1"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.replayedID != "delivery-1" {
		t.Fatalf("replayed ID=%q", fake.replayedID)
	}
	if err := run(context.Background(), []string{"aws-connector-create", "-database-url", "postgres://test",
		"-tenant-id", "tenant-1", "-account-id", "123456789012",
		"-sns-topic-arn", "arn:aws:sns:us-east-1:123456789012:health", "-regions", "us-east-1,us-west-2"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.connector.ExternalAccountID != "123456789012" || len(fake.connector.AllowedRegions) != 2 {
		t.Fatalf("connector params=%#v", fake.connector)
	}
	if err := run(context.Background(), []string{"private-agent-create", "-database-url", "postgres://test",
		"-tenant-id", "tenant-1", "-name", "dc-agent"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"private-agent-bind", "-database-url", "postgres://test",
		"-agent-id", "agent-1", "-endpoint-id", "endpoint-1"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.agentTenant != "tenant-1" || fake.boundAgent != "agent-1" || fake.boundEndpoint != "endpoint-1" {
		t.Fatalf("agent tenant=%q binding=%q/%q", fake.agentTenant, fake.boundAgent, fake.boundEndpoint)
	}
}

func TestRunTenantCreate(t *testing.T) {
	previous := openStore
	t.Cleanup(func() { openStore = previous })
	fake := &fakeAdminStore{}
	openStore = func(context.Context, string) (adminStore, error) { return fake, nil }
	var output bytes.Buffer
	if err := run(context.Background(), []string{"tenant-create", "-database-url", "postgres://test", "-slug", "acme", "-name", "Acme"}, &output); err != nil {
		t.Fatal(err)
	}
	if fake.tenant.Slug != "acme" || !strings.Contains(output.String(), "tenant-1") {
		t.Fatalf("tenant=%#v output=%s", fake.tenant, output.String())
	}
}

func TestRunIdentityAndAuditCommands(t *testing.T) {
	previous := openStore
	t.Cleanup(func() { openStore = previous })
	fake := &fakeAdminStore{}
	openStore = func(context.Context, string) (adminStore, error) { return fake, nil }
	common := []string{"-database-url", "postgres://test", "-tenant-id", "tenant-1"}
	if err := run(context.Background(), append([]string{"oidc-provider-upsert"}, append(common,
		"-issuer", "https://id.example.com", "-client-id", "statusmon", "-allowed-domains", "example.com")...), io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.oidcProvider.ClientID != "statusmon" || len(fake.oidcProvider.AllowedDomains) != 1 {
		t.Fatalf("OIDC params=%#v", fake.oidcProvider)
	}
	if err := run(context.Background(), append([]string{"tenant-member-set"}, append(common,
		"-issuer", "https://id.example.com", "-subject", "user-1", "-role", "admin")...), io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.membership.Role != auth.RoleAdmin {
		t.Fatalf("membership=%#v", fake.membership)
	}
	if err := run(context.Background(), append([]string{"service-account-create"}, append(common,
		"-name", "automation", "-role", "operator")...), io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.serviceAccount.Role != auth.RoleOperator {
		t.Fatalf("service account=%#v", fake.serviceAccount)
	}
	var exported bytes.Buffer
	if err := run(context.Background(), append([]string{"audit-export"}, common...), &exported); err != nil {
		t.Fatal(err)
	}
	var exportedEvent audit.Event
	if err := json.Unmarshal(bytes.TrimSpace(exported.Bytes()), &exportedEvent); err != nil || exportedEvent.Action != "audit.export" {
		t.Fatalf("export=%s err=%v", exported.String(), err)
	}
}

func TestRunRegionAndRolloutCommands(t *testing.T) {
	previous := openStore
	t.Cleanup(func() { openStore = previous })
	fake := &fakeAdminStore{}
	openStore = func(context.Context, string) (adminStore, error) { return fake, nil }
	if err := run(context.Background(), []string{"source-region-change", "-database-url", "postgres://test",
		"-source-id", "source-1", "-target-region", "west", "-expected-epoch", "3",
		"-actor-id", "operator-1", "-reason", "DR exercise"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.regionChange.ExpectedEpoch != 3 || fake.regionChange.TargetRegion != "west" {
		t.Fatalf("region change=%#v", fake.regionChange)
	}
	if err := run(context.Background(), []string{"adapter-rollout-create", "-database-url", "postgres://test",
		"-source-id", "source-1", "-candidate-name", "candidate", "-candidate-version", "v2",
		"-actor-id", "operator-1", "-minimum-samples", "250"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.rolloutCreate.MinimumSamples != 250 {
		t.Fatalf("rollout create=%#v", fake.rolloutCreate)
	}
	if err := run(context.Background(), []string{"adapter-rollout-promote", "-database-url", "postgres://test",
		"-rollout-id", "rollout-1", "-actor-id", "operator-1", "-reason", "gate passed"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if fake.rolloutDecision.RolloutID != "rollout-1" {
		t.Fatalf("rollout decision=%#v", fake.rolloutDecision)
	}
}
