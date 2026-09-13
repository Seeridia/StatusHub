package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func (s *Store) LoadEndpointCallbackConfig(ctx context.Context, endpointID string) (EndpointCallbackConfig, error) {
	if err := s.ready(); err != nil {
		return EndpointCallbackConfig{}, err
	}
	if strings.TrimSpace(endpointID) == "" {
		return EndpointCallbackConfig{}, invalid("endpoint ID is required")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return EndpointCallbackConfig{}, fmt.Errorf("postgres store: load callback endpoint: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var result EndpointCallbackConfig
	err = tx.QueryRow(ctx, `SELECT id, channel, encrypted_config, enabled FROM endpoints WHERE id=$1`, endpointID).
		Scan(&result.ID, &result.Channel, &result.EncryptedConfig, &result.Enabled)
	if err != nil {
		return EndpointCallbackConfig{}, fmt.Errorf("postgres store: load callback endpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return EndpointCallbackConfig{}, fmt.Errorf("postgres store: load callback endpoint: commit: %w", err)
	}
	return result, nil
}

func (s *Store) ApplyProviderCallback(ctx context.Context, callback ProviderCallback) (bool, bool, error) {
	if err := s.ready(); err != nil {
		return false, false, err
	}
	if strings.TrimSpace(callback.EndpointID) == "" || strings.TrimSpace(callback.ProviderEventID) == "" ||
		len(callback.RawBodySHA256) != 32 || len(callback.Payload) == 0 {
		return false, false, invalid("provider callback identity, hash, and payload are required")
	}
	if callback.DeliveryStatus != "accepted" && callback.DeliveryStatus != "delivered" &&
		callback.DeliveryStatus != "failed" && callback.DeliveryStatus != "suppressed" {
		return false, false, invalid("provider callback delivery status is invalid")
	}
	if callback.ID == "" {
		callback.ID = uuid.NewString()
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, false, fmt.Errorf("postgres store: apply callback: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var insertedID string
	err = tx.QueryRow(ctx, `
INSERT INTO provider_callbacks (
    id, endpoint_id, delivery_id, provider_event_id, provider_message_id,
    received_at, raw_body_sha256, payload
) VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8)
ON CONFLICT (endpoint_id, provider_event_id) DO NOTHING
RETURNING id`, callback.ID, callback.EndpointID, callback.DeliveryID, callback.ProviderEventID,
		callback.ProviderMessageID, callback.ReceivedAt, callback.RawBodySHA256, callback.Payload).Scan(&insertedID)
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return false, false, fmt.Errorf("postgres store: commit duplicate callback: %w", commitErr)
		}
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("postgres store: insert callback: %w", err)
	}
	var tag pgconn.CommandTag
	if callback.DeliveryID != nil {
		tag, err = tx.Exec(ctx, `
UPDATE deliveries SET status=$3,
    delivered_at=CASE WHEN $3='delivered' THEN $4 ELSE delivered_at END,
    updated_at=statement_timestamp()
WHERE id=$1 AND endpoint_id=$2
  AND CASE status WHEN 'suppressed' THEN 4 WHEN 'failed' THEN 3 WHEN 'delivered' THEN 2 WHEN 'accepted' THEN 1 ELSE 0 END
      <= CASE $3 WHEN 'suppressed' THEN 4 WHEN 'failed' THEN 3 WHEN 'delivered' THEN 2 WHEN 'accepted' THEN 1 ELSE 0 END`, *callback.DeliveryID, callback.EndpointID, callback.DeliveryStatus, callback.ReceivedAt)
	} else {
		tag, err = tx.Exec(ctx, `
UPDATE deliveries SET status=$3,
    delivered_at=CASE WHEN $3='delivered' THEN $4 ELSE delivered_at END,
    updated_at=statement_timestamp()
WHERE endpoint_id=$1 AND provider_message_id=$2
  AND CASE status WHEN 'suppressed' THEN 4 WHEN 'failed' THEN 3 WHEN 'delivered' THEN 2 WHEN 'accepted' THEN 1 ELSE 0 END
      <= CASE $3 WHEN 'suppressed' THEN 4 WHEN 'failed' THEN 3 WHEN 'delivered' THEN 2 WHEN 'accepted' THEN 1 ELSE 0 END`, callback.EndpointID, callback.ProviderMessageID, callback.DeliveryStatus, callback.ReceivedAt)
	}
	if err != nil {
		return false, false, fmt.Errorf("postgres store: update delivery from callback: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("postgres store: apply callback: commit: %w", err)
	}
	return true, tag.RowsAffected() > 0, nil
}
