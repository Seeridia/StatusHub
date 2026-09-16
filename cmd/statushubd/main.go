package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Seeridia/StatusHub/internal/adapter"
	"github.com/Seeridia/StatusHub/internal/adapter/awshealth"
	"github.com/Seeridia/StatusHub/internal/adapter/ecosystem"
	shadowadapter "github.com/Seeridia/StatusHub/internal/adapter/shadow"
	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
	"github.com/Seeridia/StatusHub/internal/adapter/vendorprofile"
	busjs "github.com/Seeridia/StatusHub/internal/bus/jetstream"
	providercallback "github.com/Seeridia/StatusHub/internal/callback"
	"github.com/Seeridia/StatusHub/internal/collector"
	"github.com/Seeridia/StatusHub/internal/connector/awsaccount"
	"github.com/Seeridia/StatusHub/internal/notify"
	"github.com/Seeridia/StatusHub/internal/pipeline/fanout"
	notifierpipeline "github.com/Seeridia/StatusHub/internal/pipeline/notifier"
	outboxpipeline "github.com/Seeridia/StatusHub/internal/pipeline/outbox"
	eventprocessor "github.com/Seeridia/StatusHub/internal/pipeline/processor"
	"github.com/Seeridia/StatusHub/internal/privateagent"
	"github.com/Seeridia/StatusHub/internal/scheduler"
	"github.com/Seeridia/StatusHub/internal/secret"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"github.com/Seeridia/StatusHub/internal/telemetry"
	"github.com/Seeridia/StatusHub/internal/transport"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

