package main

import (
	"strings"
	"testing"
)

func TestParseConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := parseConfig(nil); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("missing database error = %v", err)
	}
	config, err := parseConfig([]string{
		"-database-url", "postgres://localhost/statusmon",
		"-nats-url", "nats://localhost:4222",
		"-worker-id", "worker-a",
		"-once",
	})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	if !config.once || config.workerID != "worker-a" {
		t.Fatalf("config = %+v", config)
	}
	if config.serviceRegion != "local" {
		t.Fatalf("service region=%q", config.serviceRegion)
	}
}

func TestParseConfigRejectsInvalidIntervals(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/statusmon")
	_, err := parseConfig([]string{"-collector-interval", "0s"})
	if err == nil || !strings.Contains(err.Error(), "intervals") {
		t.Fatalf("interval validation error = %v", err)
	}
}
