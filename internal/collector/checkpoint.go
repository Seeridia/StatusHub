package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/reconcile"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/scheduler"
)

const checkpointVersion = 1

type ResourceCheckpoint struct {
	LastSuccessAt  *time.Time `json:"last_success_at,omitempty"`
	ScheduleReason string     `json:"schedule_reason,omitempty"`
	NextPollAt     time.Time  `json:"next_poll_at"`
	ETag           string     `json:"etag,omitempty"`
	LastModified   *time.Time `json:"last_modified,omitempty"`
	SchemaHash     string     `json:"schema_hash,omitempty"`
}

// Checkpoint is the source-scoped durable collector state stored behind the
// source lease fencing token. It contains no raw response bodies.
type Checkpoint struct {
	Version      int                                        `json:"version"`
	Capabilities domain.Capabilities                        `json:"capabilities"`
	Cadence      scheduler.State                            `json:"cadence"`
	Resources    map[domain.ResourceKind]ResourceCheckpoint `json:"resources"`
	Reconcile    reconcile.State                            `json:"reconcile"`
}

func DecodeCheckpoint(value *string, sourceID string) (Checkpoint, error) {
	if value == nil || *value == "" {
		return newCheckpoint(sourceID), nil
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal([]byte(*value), &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("collector: decode checkpoint: %w", err)
	}
	if checkpoint.Version != checkpointVersion {
		return Checkpoint{}, fmt.Errorf("collector: unsupported checkpoint version %d", checkpoint.Version)
	}
	if checkpoint.Reconcile.SourceID != "" && checkpoint.Reconcile.SourceID != sourceID {
		return Checkpoint{}, errors.New("collector: checkpoint belongs to another source")
	}
	if checkpoint.Resources == nil {
		checkpoint.Resources = make(map[domain.ResourceKind]ResourceCheckpoint)
	}
	return checkpoint, nil
}

func EncodeCheckpoint(checkpoint Checkpoint) (string, error) {
	checkpoint.Version = checkpointVersion
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return "", fmt.Errorf("collector: encode checkpoint: %w", err)
	}
	return string(data), nil
}

func newCheckpoint(sourceID string) Checkpoint {
	return Checkpoint{
		Version:   checkpointVersion,
		Resources: make(map[domain.ResourceKind]ResourceCheckpoint),
		Reconcile: reconcile.State{SourceID: sourceID},
	}
}

func resourceStates(checkpoint Checkpoint) []scheduler.ResourceState {
	states := make([]scheduler.ResourceState, 0, len(checkpoint.Resources))
	for resource, state := range checkpoint.Resources {
		states = append(states, scheduler.ResourceState{Resource: resource, NextPollAt: state.NextPollAt})
	}
	return states
}

func nextResourceDeadline(resources map[domain.ResourceKind]ResourceCheckpoint, fallback time.Time) time.Time {
	next := time.Time{}
	for _, state := range resources {
		if state.NextPollAt.IsZero() {
			continue
		}
		if next.IsZero() || state.NextPollAt.Before(next) {
			next = state.NextPollAt
		}
	}
	if next.IsZero() {
		return fallback
	}
	return next
}
