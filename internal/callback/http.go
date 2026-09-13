package callback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

const maximumCallbackBody = 256 << 10

type Repository interface {
	LoadEndpointCallbackConfig(context.Context, string) (store.EndpointCallbackConfig, error)
	ApplyProviderCallback(context.Context, store.ProviderCallback) (bool, bool, error)
}

type Handler struct {
	repository Repository
	sns        *SNSVerifier
	now        func() time.Time
}

func NewHandler(repository Repository, verifier *SNSVerifier) (*Handler, error) {
	if repository == nil || verifier == nil {
		return nil, errors.New("callback: repository and SNS verifier are required")
	}
	return &Handler{repository: repository, sns: verifier, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (h *Handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "v1" || parts[1] != "provider-callbacks" {
		http.NotFound(response, request)
		return
	}
	provider, endpointID := parts[2], parts[3]
	config, err := h.repository.LoadEndpointCallbackConfig(request.Context(), endpointID)
	if err != nil || !config.Enabled {
		http.Error(response, "endpoint not found", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maximumCallbackBody))
	if err != nil {
		http.Error(response, "invalid body", http.StatusBadRequest)
		return
	}
	var event Event
	switch provider {
	case "ses":
		if config.Channel != "email_ses" {
			http.Error(response, "channel mismatch", http.StatusBadRequest)
			return
		}
		envelope, verifyErr := h.sns.Verify(request.Context(), body)
		if verifyErr != nil {
			http.Error(response, "invalid signature", http.StatusUnauthorized)
			return
		}
		var endpoint struct {
			SNSTopicARN string `json:"sns_topic_arn"`
		}
		if json.Unmarshal(config.EncryptedConfig, &endpoint) != nil || endpoint.SNSTopicARN == "" || endpoint.SNSTopicARN != envelope.TopicARN {
			http.Error(response, "topic not allowed", http.StatusUnauthorized)
			return
		}
		if envelope.Type == "SubscriptionConfirmation" {
			if err := h.sns.ConfirmSubscription(request.Context(), envelope); err != nil {
				http.Error(response, "subscription confirmation failed", http.StatusBadGateway)
				return
			}
			response.WriteHeader(http.StatusNoContent)
			return
		}
		if envelope.Type != "Notification" {
			http.Error(response, "unsupported SNS message type", http.StatusBadRequest)
			return
		}
		event, err = DecodeSES(body)
	case "twilio":
		if config.Channel != "twilio_sms" {
			http.Error(response, "channel mismatch", http.StatusBadRequest)
			return
		}
		var endpoint struct {
			Secret      string `json:"secret"`
			CallbackURL string `json:"callback_url"`
		}
		if json.Unmarshal(config.EncryptedConfig, &endpoint) != nil || endpoint.Secret == "" || endpoint.CallbackURL == "" {
			http.Error(response, "callback is not configured", http.StatusUnauthorized)
			return
		}
		form, parseErr := url.ParseQuery(string(body))
		if parseErr != nil || !VerifyTwilioSignature(endpoint.Secret, endpoint.CallbackURL, request.Header.Get("X-Twilio-Signature"), form) {
			http.Error(response, "invalid signature", http.StatusUnauthorized)
			return
		}
		event, err = DecodeTwilio(form)
	default:
		http.NotFound(response, request)
		return
	}
	if err != nil {
		http.Error(response, "invalid provider event", http.StatusBadRequest)
		return
	}
	_, _, err = h.repository.ApplyProviderCallback(request.Context(), ToStore(endpointID, event, body, h.now()))
	if err != nil {
		http.Error(response, "callback persistence failed", http.StatusInternalServerError)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
