// Package secret provides the small encryption boundary used for persisted
// endpoint credentials. Production key-management backends can implement the
// same Sealer/Opener contracts without changing API or notifier code.
package secret

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const envelopeVersion byte = 1

var ErrInvalidCiphertext = errors.New("secret: ciphertext cannot be opened")

type Sealer interface {
	Seal(context.Context, []byte, []byte) ([]byte, error)
	KeyID() string
}

type Opener interface {
	Open(context.Context, []byte, []byte, string) ([]byte, error)
}

// StaticEnvelope is the local-development implementation of envelope
// encryption. The key is supplied at process start and never stored in the
// database. KMS/Vault implementations are intentionally behind the same
// interface.
type StaticEnvelope struct {
	keyID string
	aead  cipher.AEAD
}

func NewStaticEnvelope(keyID string, key []byte) (*StaticEnvelope, error) {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		return nil, errors.New("secret: key ID is required")
	}
	if len(key) != 32 {
		return nil, errors.New("secret: AES-256 key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secret: create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secret: create AEAD: %w", err)
	}
	return &StaticEnvelope{keyID: keyID, aead: aead}, nil
}

func ParseBase64Key(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		decoded, err = base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	}
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("secret: key must be base64-encoded 32 bytes")
	}
	return decoded, nil
}

func (s *StaticEnvelope) KeyID() string {
	if s == nil {
		return ""
	}
	return s.keyID
}

func (s *StaticEnvelope) Seal(ctx context.Context, plaintext, associatedData []byte) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, errors.New("secret: sealer is not initialized")
	}
	if ctx == nil {
		return nil, errors.New("secret: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(plaintext) == 0 {
		return nil, errors.New("secret: plaintext is required")
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("secret: generate nonce: %w", err)
	}
	result := make([]byte, 1+len(nonce), 1+len(nonce)+len(plaintext)+s.aead.Overhead())
	result[0] = envelopeVersion
	copy(result[1:], nonce)
	return s.aead.Seal(result, nonce, plaintext, associatedData), nil
}

func (s *StaticEnvelope) Open(ctx context.Context, ciphertext, associatedData []byte, keyID string) ([]byte, error) {
	if s == nil || s.aead == nil || ctx == nil {
		return nil, ErrInvalidCiphertext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if keyID != s.keyID || len(ciphertext) < 1+s.aead.NonceSize()+s.aead.Overhead() || ciphertext[0] != envelopeVersion {
		return nil, ErrInvalidCiphertext
	}
	nonce := ciphertext[1 : 1+s.aead.NonceSize()]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext[1+s.aead.NonceSize():], associatedData)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}
	return plaintext, nil
}

func EndpointAssociatedData(endpointID string, version int) []byte {
	return []byte(fmt.Sprintf("statushub.endpoint.v1:%s:%d", endpointID, version))
}
