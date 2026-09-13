// Package shadow runs candidate adapters off the response path. Candidate
// output is persisted only as comparison evidence and can never reach
// reconciliation before an explicit, policy-gated promotion.
package shadow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	adaptercontract "github.com/vendor-status-monitoring/vendor-status-monitoring/internal/adapter"
	"github.com/vendor-status-monitoring/vendor-status-monitoring/internal/domain"
)

type Rollout struct {
	ID                      string  `json:"id"`
	SourceID                string  `json:"source_id"`
	CandidateAdapterName    string  `json:"candidate_adapter_name"`
	CandidateAdapterVersion string  `json:"candidate_adapter_version"`
	SampleRate              float64 `json:"sample_rate"`
	MinimumSamples          int     `json:"minimum_samples"`
	MaximumMismatchRate     float64 `json:"maximum_mismatch_rate"`
	MaximumErrorRate        float64 `json:"maximum_error_rate"`
}

type Comparison struct {
	ID                string
	RolloutID         string
	SourceID          string
	ResourceKind      domain.ResourceKind
	ObservedAt        time.Time
	PrimaryDigest     []byte
	CandidateDigest   []byte
	Equivalent        bool
	CandidateError    string
	PrimaryDuration   time.Duration
	CandidateDuration time.Duration
	Difference        json.RawMessage
}

type Repository interface {
	ActiveAdapterRollout(context.Context, string) (Rollout, bool, error)
	RecordAdapterComparison(context.Context, Comparison) error
}

type Config struct {
	Workers       int
	QueueSize     int
	FetchTimeout  time.Duration
	ControlTTL    time.Duration
	RecordTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{Workers: 2, QueueSize: 256, FetchTimeout: 20 * time.Second, ControlTTL: 30 * time.Second, RecordTimeout: 5 * time.Second}
}

type Adapter struct {
	primary      adaptercontract.Adapter
	candidates   map[string]adaptercontract.Adapter
	repository   Repository
	config       Config
	jobs         chan job
	stop         chan struct{}
	done         chan struct{}
	closeOnce    sync.Once
	cacheMutex   sync.RWMutex
	cache        map[string]cachedRollout
	refreshing   map[string]bool
	refreshSlots chan struct{}
	workers      sync.WaitGroup
	refreshes    sync.WaitGroup
	closed       bool
}

type cachedRollout struct {
	rollout   Rollout
	found     bool
	expiresAt time.Time
}

type job struct {
	rollout         Rollout
	request         domain.FetchRequest
	primary         domain.Snapshot
	primaryDuration time.Duration
}

func New(primary adaptercontract.Adapter, candidates map[string]adaptercontract.Adapter, repository Repository, config Config) (*Adapter, error) {
	if primary == nil || repository == nil || config.Workers <= 0 || config.QueueSize <= 0 || config.FetchTimeout <= 0 || config.ControlTTL <= 0 || config.RecordTimeout <= 0 {
		return nil, errors.New("shadow adapter: invalid dependencies or configuration")
	}
	copyCandidates := make(map[string]adaptercontract.Adapter, len(candidates))
	for key, candidate := range candidates {
		if strings.TrimSpace(key) == "" || candidate == nil {
			return nil, errors.New("shadow adapter: candidate key and adapter are required")
		}
		copyCandidates[key] = candidate
	}
	result := &Adapter{primary: primary, candidates: copyCandidates, repository: repository, config: config,
		jobs: make(chan job, config.QueueSize), stop: make(chan struct{}), done: make(chan struct{}),
		cache: make(map[string]cachedRollout), refreshing: make(map[string]bool), refreshSlots: make(chan struct{}, config.Workers)}
	for index := 0; index < config.Workers; index++ {
		result.workers.Add(1)
		go result.runWorker()
	}
	return result, nil
}

func (adapter *Adapter) Probe(ctx context.Context, target domain.Target) (domain.Capabilities, error) {
	return adapter.primary.Probe(ctx, target)
}

func (adapter *Adapter) Fetch(ctx context.Context, request domain.FetchRequest) (domain.Snapshot, domain.FetchMeta, error) {
	started := time.Now()
	snapshot, metadata, err := adapter.primary.Fetch(ctx, request)
	primaryDuration := time.Since(started)
	if err != nil {
		return snapshot, metadata, err
	}
	if metadata.NotModified {
		return snapshot, metadata, nil
	}
	rollout, found := adapter.cached(request.Source.ID)
	if !found {
		adapter.refresh(request.Source.ID)
		return snapshot, metadata, nil
	}
	if shouldSample(rollout.SampleRate, request.Source.ID, request.ResourceKind, metadata.ObservedAt) {
		select {
		case adapter.jobs <- job{rollout: rollout, request: request, primary: snapshot, primaryDuration: primaryDuration}:
		default:
		}
	}
	return snapshot, metadata, nil
}

