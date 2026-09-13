// Package controlplane implements the tenant-scoped management API, SSE
// stream, OIDC browser session, and the minimal operations UI.
package controlplane

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	store "github.com/Seeridia/StatusHub/internal/store/postgres"
)

var ErrInvalidCursor = errors.New("controlplane: invalid cursor")

type cursorPayload struct {
	Version  int       `json:"v"`
	Kind     string    `json:"k"`
	Tenant   string    `json:"t"`
	Time     time.Time `json:"at,omitempty"`
	ID       string    `json:"id,omitempty"`
	Sequence int64     `json:"seq,omitempty"`
}

type CursorCodec struct {
	key []byte
}

func NewCursorCodec(masterKey []byte) (*CursorCodec, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("controlplane: cursor master key must be 32 bytes")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("statushub.cursor.v1"))
	return &CursorCodec{key: mac.Sum(nil)}, nil
}

func (c *CursorCodec) EncodeTime(kind, tenant string, cursor store.TimeCursor) (string, error) {
	if c == nil || strings.TrimSpace(kind) == "" || strings.TrimSpace(tenant) == "" || cursor.Time.IsZero() || strings.TrimSpace(cursor.ID) == "" {
		return "", ErrInvalidCursor
	}
	return c.encode(cursorPayload{Version: 1, Kind: kind, Tenant: tenant, Time: cursor.Time.UTC(), ID: cursor.ID})
}

func (c *CursorCodec) DecodeTime(value, kind, tenant string) (*store.TimeCursor, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	payload, err := c.decode(value)
	if err != nil || payload.Version != 1 || payload.Kind != kind || payload.Tenant != tenant || payload.Time.IsZero() || payload.ID == "" || payload.Sequence != 0 {
		return nil, ErrInvalidCursor
	}
	return &store.TimeCursor{Time: payload.Time.UTC(), ID: payload.ID}, nil
}

func (c *CursorCodec) EncodeSequence(kind, tenant string, sequence int64) (string, error) {
	if c == nil || kind == "" || tenant == "" || sequence <= 0 {
		return "", ErrInvalidCursor
	}
	return c.encode(cursorPayload{Version: 1, Kind: kind, Tenant: tenant, Sequence: sequence})
}

func (c *CursorCodec) DecodeSequence(value, kind, tenant string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	payload, err := c.decode(value)
	if err != nil || payload.Version != 1 || payload.Kind != kind || payload.Tenant != tenant || payload.Sequence <= 0 || !payload.Time.IsZero() || payload.ID != "" {
		return 0, ErrInvalidCursor
	}
	return payload.Sequence, nil
}

func (c *CursorCodec) encode(payload cursorPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(data)
	signature := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *CursorCodec) decode(value string) (cursorPayload, error) {
	if c == nil || len(value) > 2048 {
		return cursorPayload{}, ErrInvalidCursor
	}
	encodedPayload, encodedSignature, ok := strings.Cut(value, ".")
	if !ok || encodedPayload == "" || encodedSignature == "" || strings.Contains(encodedSignature, ".") {
		return cursorPayload{}, ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(encodedPayload)
	if err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(encodedSignature)
	if err != nil || len(signature) != sha256.Size {
		return cursorPayload{}, ErrInvalidCursor
	}
	mac := hmac.New(sha256.New, c.key)
	_, _ = mac.Write(data)
	if subtle.ConstantTimeCompare(signature, mac.Sum(nil)) != 1 {
		return cursorPayload{}, ErrInvalidCursor
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var payload cursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return cursorPayload{}, ErrInvalidCursor
	}
	return payload, nil
}
