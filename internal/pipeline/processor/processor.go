// Package processor persists reconciled canonical events and their compact
// bus references through the transactional outbox.
package processor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/bus"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

const DefaultSubject = "statusmon.events.normal"

type Ledger interface {
	InsertEventWithOutbox(context.Context, domain.CanonicalEvent, store.OutboxMessage) (bool, error)
}

type Processor struct {
	ledger  Ledger
	subject string
	newID   func() string
}

func New(ledger Ledger, subject string) (*Processor, error) {
	if ledger == nil {
		return nil, errors.New("event processor ledger is required")
	}
	if strings.TrimSpace(subject) == "" {
		subject = DefaultSubject
	}
	if err := bus.ValidateSubject(subject); err != nil {
		return nil, err
	}
	return &Processor{ledger: ledger, subject: subject, newID: uuid.NewString}, nil
}

type Result struct {
	EventID  domain.CanonicalEventID
	OutboxID string
	Inserted bool
}

func (p *Processor) Prepare(event domain.CanonicalEvent) (store.EventWrite, error) {
	if p == nil || p.ledger == nil {
		return store.EventWrite{}, errors.New("event processor is not initialized")
	}
	if event.ID == "" {
		event.ID = domain.CanonicalEventID(p.newID())
	}
	outboxID := p.newID()
	payload, err := (bus.EventEnvelope{
		Version: bus.EventEnvelopeVersion, EventID: event.ID, SourceID: event.SourceID,
		Kind: event.Kind, EntityKind: event.EntityKind, EntityID: event.EntityID,
		AggregateRevision: event.AggregateRevision, SchemaVersion: event.SchemaVersion,
	}).Marshal()
	if err != nil {
		return store.EventWrite{}, fmt.Errorf("build event envelope: %w", err)
	}
	return store.EventWrite{
		Event:  event,
		Outbox: store.OutboxMessage{ID: outboxID, Subject: p.subject, Payload: payload},
	}, nil
}

func (p *Processor) PrepareAll(events []domain.CanonicalEvent) ([]store.EventWrite, error) {
	writes := make([]store.EventWrite, 0, len(events))
	for index, event := range events {
		write, err := p.Prepare(event)
		if err != nil {
			return nil, fmt.Errorf("prepare event %d: %w", index, err)
		}
		writes = append(writes, write)
	}
	return writes, nil
}

func (p *Processor) Persist(ctx context.Context, event domain.CanonicalEvent) (Result, error) {
	write, err := p.Prepare(event)
	if err != nil {
		return Result{}, err
	}
	inserted, err := p.ledger.InsertEventWithOutbox(ctx, write.Event, write.Outbox)
	if err != nil {
		return Result{}, err
	}
	if !inserted {
		return Result{Inserted: false}, nil
	}
	return Result{EventID: write.Event.ID, OutboxID: write.Outbox.ID, Inserted: true}, nil
}

func (p *Processor) PersistAll(ctx context.Context, events []domain.CanonicalEvent) ([]Result, error) {
	results := make([]Result, 0, len(events))
	for index, event := range events {
		result, err := p.Persist(ctx, event)
		if err != nil {
			return results, fmt.Errorf("persist event %d: %w", index, err)
		}
		results = append(results, result)
	}
	return results, nil
}
