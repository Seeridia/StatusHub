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
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Seeridia/StatusHub/internal/adapter/awshealth"
	"github.com/Seeridia/StatusHub/internal/adapter/ecosystem"
	"github.com/Seeridia/StatusHub/internal/adapter/statuspage"
	"github.com/Seeridia/StatusHub/internal/adapter/vendorprofile"
	"github.com/Seeridia/StatusHub/internal/auth"
	"github.com/Seeridia/StatusHub/internal/bus"
	"github.com/Seeridia/StatusHub/internal/controlplane"
	"github.com/Seeridia/StatusHub/internal/notify"
	notifierpipeline "github.com/Seeridia/StatusHub/internal/pipeline/notifier"
	"github.com/Seeridia/StatusHub/internal/secret"
	store "github.com/Seeridia/StatusHub/internal/store/postgres"
	"github.com/Seeridia/StatusHub/internal/transport"
	"github.com/nats-io/nats.go"
)

type config struct {
	databaseURL     string
	natsURL         string
	listenAddress   string
	publicURL       string
	serviceRegion   string
	workerID        string
	configKey       string
	configKeyID     string
	apiKey          string
	htmlRecipesFile string
	allowHTTP       bool
	trustedProxies  []netip.Prefix
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, context.Canceled) {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("statushub-api", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	hostname, _ := os.Hostname()
	result := config{}
	flags.StringVar(&result.databaseURL, "database-url", os.Getenv("DATABASE_URL"), "PostgreSQL connection URL")
	flags.StringVar(&result.natsURL, "nats-url", environmentOr("NATS_URL", "nats://127.0.0.1:54222"), "NATS URL")
	flags.StringVar(&result.listenAddress, "listen-address", environmentOr("STATUSHUB_API_ADDRESS", "127.0.0.1:8080"), "HTTP listen address")
	flags.StringVar(&result.publicURL, "public-url", environmentOr("STATUSHUB_PUBLIC_URL", "http://127.0.0.1:8080"), "browser-visible base URL")
	flags.StringVar(&result.serviceRegion, "service-region", environmentOr("STATUSHUB_REGION", "local"), "source ownership region")
	flags.StringVar(&result.workerID, "worker-id", hostname+"/api", "endpoint test worker identity")
	flags.StringVar(&result.configKey, "config-key", os.Getenv("STATUSHUB_CONFIG_KEY"), "base64 AES-256 endpoint config key")
	flags.StringVar(&result.configKeyID, "config-key-id", environmentOr("STATUSHUB_CONFIG_KEY_ID", "local-v1"), "endpoint config key ID")
	flags.StringVar(&result.apiKey, "api-key", os.Getenv("STATUSHUB_API_KEY"), "base64 32-byte cursor/session key")
	flags.StringVar(&result.htmlRecipesFile, "html-recipes-file", os.Getenv("STATUSHUB_HTML_RECIPES_FILE"), "controlled HTML recipe file")
	flags.BoolVar(&result.allowHTTP, "allow-local-http", false, "allow loopback HTTP for local development")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if result.databaseURL == "" || result.natsURL == "" || result.listenAddress == "" || result.publicURL == "" ||
		result.serviceRegion == "" || result.workerID == "" {
		return config{}, errors.New("database, NATS, listen/public URLs, region, and worker ID are required")
	}
	if result.configKey == "" || result.configKeyID == "" || result.apiKey == "" {
		return config{}, errors.New("STATUSHUB_CONFIG_KEY, STATUSHUB_CONFIG_KEY_ID, and STATUSHUB_API_KEY are required")
	}
	parsedPublic, err := url.Parse(result.publicURL)
	if err != nil || parsedPublic.Host == "" || (parsedPublic.Scheme != "https" && !(result.allowHTTP && parsedPublic.Scheme == "http")) {
		return config{}, errors.New("public URL must be HTTPS; local HTTP requires -allow-local-http")
	}
	for _, v := range strings.Split(os.Getenv("STATUSHUB_TRUSTED_PROXIES"), ",") {
		if strings.TrimSpace(v) == "" {
			continue
		}
		p, e := netip.ParsePrefix(strings.TrimSpace(v))
		if e != nil {
			return config{}, errors.New("invalid trusted proxy CIDR")
		}
		result.trustedProxies = append(result.trustedProxies, p)
	}
	return result, nil
}

func run(parent context.Context, args []string) error {
	settings, err := parseConfig(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	repository, err := store.Open(ctx, settings.databaseURL)
	if err != nil {
		return err
	}
	defer repository.Close()
	regionMetadata, _ := json.Marshal(map[string]string{"worker_id": settings.workerID, "role": "api"})
	if _, err := repository.HeartbeatRegion(ctx, settings.serviceRegion, regionMetadata); err != nil {
		return err
	}
	if _, err := repository.BootstrapSourceOwnership(ctx, settings.serviceRegion); err != nil {
		return err
	}

	configKey, err := secret.ParseBase64Key(settings.configKey)
	if err != nil {
		return err
	}
	envelope, err := secret.NewStaticEnvelope(settings.configKeyID, configKey)
	clear(configKey)
	if err != nil {
		return err
	}
	apiKey, err := secret.ParseBase64Key(settings.apiKey)
	if err != nil {
		return fmt.Errorf("API key: %w", err)
	}
	sessions, err := controlplane.NewSessionManager(apiKey, strings.HasPrefix(settings.publicURL, "https://"), 7*24*time.Hour)
	if err != nil {
		return err
	}
	cursors, err := controlplane.NewCursorCodec(apiKey)
	sessions.Store = repository
	clear(apiKey)
	if err != nil {
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
	profiledAdapter, err := vendorprofile.New(statusAdapter, awsAdapter, ecosystemAdapter)
	if err != nil {
		return err
	}
	verifier := auth.ServiceVerifier{Repository: repository}

	hub := controlplane.NewEventHub()
	natsConnection, err := nats.Connect(settings.natsURL, nats.Name("statushub-api-"+settings.workerID),
		nats.DisconnectErrHandler(func(_ *nats.Conn, _ error) { hub.SetReady(false) }),
		nats.ReconnectHandler(func(_ *nats.Conn) { hub.SetReady(true) }))
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}
	defer natsConnection.Close()
	_, err = natsConnection.Subscribe("statushub.events.>", func(message *nats.Msg) {
		envelope, decodeErr := bus.DecodeEventEnvelope(message.Data)
		if decodeErr == nil {
			hub.Publish(string(envelope.EventID))
		}
	})
	if err != nil {
		return fmt.Errorf("subscribe live events: %w", err)
	}
	if err := natsConnection.FlushTimeout(5 * time.Second); err != nil {
		return fmt.Errorf("activate live subscription: %w", err)
	}
	hub.SetReady(true)

	notifyTransportConfig := transport.DefaultConfig()
	notifyTransportConfig.DisableRedirects = true
	notifyClient, err := transport.New(notifyTransportConfig)
	if err != nil {
		return err
	}
	defer notifyClient.CloseIdleConnections()
	drivers := map[notify.Channel]notify.ChannelDriver{
		notify.ChannelGenericWebhook: notify.NewGenericWebhook(notifyClient),
		notify.ChannelSlack:          notify.NewSlack(notifyClient),
		notify.ChannelLark:           notify.NewLark(notifyClient),
		notify.ChannelSMTP:           notify.NewSMTP(),
	}
	testWorker, err := controlplane.NewEndpointTestWorker(repository,
		notifierpipeline.EnvelopeConfigDecoder{Opener: envelope}, drivers, settings.workerID+"/endpoint-tests", 10*time.Second)
	if err != nil {
		return err
	}
	workerErrors := make(chan error, 1)
	go repeat(ctx, 100*time.Millisecond, func(ctx context.Context) error {
		_, runErr := testWorker.RunOnce(ctx)
		return runErr
	}, workerErrors)

	application, err := controlplane.NewServer(repository, verifier, profiledAdapter, envelope, sessions, cursors,
		hub.Broker(), controlplane.Config{PublicURL: settings.publicURL, TrustedProxies: settings.trustedProxies, ServiceRegion: settings.serviceRegion, ProbeTimeout: 15 * time.Second,
			RequestTimeout: 15 * time.Second, Logger: logger})
	if err != nil {
		return err
	}
	httpServer := &http.Server{Addr: settings.listenAddress, Handler: application.Handler(),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 2 * time.Minute}
	serverErrors := make(chan error, 1)
	go repeat(ctx, 5*time.Second, application.RunIdentityMail, workerErrors)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()
	logger.Info("management API listening", "address", settings.listenAddress, "public_url", settings.publicURL)
	for {
		select {
		case <-ctx.Done():
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			return errors.Join(ctx.Err(), httpServer.Shutdown(shutdownContext))
		case err := <-workerErrors:
			if err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("endpoint test worker iteration failed", "error", err)
			}
		case err := <-serverErrors:
			return fmt.Errorf("management API server: %w", err)
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
