// statushub-admin contains deliberately narrow operational commands. It does
// not expose arbitrary SQL and never prints endpoint secrets.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Seeridia/StatusHub/internal/audit"
	"github.com/Seeridia/StatusHub/internal/auth"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type adminStore interface {
	CreateTenant(context.Context, store.CreateTenantParams) (store.Tenant, error)
	ListDeadLetters(context.Context, *time.Time, int) ([]store.DeadLetter, error)
	ReplayDeadLetter(context.Context, string, time.Time) (bool, error)
	CreateAWSAccountConnector(context.Context, store.CreateAWSAccountConnectorParams) (store.AWSAccountConnector, error)
	CreatePrivateAgent(context.Context, string, string) (store.PrivateAgentCredential, error)
	BindPrivateAgentEndpoint(context.Context, string, string) (bool, error)
	CreateServiceAccount(context.Context, store.CreateServiceAccountParams) (store.ServiceAccountCredential, error)
	AppendAuditEvent(context.Context, string, audit.AppendInput) (audit.Event, error)
	ExportAuditEvents(context.Context, string, int64, int) ([]audit.Event, error)
	ChangeSourceRegion(context.Context, store.ChangeSourceRegionParams) (store.SourceOwnership, error)
	CreateAdapterRollout(context.Context, store.CreateAdapterRolloutParams) (store.AdapterRollout, error)
	AdapterRolloutStatistics(context.Context, string) (store.AdapterRolloutStats, error)
	PromoteAdapterRollout(context.Context, store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error)
	RollbackAdapterRollout(context.Context, store.DecideAdapterRolloutParams) (store.AdapterRolloutStats, error)
	Close()
}

var openStore = func(ctx context.Context, databaseURL string) (adminStore, error) {
	return store.Open(ctx, databaseURL)
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("operation is required: tenant-create, dlq-list, dlq-replay, aws-connector-create, private-agent-create, private-agent-bind, setup-link, service-account-create, audit-export, audit-verify, source-region-change, adapter-rollout-create, adapter-rollout-status, adapter-rollout-promote, or adapter-rollout-rollback")
	}
	switch args[0] {
	case "tenant-create":
		return runTenantCreate(ctx, args[1:], output)
	case "setup-link":
		return runSetupLink(ctx, args[1:], output)
	case "dlq-list":
		return runDLQList(ctx, args[1:], output)
	case "dlq-replay":
		return runDLQReplay(ctx, args[1:], output)
	case "aws-connector-create":
		return runAWSConnectorCreate(ctx, args[1:], output)
	case "private-agent-create":
		return runPrivateAgentCreate(ctx, args[1:], output)
	case "private-agent-bind":
		return runPrivateAgentBind(ctx, args[1:], output)
	case "service-account-create":
		return runServiceAccountCreate(ctx, args[1:], output)
	case "audit-export":
		return runAuditExport(ctx, args[1:], output)
	case "audit-verify":
		return runAuditVerify(args[1:], output)
	case "source-region-change":
		return runSourceRegionChange(ctx, args[1:], output)
	case "adapter-rollout-create":
		return runAdapterRolloutCreate(ctx, args[1:], output)
	case "adapter-rollout-status":
		return runAdapterRolloutStatus(ctx, args[1:], output)
	case "adapter-rollout-promote":
		return runAdapterRolloutDecision(ctx, args[1:], output, true)
	case "adapter-rollout-rollback":
		return runAdapterRolloutDecision(ctx, args[1:], output, false)
	default:
		return fmt.Errorf("unsupported operation %q", args[0])
	}
}

func runTenantCreate(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("tenant-create", flag.ContinueOnError)
	email := f.String("admin-email", "", "existing administrator email")
	name := f.String("name", "", "workspace name")
	slug := f.String("slug", "", "workspace slug")
	if e := f.Parse(args); e != nil {
		return e
	}
	repo, e := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		return e
	}
	defer repo.Close()
	w, e := repo.CreateAdminWorkspace(ctx, *email, *name, *slug)
	if e != nil {
		return e
	}
	return writeJSON(out, w)
}
func runSetupLink(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("setup-link", flag.ContinueOnError)
	base := f.String("public-url", os.Getenv("STATUSHUB_PUBLIC_URL"), "public origin")
	if e := f.Parse(args); e != nil {
		return e
	}
	u, e := url.Parse(*base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return errors.New("valid public origin required")
	}
	repo, e := store.Open(ctx, os.Getenv("DATABASE_URL"))
	if e != nil {
		return e
	}
	defer repo.Close()
	token, e := repo.SetupLink(ctx)
	if e != nil {
		return e
	}
	return writeJSON(out, map[string]string{"setup_url": strings.TrimRight(*base, "/") + "/ui/#/setup?token=" + token})
}

