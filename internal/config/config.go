// Package config loads application configuration from the environment,
// optionally seeded from a .env file in the working directory.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	// DefaultTargetURL is the Berlin service page being watched.
	DefaultTargetURL = "https://service.berlin.de/dienstleistung/318998/"

	defaultCheckInterval = time.Hour
	defaultAlertInterval = 10 * time.Minute
)

// Config holds all runtime settings. Secrets come exclusively from the
// environment / .env file and must never be logged.
type Config struct {
	PushoverToken string
	PushoverUser  string
	TargetURL     string
	CheckInterval time.Duration
	AlertInterval time.Duration
	LogLevel      string

	StateFile   string
	HistoryFile string
	LogFile     string
}

// Load reads .env (if present) and the process environment.
func Load() (*Config, error) {
	// A missing .env is fine; real env vars may already be set.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("loading .env: %w", err)
	}

	cfg := &Config{
		PushoverToken: strings.TrimSpace(os.Getenv("PUSHOVER_TOKEN")),
		PushoverUser:  strings.TrimSpace(os.Getenv("PUSHOVER_USER")),
		TargetURL:     envOr("TARGET_URL", DefaultTargetURL),
		LogLevel:      envOr("LOG_LEVEL", "info"),
		StateFile:     envOr("STATE_FILE", "state.json"),
		HistoryFile:   envOr("HISTORY_FILE", "history.jsonl"),
		LogFile:       envOr("LOG_FILE", "app.log"),
	}

	var err error
	if cfg.CheckInterval, err = durationOr("CHECK_INTERVAL", defaultCheckInterval); err != nil {
		return nil, err
	}
	if cfg.AlertInterval, err = durationOr("ALERT_INTERVAL", defaultAlertInterval); err != nil {
		return nil, err
	}

	var missing []string
	if cfg.PushoverToken == "" {
		missing = append(missing, "PUSHOVER_TOKEN")
	}
	if cfg.PushoverUser == "" {
		missing = append(missing, "PUSHOVER_USER")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf(
			"missing required settings: %s\n\nCopy .env.example to .env and fill in your Pushover credentials.\n"+
				"PUSHOVER_TOKEN is an application token (create one at https://pushover.net/apps/build),\n"+
				"PUSHOVER_USER is the user key shown on your Pushover dashboard",
			strings.Join(missing, ", "))
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func durationOr(key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, v, err)
	}
	if d < time.Second {
		return 0, fmt.Errorf("%s must be at least 1s, got %s", key, d)
	}
	return d, nil
}
