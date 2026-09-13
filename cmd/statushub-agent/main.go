// statushub-agent runs inside a tenant network and performs webhook delivery
// after claiming work over an outbound connection to the Statusmon service.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Seeridia/StatusHub/internal/notify"
	"github.com/Seeridia/StatusHub/internal/privateagent"
)

const agentVersion = "statushub-agent/1"

type config struct {
	serverURL   string
	agentID     string
	token       string
	batchSize   int
	concurrency int
	waitSeconds int
	once        bool
}

type targetConfig struct {
	URL             string `json:"url"`
	Secret          string `json:"secret"`
	MaxPayloadBytes int    `json:"max_payload_bytes"`
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
	flags := flag.NewFlagSet("statushub-agent", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	result := config{}
	flags.StringVar(&result.serverURL, "server-url", os.Getenv("STATUSHUB_SERVER_URL"), "Statusmon public HTTPS base URL")
	flags.StringVar(&result.agentID, "agent-id", os.Getenv("STATUSHUB_AGENT_ID"), "private agent UUID")
	flags.StringVar(&result.token, "token", os.Getenv("STATUSHUB_AGENT_TOKEN"), "private agent bearer token")
	flags.IntVar(&result.batchSize, "batch-size", 16, "claim batch size in [1,100]")
	flags.IntVar(&result.concurrency, "concurrency", 8, "maximum concurrent private deliveries")
	flags.IntVar(&result.waitSeconds, "wait-seconds", 25, "claim long-poll seconds in [0,25]")
	flags.BoolVar(&result.once, "once", false, "claim and process one batch")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(result.agentID) == "" || strings.TrimSpace(result.token) == "" {
		return config{}, errors.New("-server-url, -agent-id, and -token (or corresponding environment variables) are required")
	}
	parsed, err := url.Parse(strings.TrimRight(result.serverURL, "/"))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return config{}, errors.New("-server-url must be an absolute origin URL")
	}
	local := parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "localhost" || parsed.Hostname() == "::1"
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && local) {
		return config{}, errors.New("-server-url must use HTTPS except for loopback development")
	}
	if result.batchSize < 1 || result.batchSize > 100 || result.concurrency < 1 || result.concurrency > result.batchSize || result.waitSeconds < 0 || result.waitSeconds > 25 {
		return config{}, errors.New("invalid batch, concurrency, or wait limits")
	}
	result.serverURL = strings.TrimRight(parsed.String(), "/")
	return result, nil
}

func run(ctx context.Context, args []string) error {
	settings, err := parseConfig(args)
	if err != nil {
		return err
	}
	client := &http.Client{Transport: &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2: true, MaxIdleConns: 64, MaxIdleConnsPerHost: 32,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 35 * time.Second,
	}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	driver := notify.NewGenericWebhook(client)
	for {
		claimed, err := claim(ctx, client, settings)
		if err != nil {
			return err
		}
		if err := processBatch(ctx, client, driver, settings, claimed.Deliveries); err != nil {
			return err
		}
		if settings.once {
			return nil
		}
	}
}

func claim(ctx context.Context, client *http.Client, settings config) (privateagent.ClaimResponse, error) {
	body, _ := json.Marshal(privateagent.ClaimRequest{Limit: settings.batchSize, WaitSeconds: settings.waitSeconds})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		settings.serverURL+"/v1/private-agents/"+settings.agentID+"/claim", bytes.NewReader(body))
	if err != nil {
		return privateagent.ClaimResponse{}, err
	}
	decorate(request, settings)
	response, err := client.Do(request)
	if err != nil {
		return privateagent.ClaimResponse{}, fmt.Errorf("claim private deliveries: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return privateagent.ClaimResponse{}, fmt.Errorf("claim private deliveries: HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	var result privateagent.ClaimResponse
	if err := decoder.Decode(&result); err != nil {
		return privateagent.ClaimResponse{}, fmt.Errorf("decode private deliveries: %w", err)
	}
	return result, nil
}

func processBatch(ctx context.Context, client *http.Client, driver *notify.GenericWebhook, settings config, items []privateagent.WorkItem) error {
	semaphore := make(chan struct{}, settings.concurrency)
	errorsChannel := make(chan error, len(items))
	var group sync.WaitGroup
	for _, item := range items {
		item := item
		group.Add(1)
		go func() {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				errorsChannel <- ctx.Err()
				return
			}
			if err := deliver(ctx, client, driver, settings, item); err != nil {
				errorsChannel <- err
			}
		}()
	}
	group.Wait()
	close(errorsChannel)
	var failures []error
	for err := range errorsChannel {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func deliver(ctx context.Context, client *http.Client, driver *notify.GenericWebhook, settings config, item privateagent.WorkItem) error {
	var target targetConfig
	if err := json.Unmarshal(item.EndpointConfig, &target); err != nil {
		return complete(ctx, client, settings, privateagent.CompleteRequest{DeliveryID: item.DeliveryID,
			AttemptNumber: item.AttemptNumber, LeaseToken: item.LeaseToken, Outcome: "permanent", ErrorSummary: "invalid private endpoint config"})
	}
	endpoint := notify.Endpoint{ID: item.EndpointID, Channel: notify.ChannelGenericWebhook, URL: target.URL,
		KeyID: item.KeyID, Secret: []byte(target.Secret), MaxPayloadBytes: target.MaxPayloadBytes}
	payload, err := driver.Render(ctx, item.Event, endpoint)
	if err == nil {
		var receipt notify.Receipt
		receipt, err = driver.Send(ctx, notify.Delivery{ID: item.DeliveryID, EventID: item.Event.ID, Endpoint: endpoint}, payload)
		if err == nil {
			return complete(ctx, client, settings, privateagent.CompleteRequest{DeliveryID: item.DeliveryID,
				AttemptNumber: item.AttemptNumber, LeaseToken: item.LeaseToken, Outcome: "accepted",
				HTTPStatus: receipt.HTTPStatus, ProviderMessageID: receipt.ProviderMessageID})
		}
	}
	decision := driver.Classify(err)
	outcome := "permanent"
	if decision.Class == notify.ErrorClassRetryable {
		outcome = "retryable"
	}
	httpStatus := 0
	var deliveryError *notify.Error
	if errors.As(err, &deliveryError) {
		httpStatus = deliveryError.StatusCode
	}
	summary := err.Error()
	if len(summary) > 1024 {
		summary = summary[:1024]
	}
	return complete(ctx, client, settings, privateagent.CompleteRequest{DeliveryID: item.DeliveryID,
		AttemptNumber: item.AttemptNumber, LeaseToken: item.LeaseToken, Outcome: outcome,
		HTTPStatus: httpStatus, ErrorSummary: summary, RetryAfterSeconds: int(decision.RetryAfter / time.Second)})
}

func complete(ctx context.Context, client *http.Client, settings config, completion privateagent.CompleteRequest) error {
	body, _ := json.Marshal(completion)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		settings.serverURL+"/v1/private-agents/"+settings.agentID+"/complete", bytes.NewReader(body))
	if err != nil {
		return err
	}
	decorate(request, settings)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("complete private delivery: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("complete private delivery: HTTP %d", response.StatusCode)
	}
	return nil
}

func decorate(request *http.Request, settings config) {
	request.Header.Set("Authorization", "Bearer "+settings.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Statusmon-Agent-Version", agentVersion)
}
