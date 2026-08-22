// Package logging configures the application's structured file logger.
// Logs go to a rotating file because stdout is owned by the TUI.
package logging

import (
	"fmt"
	"log/slog"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// New returns a slog.Logger writing to path with rotation, and a close
// function that must be called on shutdown.
func New(path, level string) (*slog.Logger, func() error, error) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "", "info":
		lvl = slog.LevelInfo
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, nil, fmt.Errorf("invalid LOG_LEVEL %q (want debug|info|warn|error)", level)
	}

	rotator := &lumberjack.Logger{
		Filename:   path,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		Compress:   true,
	}
	handler := slog.NewTextHandler(rotator, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler), rotator.Close, nil
}
