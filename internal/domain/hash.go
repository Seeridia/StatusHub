package domain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// SemanticHash returns a deterministic digest for a semantic projection.
// It canonicalizes object key order (via encoding/json), normalizes RFC3339
// timestamps to UTC, rejects trailing JSON, and includes normalizerVersion in
// a length-delimited domain-separated namespace.
func SemanticHash(normalizerVersion string, projection any) (string, error) {
	if strings.TrimSpace(normalizerVersion) == "" {
		return "", errors.New("semantic hash: normalizer version is required")
	}

	canonical, err := CanonicalJSON(projection)
	if err != nil {
		return "", fmt.Errorf("semantic hash: %w", err)
	}

	h := sha256.New()
	writeHashPart(h, "semantic-projection/v1")
	writeHashPart(h, normalizerVersion)
	_, _ = h.Write(canonical)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CanonicalJSON turns any JSON-marshalable projection into deterministic JSON.
// Transient fields are not guessed or removed here: callers must construct a
// semantic projection that omits collection time and transport headers.
func CanonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal projection: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode projection: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decode projection: trailing JSON value")
		}
		return nil, fmt.Errorf("decode projection tail: %w", err)
	}

	decoded = normalizeJSONTimes(decoded)
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, fmt.Errorf("marshal canonical projection: %w", err)
	}
	return canonical, nil
}

func normalizeJSONTimes(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = normalizeJSONTimes(child)
		}
		return typed
	case []any:
		for index, child := range typed {
			typed[index] = normalizeJSONTimes(child)
		}
		return typed
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err == nil {
			return parsed.UTC().Format(time.RFC3339Nano)
		}
		return typed
	default:
		return value
	}
}
