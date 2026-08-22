// passport-check watches a service.berlin.de page for changes to its
// notice text and alerts via Pushover.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"zubko.io/passport-check/internal/checker"
	"zubko.io/passport-check/internal/config"
	"zubko.io/passport-check/internal/engine"
	"zubko.io/passport-check/internal/logging"
	"zubko.io/passport-check/internal/notify"
	"zubko.io/passport-check/internal/store"
	"zubko.io/passport-check/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "passport-check:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log, closeLog, err := logging.New(cfg.LogFile, cfg.LogLevel)
	if err != nil {
		return err
	}
	defer closeLog() //nolint:errcheck // best-effort close on shutdown

	log.Info("starting passport-check",
		"target_url", cfg.TargetURL,
		"check_interval", cfg.CheckInterval,
		"alert_interval", cfg.AlertInterval,
		"log_level", cfg.LogLevel,
	)

	st := store.New(cfg.StateFile, cfg.HistoryFile, log)
	persisted, resetReason, err := st.LoadState()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}
	if resetReason != "" {
		// Surface the automatic reset in the History panel; appended
		// before LoadHistory so it shows up in this session too.
		ev := store.Event{Time: time.Now(), Kind: "info", Level: "warn", Message: "Previous state discarded: " + resetReason}
		if aerr := st.AppendEvent(ev); aerr != nil {
			log.Warn("recording state reset in history failed", "err", aerr)
		}
	}
	history, err := st.LoadHistory(500)
	if err != nil {
		log.Warn("loading history failed", "err", err)
	}

	fetcher := checker.New(cfg.TargetURL, log)
	notifier := notify.New(cfg.PushoverToken, cfg.PushoverUser, cfg.AlertInterval, log)
	eng := engine.New(fetcher, notifier, st, persisted, cfg.TargetURL, cfg.CheckInterval, cfg.AlertInterval, log)

	model := tui.New(eng, history)
	program := tea.NewProgram(model, tea.WithAltScreen())

	eng.SetEventHandler(func(ev store.Event) {
		program.Send(tui.EventMsg{Event: ev})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go eng.Run(ctx)

	_, err = program.Run()

	// Stop the engine and wait for it to finish flushing state.
	cancel()
	<-eng.Done()
	log.Info("shutdown complete")
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return nil
}