func (adapter *Adapter) DecodeWebhook(ctx context.Context, request domain.WebhookRequest) ([]domain.SourceEvent, error) {
	return adapter.primary.DecodeWebhook(ctx, request)
}

func (adapter *Adapter) Close(ctx context.Context) error {
	adapter.closeOnce.Do(func() {
		adapter.cacheMutex.Lock()
		adapter.closed = true
		adapter.cacheMutex.Unlock()
		close(adapter.stop)
		go func() {
			adapter.workers.Wait()
			adapter.refreshes.Wait()
			close(adapter.done)
		}()
	})
	select {
	case <-adapter.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (adapter *Adapter) cached(sourceID string) (Rollout, bool) {
	now := time.Now()
	adapter.cacheMutex.RLock()
	entry, exists := adapter.cache[sourceID]
	adapter.cacheMutex.RUnlock()
	if !exists || !now.Before(entry.expiresAt) {
		adapter.refresh(sourceID)
		return Rollout{}, false
	}
	return entry.rollout, entry.found
}

func (adapter *Adapter) refresh(sourceID string) {
	if strings.TrimSpace(sourceID) == "" {
		return
	}
	adapter.cacheMutex.Lock()
	if adapter.closed || adapter.refreshing[sourceID] {
		adapter.cacheMutex.Unlock()
		return
	}
	adapter.refreshing[sourceID] = true
	adapter.refreshes.Add(1)
	adapter.cacheMutex.Unlock()
	select {
	case adapter.refreshSlots <- struct{}{}:
	case <-adapter.stop:
		adapter.cacheMutex.Lock()
		delete(adapter.refreshing, sourceID)
		adapter.cacheMutex.Unlock()
		adapter.refreshes.Done()
		return
	default:
		adapter.cacheMutex.Lock()
		delete(adapter.refreshing, sourceID)
		adapter.cacheMutex.Unlock()
		adapter.refreshes.Done()
		return
	}
	go func() {
		defer adapter.refreshes.Done()
		defer func() { <-adapter.refreshSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), adapter.config.RecordTimeout)
		defer cancel()
		rollout, found, err := adapter.repository.ActiveAdapterRollout(ctx, sourceID)
		adapter.cacheMutex.Lock()
		delete(adapter.refreshing, sourceID)
		if err == nil {
			adapter.cache[sourceID] = cachedRollout{rollout: rollout, found: found, expiresAt: time.Now().Add(adapter.config.ControlTTL)}
		}
		adapter.cacheMutex.Unlock()
	}()
}

func (adapter *Adapter) runWorker() {
	defer adapter.workers.Done()
	for {
		select {
		case <-adapter.stop:
			return
		case work := <-adapter.jobs:
			adapter.compare(work)
		}
	}
}

func (adapter *Adapter) compare(work job) {
	candidate := adapter.candidates[candidateKey(work.rollout.CandidateAdapterName, work.rollout.CandidateAdapterVersion)]
	if candidate == nil {
		candidate = adapter.candidates[work.rollout.CandidateAdapterName]
	}
	comparison := Comparison{RolloutID: work.rollout.ID, SourceID: work.rollout.SourceID,
		ResourceKind: work.request.ResourceKind, ObservedAt: time.Now().UTC(), PrimaryDuration: work.primaryDuration}
	primaryDigest, primaryProjection, err := snapshotDigest(work.primary)
	if err != nil {
		return
	}
	comparison.PrimaryDigest = primaryDigest
	if candidate == nil {
		comparison.CandidateError = "candidate adapter is not registered"
		comparison.Difference = differenceJSON(primaryProjection, nil)
		adapter.record(comparison)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), adapter.config.FetchTimeout)
	work.request.Source.Provider = work.rollout.CandidateAdapterName
	work.request.Target.Provider = work.rollout.CandidateAdapterName
	started := time.Now()
	candidateSnapshot, _, candidateErr := candidate.Fetch(ctx, work.request)
	comparison.CandidateDuration = time.Since(started)
	cancel()
	if candidateErr != nil {
		comparison.CandidateError = truncate(candidateErr.Error(), 1024)
		comparison.Difference = differenceJSON(primaryProjection, nil)
		adapter.record(comparison)
		return
	}
	candidateDigest, candidateProjection, err := snapshotDigest(candidateSnapshot)
	if err != nil {
		comparison.CandidateError = truncate(err.Error(), 1024)
		comparison.Difference = differenceJSON(primaryProjection, nil)
		adapter.record(comparison)
		return
	}
	comparison.CandidateDigest = candidateDigest
	comparison.Equivalent = bytes.Equal(primaryDigest, candidateDigest)
	if !comparison.Equivalent {
		comparison.Difference = differenceJSON(primaryProjection, candidateProjection)
	} else {
		comparison.Difference = json.RawMessage(`{}`)
	}
	adapter.record(comparison)
}

