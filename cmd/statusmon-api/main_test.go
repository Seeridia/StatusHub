package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseConfigRequiresKeysAndSecurePublicURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/statusmon")
	t.Setenv("STATUSMON_CONFIG_KEY", "")
	t.Setenv("STATUSMON_API_KEY", "")
	if _, err := parseConfig(nil); err == nil || !strings.Contains(err.Error(), "KEY") {
		t.Fatalf("missing key error=%v", err)
	}
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	configuration, err := parseConfig([]string{"-config-key", key, "-api-key", key, "-public-url", "http://127.0.0.1:8080", "-allow-http-oidc"})
	if err != nil {
		t.Fatal(err)
	}
	if configuration.listenAddress != "127.0.0.1:8080" || configuration.serviceRegion != "local" {
		t.Fatalf("configuration=%#v", configuration)
	}
	if _, err := parseConfig([]string{"-config-key", key, "-api-key", key, "-public-url", "http://example.com"}); err == nil {
		t.Fatal("non-TLS public URL accepted")
	}
}
