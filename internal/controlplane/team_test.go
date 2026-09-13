package controlplane

import (
	"encoding/json"
	"testing"
)

func TestTeamSecretsAreEncrypted(t *testing.T) {
	codec, e := newCookieCodec(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	cipher := teamCipher{codec}
	plain := json.RawMessage(`{"token":"sa.private-token"}`)
	sealed, e := cipher.Encode(plain)
	if e != nil {
		t.Fatal(e)
	}
	opened, e := cipher.Decode(sealed)
	if e != nil || string(opened) != string(plain) {
		t.Fatal("secret round trip failed")
	}
	var wrong json.RawMessage
	if e = codec.decode(sealed, sessionCookieName, &wrong); e == nil {
		t.Fatal("cross-purpose secret accepted")
	}
}
