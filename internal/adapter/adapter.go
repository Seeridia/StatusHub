// Package adapter defines the narrow boundary implemented by status engines.
package adapter

import (
	"context"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

type Adapter interface {
	Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error)
	Fetch(ctx context.Context, req domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error)
	DecodeWebhook(ctx context.Context, req domain.WebhookRequest) ([]domain.SourceEvent, error)
}
