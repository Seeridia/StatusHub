package secret

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
)

func TestStaticEnvelopeRoundTripAndBinding(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	envelope, err := NewStaticEnvelope("local-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	aad := EndpointAssociatedData("endpoint-a", 1)
	ciphertext, err := envelope.Seal(context.Background(), []byte(`{"url":"https://example.test"}`), aad)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := envelope.Open(context.Background(), ciphertext, aad, "local-v1")
	if err != nil || string(plaintext) != `{"url":"https://example.test"}` {
		t.Fatalf("plaintext=%q err=%v", plaintext, err)
	}
	if _, err := envelope.Open(context.Background(), ciphertext, EndpointAssociatedData("endpoint-b", 1), "local-v1"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("wrong AAD error=%v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := envelope.Open(context.Background(), ciphertext, aad, "local-v1"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("tamper error=%v", err)
	}
}

func TestParseBase64Key(t *testing.T) {
	want := bytes.Repeat([]byte{7}, 32)
	got, err := ParseBase64Key(base64.RawStdEncoding.EncodeToString(want))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got=%x err=%v", got, err)
	}
	if _, err := ParseBase64Key("short"); err == nil {
		t.Fatal("expected invalid key error")
	}
}
