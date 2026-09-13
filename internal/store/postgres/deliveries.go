package postgres

import (
	"context"
	"fmt"
	"strings"
)

const ensureDeliverySQL = `
INSERT INTO deliveries (
    id,
    event_id,
    subscription_id,
    endpoint_id,
    template_version,
    status,
    priority,
    next_attempt_at,
    expires_at
)
VALUES (
    COALESCE($1::uuid, gen_random_uuid()),
    $2,
    $3,
    $4,
    $5,
    COALESCE(NULLIF($6, ''), 'queued'),
    $7,
    COALESCE($8, statement_timestamp()),
    $9
)
ON CONFLICT (event_id, subscription_id, endpoint_id, template_version)
DO NOTHING`

// EnsureDelivery inserts a logical delivery once. Concurrent calls and
// at-least-once consumer redelivery are absorbed by the database unique
// constraint; inserted is false when that logical delivery already exists.
func (s *Store) EnsureDelivery(ctx context.Context, delivery Delivery) (inserted bool, err error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	if err := validateDelivery(delivery); err != nil {
		return false, err
	}

	var id *string
	if strings.TrimSpace(delivery.ID) != "" {
		id = &delivery.ID
	}
	var nextAttemptAt any
	if !delivery.NextAttemptAt.IsZero() {
		nextAttemptAt = delivery.NextAttemptAt
	}
	tag, err := s.db.Exec(
		ctx,
		ensureDeliverySQL,
		id,
		delivery.EventID,
		delivery.SubscriptionID,
		delivery.EndpointID,
		delivery.TemplateVersion,
		delivery.Status,
		delivery.Priority,
		nextAttemptAt,
		delivery.ExpiresAt,
	)
	if err != nil {
		return false, fmt.Errorf("postgres store: ensure delivery: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func validateDelivery(delivery Delivery) error {
	if err := required(delivery.EventID, "delivery event ID"); err != nil {
		return err
	}
	if err := required(delivery.SubscriptionID, "delivery subscription ID"); err != nil {
		return err
	}
	if err := required(delivery.EndpointID, "delivery endpoint ID"); err != nil {
		return err
	}
	if delivery.TemplateVersion <= 0 {
		return invalid("delivery template version must be greater than zero")
	}
	return nil
}