func (adapter *Adapter) record(comparison Comparison) {
	ctx, cancel := context.WithTimeout(context.Background(), adapter.config.RecordTimeout)
	defer cancel()
	_ = adapter.repository.RecordAdapterComparison(ctx, comparison)
}

func candidateKey(name, version string) string { return name + "@" + version }

func shouldSample(rate float64, sourceID string, resource domain.ResourceKind, observedAt time.Time) bool {
	if rate >= 1 {
		return true
	}
	if rate <= 0 {
		return false
	}
	digest := sha256.Sum256([]byte(sourceID + "\x00" + string(resource) + "\x00" + observedAt.UTC().Format(time.RFC3339Nano)))
	value := binary.BigEndian.Uint64(digest[:8])
	return float64(value)/float64(^uint64(0)) < rate
}

type semanticSnapshot struct {
	ResourceKind     domain.ResourceKind    `json:"resource_kind"`
	OverallStatus    domain.ComponentStatus `json:"overall_status"`
	ComputedStatus   domain.ComponentStatus `json:"computed_status"`
	Components       []domain.Component     `json:"components,omitempty"`
	Incidents        []domain.Incident      `json:"incidents,omitempty"`
	Completeness     domain.Completeness    `json:"completeness"`
	AuthoritativeFor []domain.ResourceKind  `json:"authoritative_for,omitempty"`
	SourceUpdatedAt  *time.Time             `json:"source_updated_at,omitempty"`
}

func snapshotDigest(snapshot domain.Snapshot) ([]byte, json.RawMessage, error) {
	projection := semanticSnapshot{ResourceKind: snapshot.ResourceKind, OverallStatus: snapshot.OverallStatus,
		ComputedStatus: snapshot.ComputedStatus, Components: append([]domain.Component(nil), snapshot.Components...),
		Incidents: append([]domain.Incident(nil), snapshot.Incidents...), Completeness: snapshot.Completeness,
		AuthoritativeFor: append([]domain.ResourceKind(nil), snapshot.AuthoritativeFor...), SourceUpdatedAt: snapshot.SourceUpdatedAt}
	sort.Slice(projection.Components, func(i, j int) bool { return projection.Components[i].ID < projection.Components[j].ID })
	sort.Slice(projection.Incidents, func(i, j int) bool { return projection.Incidents[i].ID < projection.Incidents[j].ID })
	for index := range projection.Incidents {
		projection.Incidents[index].ComponentIDs = append([]string(nil), projection.Incidents[index].ComponentIDs...)
		sort.Strings(projection.Incidents[index].ComponentIDs)
		projection.Incidents[index].Updates = append([]domain.IncidentUpdate(nil), projection.Incidents[index].Updates...)
		sort.Slice(projection.Incidents[index].Updates, func(i, j int) bool {
			return projection.Incidents[index].Updates[i].ID < projection.Incidents[index].Updates[j].ID
		})
	}
	sort.Slice(projection.AuthoritativeFor, func(i, j int) bool { return projection.AuthoritativeFor[i] < projection.AuthoritativeFor[j] })
	canonical, err := domain.CanonicalJSON(projection)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(canonical)
	return digest[:], canonical, nil
}

func differenceJSON(primary, candidate json.RawMessage) json.RawMessage {
	result, err := json.Marshal(map[string]any{
		"primary_sha256":   hex.EncodeToString(hashBytes(primary)),
		"candidate_sha256": hex.EncodeToString(hashBytes(candidate)),
	})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return result
}

func hashBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	digest := sha256.Sum256(value)
	return digest[:]
}

func truncate(value string, maximum int) string {
	if len(value) > maximum {
		return value[:maximum]
	}
	return value
}

var _ adaptercontract.Adapter = (*Adapter)(nil)
