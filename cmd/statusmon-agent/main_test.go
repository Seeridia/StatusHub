package main

import "testing"

func TestParseConfigRequiresHTTPSExceptLoopback(t *testing.T) {
	t.Setenv("STATUSMON_SERVER_URL", "")
	t.Setenv("STATUSMON_AGENT_ID", "")
	t.Setenv("STATUSMON_AGENT_TOKEN", "")
	if _, err := parseConfig([]string{"-server-url", "http://status.example.test", "-agent-id", "agent", "-token", "token"}); err == nil {
		t.Fatal("accepted non-TLS remote server")
	}
	settings, err := parseConfig([]string{"-server-url", "http://127.0.0.1:9464", "-agent-id", "agent", "-token", "token", "-once"})
	if err != nil || !settings.once {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
}