func runSourceRegionChange(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("source-region-change", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	sourceID := flags.String("source-id", "", "source UUID")
	tenantID := flags.String("tenant-id", "", "tenant UUID; omit only for a global public source")
	targetRegion := flags.String("target-region", "", "healthy destination region")
	expectedEpoch := flags.Int64("expected-epoch", 0, "current ownership epoch")
	heartbeatMaxAge := flags.Duration("heartbeat-max-age", 30*time.Second, "maximum age of target region heartbeat")
	changedBy := flags.String("actor-id", "", "operator or automation identifier")
	reason := flags.String("reason", "", "change ticket or operational reason")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *sourceID == "" || *targetRegion == "" || *expectedEpoch <= 0 || *heartbeatMaxAge <= 0 || *changedBy == "" || *reason == "" {
		return errors.New("-source-id, -target-region, -expected-epoch, -actor-id, and -reason are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	ownership, err := repository.ChangeSourceRegion(ctx, store.ChangeSourceRegionParams{
		SourceID: *sourceID, ScopeTenantID: *tenantID, TargetRegion: *targetRegion,
		ExpectedEpoch: *expectedEpoch, HeartbeatMaxAge: *heartbeatMaxAge, ChangedBy: *changedBy, Reason: *reason,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, ownership)
}

func runAdapterRolloutCreate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("adapter-rollout-create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	rolloutID := flags.String("rollout-id", "", "optional rollout UUID")
	sourceID := flags.String("source-id", "", "source UUID")
	tenantID := flags.String("tenant-id", "", "tenant UUID; omit only for a global public source")
	candidateName := flags.String("candidate-name", "", "registered candidate adapter name")
	candidateVersion := flags.String("candidate-version", "", "candidate adapter version")
	sampleRate := flags.Float64("sample-rate", 1, "shadow sample rate in (0,1]")
	minimumSamples := flags.Int("minimum-samples", 100, "minimum comparisons before promotion")
	maximumMismatchRate := flags.Float64("maximum-mismatch-rate", 0, "maximum semantic mismatch rate")
	maximumErrorRate := flags.Float64("maximum-error-rate", 0.01, "maximum candidate error rate")
	createdBy := flags.String("actor-id", "", "operator or automation identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *sourceID == "" || *candidateName == "" || *candidateVersion == "" || *createdBy == "" {
		return errors.New("-source-id, -candidate-name, -candidate-version, and -actor-id are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	rollout, err := repository.CreateAdapterRollout(ctx, store.CreateAdapterRolloutParams{
		ID: *rolloutID, SourceID: *sourceID, ScopeTenantID: *tenantID,
		CandidateAdapterName: *candidateName, CandidateAdapterVersion: *candidateVersion,
		SampleRate: *sampleRate, MinimumSamples: *minimumSamples,
		MaximumMismatchRate: *maximumMismatchRate, MaximumErrorRate: *maximumErrorRate, CreatedBy: *createdBy,
	})
	if err != nil {
		return err
	}
	return writeJSON(output, rollout)
}

func runAdapterRolloutStatus(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("adapter-rollout-status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	rolloutID := flags.String("rollout-id", "", "rollout UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *rolloutID == "" {
		return errors.New("-rollout-id is required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	stats, err := repository.AdapterRolloutStatistics(ctx, *rolloutID)
	if err != nil {
		return err
	}
	return writeJSON(output, stats)
}

func runAdapterRolloutDecision(ctx context.Context, args []string, output io.Writer, promote bool) error {
	operation := "adapter-rollout-rollback"
	if promote {
		operation = "adapter-rollout-promote"
	}
	flags := flag.NewFlagSet(operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	rolloutID := flags.String("rollout-id", "", "rollout UUID")
	tenantID := flags.String("tenant-id", "", "tenant UUID; omit only for a global public source")
	changedBy := flags.String("actor-id", "", "operator or automation identifier")
	reason := flags.String("reason", "", "change ticket or operational reason")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *rolloutID == "" || *changedBy == "" || *reason == "" {
		return errors.New("-rollout-id, -actor-id, and -reason are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	params := store.DecideAdapterRolloutParams{RolloutID: *rolloutID, ScopeTenantID: *tenantID, ChangedBy: *changedBy, Reason: *reason}
	var stats store.AdapterRolloutStats
	if promote {
		stats, err = repository.PromoteAdapterRollout(ctx, params)
	} else {
		stats, err = repository.RollbackAdapterRollout(ctx, params)
	}
	if err != nil {
		return err
	}
	return writeJSON(output, stats)
}

func runServiceAccountCreate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("service-account-create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	accountID := flags.String("account-id", "", "optional service account UUID")
	name := flags.String("name", "", "unique service account name")
	role := flags.String("role", "", "viewer or operator")
	actorType := flags.String("actor-type", "system", "audit actor type")
	actorID := flags.String("actor-id", "statushub-admin", "audit actor identifier")
	requestID := flags.String("request-id", "", "optional audit request identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	parsedRole := auth.Role(strings.TrimSpace(*role))
	if flags.NArg() != 0 || strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*name) == "" || !parsedRole.Valid() {
		return errors.New("-tenant-id, -name, and a valid -role are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	credential, err := repository.CreateServiceAccount(ctx, store.CreateServiceAccountParams{
		ID: *accountID, TenantID: *tenantID, Name: *name, Role: parsedRole,
		Actor: store.AuditActor{Type: *actorType, ID: *actorID, RequestID: *requestID},
	})
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		ServiceAccount store.ServiceAccount `json:"service_account"`
		Token          string               `json:"token"`
		Warning        string               `json:"warning"`
	}{ServiceAccount: credential.ServiceAccount, Token: credential.Token,
		Warning: "token is shown once; store it in a secret manager"})
}

func runAuditExport(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("audit-export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	afterSequence := flags.Int64("after-sequence", 0, "exclusive sequence cursor")
	pageSize := flags.Int("page-size", 1000, "database page size in [1,10000]")
	actorType := flags.String("actor-type", "system", "audit actor type")
	actorID := flags.String("actor-id", "statushub-admin", "audit actor identifier")
	requestID := flags.String("request-id", "", "optional audit request identifier")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*tenantID) == "" || *afterSequence < 0 || *pageSize < 1 || *pageSize > 10000 {
		return errors.New("-tenant-id and a valid cursor/page size are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	metadata, _ := json.Marshal(map[string]any{"after_sequence": *afterSequence})
	if _, err := repository.AppendAuditEvent(ctx, *tenantID, audit.AppendInput{
		OccurredAt: time.Now().UTC(), ActorType: *actorType, ActorID: *actorID,
		Action: "audit.export", ResourceType: "tenant", ResourceID: *tenantID,
		Outcome: "success", RequestID: *requestID, Metadata: metadata,
	}); err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	cursor := *afterSequence
	for {
		events, err := repository.ExportAuditEvents(ctx, *tenantID, cursor, *pageSize)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
			cursor = event.Sequence
		}
		if len(events) < *pageSize {
			return nil
		}
	}
}

func runAuditVerify(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("audit-verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("file", "-", "NDJSON audit export or - for stdin")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	var input io.Reader = os.Stdin
	if *path != "-" {
		file, err := os.Open(*path)
		if err != nil {
			return err
		}
		defer file.Close()
		input = file
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	events := make([]audit.Event, 0, 1024)
	for scanner.Scan() {
		var event audit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("decode audit event %d: %w", len(events)+1, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if err := audit.Verify(events); err != nil {
		return err
	}
	result := struct {
		Verified bool   `json:"verified"`
		Count    int    `json:"count"`
		TenantID string `json:"tenant_id,omitempty"`
		LastHash string `json:"last_hash,omitempty"`
	}{Verified: true, Count: len(events)}
	if len(events) > 0 {
		result.TenantID = events[0].TenantID
		result.LastHash = events[len(events)-1].EventHash
	}
	return writeJSON(output, result)
}

func runPrivateAgentCreate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("private-agent-create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	name := flags.String("name", "", "unique agent name within the tenant")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*name) == "" {
		return errors.New("-tenant-id and -name are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	credential, err := repository.CreatePrivateAgent(ctx, *tenantID, *name)
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		AgentID string `json:"agent_id"`
		Token   string `json:"token"`
		Warning string `json:"warning"`
	}{AgentID: credential.Agent.ID, Token: credential.Token, Warning: "token is shown once; store it in the agent secret manager"})
}

func runPrivateAgentBind(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("private-agent-bind", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	agentID := flags.String("agent-id", "", "private agent UUID")
	endpointID := flags.String("endpoint-id", "", "private_agent endpoint UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*agentID) == "" || strings.TrimSpace(*endpointID) == "" {
		return errors.New("-agent-id and -endpoint-id are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	bound, err := repository.BindPrivateAgentEndpoint(ctx, *agentID, *endpointID)
	if err != nil {
		return err
	}
	if !bound {
		return errors.New("agent and enabled private_agent endpoint must belong to the same tenant")
	}
	return writeJSON(output, struct {
		AgentID    string `json:"agent_id"`
		EndpointID string `json:"endpoint_id"`
		Bound      bool   `json:"bound"`
	}{AgentID: *agentID, EndpointID: *endpointID, Bound: true})
}

func runAWSConnectorCreate(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("aws-connector-create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	tenantID := flags.String("tenant-id", "", "tenant UUID")
	accountID := flags.String("account-id", "", "12-digit AWS account ID")
	topicARN := flags.String("sns-topic-arn", "", "dedicated SNS topic ARN")
	regions := flags.String("regions", "", "optional comma-separated AWS region allowlist")
	services := flags.String("services", "", "optional comma-separated AWS service allowlist")
	connectorID := flags.String("connector-id", "", "optional preallocated connector UUID")
	sourceID := flags.String("source-id", "", "optional preallocated source UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*tenantID) == "" || strings.TrimSpace(*accountID) == "" || strings.TrimSpace(*topicARN) == "" {
		return errors.New("-tenant-id, -account-id, and -sns-topic-arn are required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	connector, err := repository.CreateAWSAccountConnector(ctx, store.CreateAWSAccountConnectorParams{
		ID: *connectorID, SourceID: *sourceID, TenantID: *tenantID,
		ExternalAccountID: *accountID, SNSTopicARN: *topicARN,
		AllowedRegions: splitCSV(*regions), AllowedServices: splitCSV(*services),
	})
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		ConnectorID string `json:"connector_id"`
		SourceID    string `json:"source_id"`
		IngressPath string `json:"ingress_path"`
		SNSTopicARN string `json:"sns_topic_arn"`
	}{ConnectorID: connector.ID, SourceID: connector.SourceID,
		IngressPath: "/v1/connectors/aws-health/" + connector.ID, SNSTopicARN: connector.SNSTopicARN})
}

func runDLQList(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("dlq-list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	limit := flags.Int("limit", 100, "maximum results in [1,500]")
	cursorValue := flags.String("before", "", "exclusive RFC3339 dead-letter timestamp cursor")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	var cursor *time.Time
	if strings.TrimSpace(*cursorValue) != "" {
		value, err := time.Parse(time.RFC3339Nano, *cursorValue)
		if err != nil {
			return fmt.Errorf("parse -before: %w", err)
		}
		value = value.UTC()
		cursor = &value
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	items, err := repository.ListDeadLetters(ctx, cursor, *limit)
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		DeadLetters []store.DeadLetter `json:"dead_letters"`
	}{DeadLetters: items})
}

func runDLQReplay(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("dlq-replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databaseURL := flags.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	deliveryID := flags.String("delivery-id", "", "dead-letter delivery UUID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*deliveryID) == "" {
		return errors.New("-delivery-id is required")
	}
	repository, err := connect(ctx, *databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	replayed, err := repository.ReplayDeadLetter(ctx, *deliveryID, time.Now().UTC())
	if err != nil {
		return err
	}
	return writeJSON(output, struct {
		DeliveryID string `json:"delivery_id"`
		Replayed   bool   `json:"replayed"`
	}{DeliveryID: *deliveryID, Replayed: replayed})
}

func connect(ctx context.Context, databaseURL string) (adminStore, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("-database-url or DATABASE_URL is required")
	}
	repository, err := openStore(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return repository, nil
}

func splitCSV(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func writeJSON(output io.Writer, value any) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
