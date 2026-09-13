package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	store "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/store/postgres"
)

type EventHub struct {
	mutex       sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan string
	ready       atomic.Bool
}

func NewEventHub() *EventHub {
	return &EventHub{subscribers: make(map[uint64]chan string)}
}

func (h *EventHub) SetReady(ready bool) { h.ready.Store(ready) }

func (h *EventHub) Ready() bool { return h != nil && h.ready.Load() }

func (h *EventHub) Publish(eventID string) {
	if h == nil || eventID == "" {
		return
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	for id, subscriber := range h.subscribers {
		select {
		case subscriber <- eventID:
		default:
			close(subscriber)
			delete(h.subscribers, id)
		}
	}
}

func (h *EventHub) Subscribe() (<-chan string, func()) {
	if h == nil {
		channel := make(chan string)
		close(channel)
		return channel, func() {}
	}
	h.mutex.Lock()
	h.nextID++
	id := h.nextID
	channel := make(chan string, 128)
	h.subscribers[id] = channel
	h.mutex.Unlock()
	var once sync.Once
	return channel, func() {
		once.Do(func() {
			h.mutex.Lock()
			if existing, ok := h.subscribers[id]; ok {
				delete(h.subscribers, id)
				close(existing)
			}
			h.mutex.Unlock()
		})
	}
}

func (h *EventHub) Broker() EventBroker {
	return NewEventBroker(h.Subscribe, h.Ready)
}

func (s *Server) handleEventStream(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeProblemStatus(response, request, http.StatusNotImplemented, "streaming_unavailable", "HTTP streaming is unavailable")
		return
	}
	details := requestDetails(request)
	rawCursor := request.Header.Get("Last-Event-ID")
	if rawCursor == "" {
		rawCursor = request.URL.Query().Get("cursor")
	}
	cursor, err := s.cursors.DecodeTime(rawCursor, "events", details.Tenant.ID)
	if err != nil {
		writeProblem(response, request, err)
		return
	}
	if cursor == nil {
		cursor = &store.TimeCursor{Time: time.Now().UTC().Add(-24 * time.Hour), ID: "00000000-0000-0000-0000-000000000000"}
	}
	var live <-chan string
	unsubscribe := func() {}
	if s.broker.subscribe != nil {
		live, unsubscribe = s.broker.subscribe()
	}
	defer unsubscribe()

	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache, no-transform")
	response.Header().Set("Connection", "keep-alive")
	response.Header().Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(response, "retry: 2000\n\n")
	flusher.Flush()

	send := func(event store.EventView) bool {
		if !cursorBefore(*cursor, event.IngestedAt, event.ID) {
			return true
		}
		encodedCursor, encodeErr := s.cursors.EncodeTime("events", details.Tenant.ID, store.TimeCursor{Time: event.IngestedAt, ID: event.ID})
		if encodeErr != nil {
			return false
		}
		data, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			return false
		}
		if _, writeErr := fmt.Fprintf(response, "id: %s\nevent: canonical_event\ndata: %s\n\n", encodedCursor, data); writeErr != nil {
			return false
		}
		flusher.Flush()
		cursor.Time, cursor.ID = event.IngestedAt, event.ID
		return true
	}

	for pages := 0; pages < 25; pages++ {
		items, listErr := s.repository.ListTenantEvents(request.Context(), details.Tenant.ID, cursor, 200)
		if listErr != nil {
			return
		}
		for _, event := range items {
			if !send(event) {
				return
			}
		}
		if len(items) < 200 {
			break
		}
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
			if token, ok := bearerToken(request.Header.Get("Authorization")); ok {
				if _, err := s.verifier.Authenticate(request.Context(), details.Tenant.ID, token); err != nil {
					return
				}
			} else if _, err := s.sessions.Read(request); err != nil {
				return
			}
			if _, err := fmt.Fprintf(response, ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
				return
			}
			flusher.Flush()
		case eventID, open := <-live:
			if !open {
				return
			}
			event, err := s.repository.TenantEvent(request.Context(), details.Tenant.ID, eventID)
			if err != nil {
				continue
			}
			if !send(event) {
				return
			}
		}
	}
}

func cursorBefore(cursor store.TimeCursor, at time.Time, id string) bool {
	return cursor.Time.Before(at) || (cursor.Time.Equal(at) && cursor.ID < id)
}