type config struct {
	databaseURL       string
	natsURL           string
	workerID          string
	serviceRegion     string
	metricsAddress    string
	collectorInterval time.Duration
	publisherInterval time.Duration
	fanoutInterval    time.Duration
	notifierInterval  time.Duration
	awsRegion         string
	htmlRecipesFile   string
	configKey         string
	configKeyID       string
	once              bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("statushubd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	hostname, _ := os.Hostname()
	result := config{}
	flags.StringVar(&result.databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	flags.StringVar(&result.natsURL, "nats-url", environmentOr("NATS_URL", "nats://127.0.0.1:54222"), "NATS connection URL")
	flags.StringVar(&result.workerID, "worker-id", hostname, "stable worker identity")
	flags.StringVar(&result.serviceRegion, "service-region", environmentOr("STATUSHUB_REGION", "local"), "regional ownership identity")
	flags.StringVar(&result.metricsAddress, "metrics-address", "127.0.0.1:9464", "Prometheus listen address; empty disables")
	flags.DurationVar(&result.collectorInterval, "collector-interval", 500*time.Millisecond, "idle scan interval for due sources")
	flags.DurationVar(&result.publisherInterval, "publisher-interval", 50*time.Millisecond, "outbox scan interval")
	flags.DurationVar(&result.fanoutInterval, "fanout-interval", 25*time.Millisecond, "fanout shard scan interval")
	flags.DurationVar(&result.notifierInterval, "notifier-interval", 25*time.Millisecond, "notification delivery scan interval")
	flags.StringVar(&result.awsRegion, "aws-region", environmentOr("AWS_REGION", "us-east-1"), "AWS region used by the SES driver")
	flags.StringVar(&result.htmlRecipesFile, "html-recipes-file", os.Getenv("STATUSHUB_HTML_RECIPES_FILE"), "JSON file containing controlled HTML extraction recipes")
	flags.StringVar(&result.configKey, "config-key", os.Getenv("STATUSHUB_CONFIG_KEY"), "base64 AES-256 endpoint configuration key")
	flags.StringVar(&result.configKeyID, "config-key-id", os.Getenv("STATUSHUB_CONFIG_KEY_ID"), "endpoint configuration key identifier")
	flags.BoolVar(&result.once, "once", false, "run one collector and publisher pass, then exit")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(result.databaseURL) == "" {
		return config{}, errors.New("-database-url or DATABASE_URL is required")
	}
	if strings.TrimSpace(result.natsURL) == "" || strings.TrimSpace(result.workerID) == "" || strings.TrimSpace(result.serviceRegion) == "" {
		return config{}, errors.New("NATS URL, worker ID, and service region are required")
	}
	if result.collectorInterval <= 0 || result.publisherInterval <= 0 || result.fanoutInterval <= 0 || result.notifierInterval <= 0 {
		return config{}, errors.New("worker intervals must be positive")
	}
	if (result.configKey == "") != (result.configKeyID == "") {
		return config{}, errors.New("endpoint config key and key ID must be configured together")
	}
	return result, nil
}

func run(parent context.Context, args []string, output io.Writer) error {
	settings, err := parseConfig(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	repository, err := store.Open(ctx, settings.databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	regionMetadata, _ := json.Marshal(map[string]string{"worker_id": settings.workerID})
	if _, err := repository.HeartbeatRegion(ctx, settings.serviceRegion, regionMetadata); err != nil {
		return err
	}
	if _, err := repository.BootstrapSourceOwnership(ctx, settings.serviceRegion); err != nil {
		return err
	}

	transportConfig := transport.DefaultConfig()
	transportConfig.MaxBodyBytes = 8 << 20
	httpClient, err := transport.New(transportConfig)
	if err != nil {
		return err
	}
	defer httpClient.CloseIdleConnections()
	statusAdapter, err := statuspage.New(httpClient)
	if err != nil {
		return err
	}
	awsAdapter, err := awshealth.New(httpClient)
	if err != nil {
		return err
	}
	ecosystemOptions, err := loadHTMLRecipeOptions(settings.htmlRecipesFile)
	if err != nil {
		return err
	}
	ecosystemAdapter, err := ecosystem.New(httpClient, ecosystemOptions...)
	if err != nil {
		return err
	}
	vendorAdapter, err := vendorprofile.New(statusAdapter, awsAdapter, ecosystemAdapter)
	if err != nil {
		return err
	}
	shadowCandidates := map[string]adapter.Adapter{
		statuspage.Engine + "@" + statuspage.AdapterVersion: statusAdapter,
		awshealth.Engine + "@" + awshealth.AdapterVersion:   awsAdapter,
	}
	for _, engine := range []string{
		ecosystem.EngineIncidentIO, ecosystem.EngineInstatus, ecosystem.EngineBetterStack,
		ecosystem.EngineStatusIO, ecosystem.EngineCachet, ecosystem.EngineGatus,
		ecosystem.EngineCState, ecosystem.EngineHTMLRecipe,
	} {
		shadowCandidates[engine+"@"+ecosystem.AdapterVersion] = ecosystemAdapter
	}
	shadowAdapter, err := shadowadapter.New(vendorAdapter, shadowCandidates, repository, shadowadapter.DefaultConfig())
	if err != nil {
		return err
	}
	defer func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer shutdownCancel()
		_ = shadowAdapter.Close(shutdownContext)
	}()

	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	metrics, err := telemetry.NewPrometheus(registry)
	if err != nil {
		return err
	}
	defer func() { _ = metrics.Shutdown(context.Background()) }()

	eventProcessor, err := eventprocessor.New(repository, eventprocessor.DefaultSubject)
	if err != nil {
		return err
	}
	resourcePlanner, err := scheduler.NewResourcePlanner(scheduler.StatuspageCadences(), nil)
	if err != nil {
		return err
	}
	collectorConfig := collector.DefaultConfig(settings.workerID + "/collector")
	collectorConfig.Region = settings.serviceRegion
	collectorWorker, err := collector.New(
		repository, shadowAdapter, eventProcessor, scheduler.NewDefaultPolicy(), resourcePlanner,
		metrics, collectorConfig,
	)
	if err != nil {
		return err
	}

	eventBus, err := busjs.Connect(ctx, settings.natsURL, busjs.DefaultConfig(), nats.Name("statushubd-"+settings.workerID))
	if err != nil {
		return err
	}
	defer eventBus.Close()
	outboxPublisher, err := outboxpipeline.New(
		repository, eventBus, metrics, outboxpipeline.DefaultConfig(settings.workerID+"/outbox"),
	)
	if err != nil {
		return err
	}
	fanoutWorker, err := fanout.New(repository, metrics, fanout.DefaultConfig(settings.workerID+"/fanout"))
	if err != nil {
		return err
	}
	fanoutConsumer, err := eventBus.Consumer(ctx, busjs.DefaultConsumerConfig("statushub_fanout", "statushub.events.>"))
	if err != nil {
		return err
	}
	notifyTransportConfig := transport.DefaultConfig()
	notifyTransportConfig.DisableRedirects = true
	notifyHTTPClient, err := transport.New(notifyTransportConfig)
	if err != nil {
		return err
	}
	defer notifyHTTPClient.CloseIdleConnections()
	snsVerifier, err := providercallback.NewSNSVerifier(notifyHTTPClient)
	if err != nil {
		return err
	}
	callbackHandler, err := providercallback.NewHandler(repository, snsVerifier)
	if err != nil {
		return err
	}
	awsAccountHandler, err := awsaccount.NewHandler(repository, snsVerifier)
	if err != nil {
		return err
	}
	privateAgentHandler, err := privateagent.NewHandler(repository)
	if err != nil {
		return err
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(settings.awsRegion))
	if err != nil {
		return fmt.Errorf("load AWS configuration for SES: %w", err)
	}
	notificationDrivers := []notify.ChannelDriver{
		notifierpipeline.Bind(notify.ChannelGenericWebhook, notify.NewGenericWebhook(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelSlack, notify.NewSlack(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelPagerDuty, notify.NewPagerDuty(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelTwilioSMS, notify.NewTwilioSMS(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelEmailSES, notify.NewSES(notify.NewAWSSESClient(sesv2.NewFromConfig(awsConfiguration)))),
		notifierpipeline.Bind(notify.ChannelTeams, notify.NewTeams(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelDiscord, notify.NewDiscord(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelTelegram, notify.NewTelegram(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelLark, notify.NewLark(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelSMTP, notify.NewSMTP()),
		notifierpipeline.Bind(notify.ChannelDingTalk, notify.NewDingTalk(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelWeCom, notify.NewWeCom(notifyHTTPClient)),
		notifierpipeline.Bind(notify.ChannelShoutrrr, notify.NewShoutrrr(nil)),
	}
	notificationWorkers := make([]*notifierpipeline.Worker, 0, 3)
	var configDecoder notifierpipeline.ConfigDecoder = notifierpipeline.JSONConfigDecoder{}
	if settings.configKey != "" {
		key, keyErr := secret.ParseBase64Key(settings.configKey)
		if keyErr != nil {
			return keyErr
		}
		envelope, keyErr := secret.NewStaticEnvelope(settings.configKeyID, key)
		clear(key)
		if keyErr != nil {
			return keyErr
		}
		configDecoder = notifierpipeline.EnvelopeConfigDecoder{Opener: envelope}
	}
	for _, laneConfig := range notifierpipeline.DefaultLaneConfigs(settings.workerID + "/notifier") {
		laneConfig.PublicURL = os.Getenv("STATUSHUB_PUBLIC_URL")
		worker, workerErr := notifierpipeline.New(repository, notificationDrivers, configDecoder, metrics, laneConfig)
		if workerErr != nil {
			return workerErr
		}
		notificationWorkers = append(notificationWorkers, worker)
	}

	if settings.once {
		collectorStats, collectorErr := collectorWorker.RunOnce(ctx)
		publisherStats, publisherErr := outboxPublisher.RunOnce(ctx)
		consumed, consumerErr := fanoutConsumer.ProcessBatch(ctx, 128, 250*time.Millisecond, func(ctx context.Context, delivery busjs.Delivery) error {
			err := fanoutWorker.HandleMessage(ctx, delivery.Data)
			result := "success"
			if err != nil {
				result = "error"
			}
			metrics.RecordBusDelivery(ctx, "fanout", result, delivery.NumDelivered)
			return err
		})
		fanoutStats, fanoutErr := fanoutWorker.RunOnce(ctx)
		notifierStats := make(map[string]notifierpipeline.Stats, len(notificationWorkers))
		var notifierErrors []error
		for _, worker := range notificationWorkers {
			stats, runErr := worker.RunOnce(ctx)
			notifierStats[string(stats.Lane)] = stats
			notifierErrors = append(notifierErrors, runErr)
		}
		if err := json.NewEncoder(output).Encode(struct {
			Collector collector.Stats                   `json:"collector"`
			Publisher outboxpipeline.Stats              `json:"publisher"`
			Consumed  int                               `json:"consumed"`
			Fanout    fanout.Stats                      `json:"fanout"`
			Notifier  map[string]notifierpipeline.Stats `json:"notifier"`
		}{collectorStats, publisherStats, consumed, fanoutStats, notifierStats}); err != nil {
			return err
		}
		return errors.Join(collectorErr, publisherErr, consumerErr, fanoutErr, errors.Join(notifierErrors...))
	}

	serverErrors := make(chan error, 1)
	var metricsServer *http.Server
	if settings.metricsAddress != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		mux.Handle("/v1/provider-callbacks/", callbackHandler)
		mux.Handle("/v1/connectors/aws-health/", awsAccountHandler)
		mux.Handle("/v1/private-agents/", privateAgentHandler)
		metricsServer = &http.Server{
			Addr: settings.metricsAddress, Handler: mux,
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second,
		}
		go func() {
			if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				serverErrors <- fmt.Errorf("metrics server: %w", err)
			}
		}()
	}

	workerErrors := make(chan error, 6+len(notificationWorkers))
	go repeat(ctx, 10*time.Second, func(ctx context.Context) error {
		if _, err := repository.HeartbeatRegion(ctx, settings.serviceRegion, regionMetadata); err != nil {
			return err
		}
		_, err := repository.BootstrapSourceOwnership(ctx, settings.serviceRegion)
		return err
	}, workerErrors)
	go repeat(ctx, settings.collectorInterval, func(ctx context.Context) error {
		_, err := collectorWorker.RunOnce(ctx)
		return err
	}, workerErrors)
	go repeat(ctx, settings.publisherInterval, func(ctx context.Context) error {
		_, err := outboxPublisher.RunOnce(ctx)
		return err
	}, workerErrors)
	go repeat(ctx, settings.fanoutInterval, func(ctx context.Context) error {
		_, err := fanoutConsumer.ProcessBatch(ctx, 128, 250*time.Millisecond, func(ctx context.Context, delivery busjs.Delivery) error {
			handleErr := fanoutWorker.HandleMessage(ctx, delivery.Data)
			result := "success"
			if handleErr != nil {
				result = "error"
			}
			metrics.RecordBusDelivery(ctx, "fanout", result, delivery.NumDelivered)
			return handleErr
		})
		if err != nil {
			return err
		}
		_, err = fanoutWorker.RunOnce(ctx)
		return err
	}, workerErrors)
	for _, worker := range notificationWorkers {
		worker := worker
		go repeat(ctx, settings.notifierInterval, func(ctx context.Context) error {
			_, err := worker.RunOnce(ctx)
			return err
		}, workerErrors)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	for {
		select {
		case <-ctx.Done():
			if metricsServer != nil {
				shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				_ = metricsServer.Shutdown(shutdownContext)
			}
			return ctx.Err()
		case err := <-workerErrors:
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("worker iteration failed", "error", err)
			}
		case err := <-serverErrors:
			return err
		}
	}
}

func loadHTMLRecipeOptions(path string) ([]ecosystem.Option, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read HTML recipes: %w", err)
	}
	var recipes []ecosystem.HTMLRecipe
	if err := json.Unmarshal(data, &recipes); err != nil {
		return nil, fmt.Errorf("decode HTML recipes: %w", err)
	}
	return []ecosystem.Option{ecosystem.WithHTMLRecipes(recipes...)}, nil
}

func repeat(ctx context.Context, interval time.Duration, operation func(context.Context) error, errorsChannel chan<- error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := operation(ctx); err != nil {
			select {
			case errorsChannel <- err:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func environmentOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
