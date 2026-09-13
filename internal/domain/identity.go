package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// The distinct types prevent an observation hash or source-event dedup key
// from accidentally being used as the immutable canonical event identifier.
type ObservationKey string
type SourceEventKey string
type CanonicalEventID string

type ObservationIdentity struct {
	SourceID     string
	Endpoint     string
	ETag         string
	LastModified *time.Time
	RawHash      string
}

func (i ObservationIdentity) Key() (ObservationKey, error) {
	if strings.TrimSpace(i.SourceID) == "" {
		return "", errors.New("observation identity: source ID is required")
	}
	if strings.TrimSpace(i.Endpoint) == "" {
		return "", errors.New("observation identity: endpoint is required")
	}
	if i.ETag == "" && i.LastModified == nil && i.RawHash == "" {
		return "", errors.New("observation identity: a validator or raw hash is required")
	}

	lastModified := ""
	if i.LastModified != nil {
		lastModified = i.LastModified.UTC().Format(time.RFC3339Nano)
	}
	return ObservationKey(hashParts("observation/v1", i.SourceID, i.Endpoint, i.ETag, lastModified, i.RawHash)), nil
}

type SourceEventIdentity struct {
	SourceID               string
	Provider               string
	PageID                 string
	Kind                   EventKind
	EntityKind             EntityKind
	EntityID               string
	UpstreamEventID        string
	SourceUpdatedAt        *time.Time
	NormalizerVersion      string
	SemanticProjectionHash string
}

func (i SourceEventIdentity) Key() (SourceEventKey, error) {
	if strings.TrimSpace(i.SourceID) == "" {
		return "", errors.New("source-event identity: source ID is required")
	}
	if i.UpstreamEventID != "" {
		return SourceEventKey(hashParts("source-event/upstream/v1", i.SourceID, i.UpstreamEventID)), nil
	}
	if !i.Kind.Valid() {
		return "", errors.New("source-event identity: valid event kind is required")
	}
	if !i.EntityKind.Valid() || strings.TrimSpace(i.EntityID) == "" {
		return "", errors.New("source-event identity: valid entity kind and entity ID are required")
	}
	if strings.TrimSpace(i.NormalizerVersion) == "" || strings.TrimSpace(i.SemanticProjectionHash) == "" {
		return "", errors.New("source-event identity: normalizer version and semantic projection hash are required")
	}

	updatedAt := ""
	if i.SourceUpdatedAt != nil {
		updatedAt = i.SourceUpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return SourceEventKey(hashParts(
		"source-event/semantic/v1",
		i.SourceID,
		i.Provider,
		i.PageID,
		string(i.Kind),
		string(i.EntityKind),
		i.EntityID,
		updatedAt,
		i.NormalizerVersion,
		i.SemanticProjectionHash,
	)), nil
}

// CanonicalEventIdentity is allocated by the event ledger. Its ID must remain
// stable across publication retries; Revision is monotonic per aggregate.
type CanonicalEventIdentity struct {
	ID       CanonicalEventID `json:"id"`
	Revision uint64           `json:"revision"`
}

func hashParts(namespace string, parts ...string) string {
	h := sha256.New()
	writeHashPart(h, namespace)
	for _, part := range parts {
		writeHashPart(h, part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func writeHashPart(w hashWriter, part string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(part)))
	_, _ = w.Write(length[:])
	_, _ = w.Write([]byte(part))
}
