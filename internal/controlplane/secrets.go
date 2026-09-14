package controlplane

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Seeridia/StatusHub/internal/auth"
	"io"
	"strings"
)

const maximumCookieSize = 16384

type cookieCodec struct {
	aead cipher.AEAD
}

func newCookieCodec(masterKey []byte) (*cookieCodec, error) {
	if len(masterKey) != 32 {
		return nil, errors.New("controlplane: session master key must be 32 bytes")
	}
	mac := hmac.New(sha256.New, masterKey)
	_, _ = mac.Write([]byte("statushub.browser-session.v1"))
	block, err := aes.NewCipher(mac.Sum(nil))
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &cookieCodec{aead: aead}, nil
}

func (c *cookieCodec) encode(value any, purpose string) (string, error) {
	plaintext, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, plaintext, []byte(purpose))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *cookieCodec) decode(value, purpose string, target any) error {
	if c == nil || len(value) == 0 || len(value) > maximumCookieSize {
		return auth.ErrUnauthenticated
	}
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(sealed) < c.aead.NonceSize()+c.aead.Overhead() {
		return auth.ErrUnauthenticated
	}
	nonce := sealed[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, sealed[c.aead.NonceSize():], []byte(purpose))
	if err != nil {
		return auth.ErrUnauthenticated
	}
	defer clear(plaintext)
	decoder := json.NewDecoder(strings.NewReader(string(plaintext)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return auth.ErrUnauthenticated
	}
	return nil
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
