package config

import (
	"strings"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("PUSHOVER_TOKEN", "tok")
	t.Setenv("PUSHOVER_USER", "usr")
}

func TestLoadDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // avoid picking up a real .env
	setRequired(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CheckInterval != time.Hour {
		t.Errorf("CheckInterval = %s, want 1h", cfg.CheckInterval)
	}
	if cfg.AlertInterval != 10*time.Minute {
		t.Errorf("AlertInterval = %s, want 10m", cfg.AlertInterval)
	}
	if cfg.TargetURL != DefaultTargetURL {
		t.Errorf("TargetURL = %s", cfg.TargetURL)
	}
}

func TestLoadMissingCredentials(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PUSHOVER_TOKEN", "")
	t.Setenv("PUSHOVER_USER", "")

	_, err := Load()
	if err == nil {
		t.Fatal("want error for missing credentials")
	}
	if !strings.Contains(err.Error(), "PUSHOVER_TOKEN") || !strings.Contains(err.Error(), "PUSHOVER_USER") {
		t.Errorf("error should name both missing vars: %v", err)
	}
}

func TestLoadInvalidInterval(t *testing.T) {
	t.Chdir(t.TempDir())
	setRequired(t)
	t.Setenv("CHECK_INTERVAL", "banana")

	if _, err := Load(); err == nil {
		t.Fatal("want error for invalid CHECK_INTERVAL")
	}
}

func TestLoadCustomIntervals(t *testing.T) {
	t.Chdir(t.TempDir())
	setRequired(t)
	t.Setenv("CHECK_INTERVAL", "30m")
	t.Setenv("ALERT_INTERVAL", "5m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.CheckInterval != 30*time.Minute || cfg.AlertInterval != 5*time.Minute {
		t.Errorf("intervals = %s / %s", cfg.CheckInterval, cfg.AlertInterval)
	}
}
