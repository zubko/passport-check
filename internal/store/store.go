// Package store persists monitor state and the event history across
// restarts. State is a small JSON file rewritten atomically; history is an
// append-only JSONL file.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// CurrentSchemaVersion identifies the shape and semantics of persisted
// state. This app NEVER migrates old state: on any mismatch (including
// pre-versioning files, which parse as 0) LoadState discards the file and
// starts fresh — the baseline simply re-captures on the next check. Bump
// this whenever the State fields or the meaning of their values change
// (e.g. the notice-text format).
const CurrentSchemaVersion = 1

// State is everything the engine needs to survive a restart without
// false-triggering or losing an active alert.
type State struct {
	// SchemaVersion is stamped by SaveState; LoadState resets state on
	// mismatch rather than migrating.
	SchemaVersion int `json:"schema_version"`
	// Baseline is the last acknowledged notice text (normally the known
	// error message). Empty until the first successful check.
	Baseline string `json:"baseline"`
	// Alerting is true while the notice differs from Baseline and the user
	// has not acknowledged the change.
	Alerting bool `json:"alerting"`
	// ChangedText is the most recent differing notice text seen while
	// alerting.
	ChangedText string `json:"changed_text,omitempty"`
	// FailCount is the number of consecutive fetch failures.
	FailCount int `json:"fail_count"`
	// FailNotified records that the "checks are failing" notification for
	// the current failure streak was already sent.
	FailNotified bool      `json:"fail_notified"`
	LastCheckAt  time.Time `json:"last_check_at,omitzero"`
}

// Event is one history entry, shown in the TUI history panel.
type Event struct {
	Time    time.Time `json:"time"`
	Kind    string    `json:"kind"`  // check, change, revert, alert, notify, fail, action, info
	Level   string    `json:"level"` // info, good, warn, error
	Message string    `json:"message"`
}

// Store owns the state and history files.
type Store struct {
	statePath   string
	historyPath string
	log         *slog.Logger
}

// New builds a Store using the given file paths, creating their parent
// directories so nested STATE_FILE/HISTORY_FILE paths work. A creation
// failure is only logged: the subsequent writes fail loudly anyway.
func New(statePath, historyPath string, log *slog.Logger) *Store {
	for _, p := range []string{statePath, historyPath} {
		if dir := filepath.Dir(p); dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				log.Warn("creating data directory failed", "dir", dir, "err", err)
			}
		}
	}
	return &Store{statePath: statePath, historyPath: historyPath, log: log}
}

// LoadState returns the persisted state, or a zero state if none exists
// yet. The returned reason is non-empty when an existing state file was
// discarded (schema mismatch or unreadable content): this app resets
// state instead of migrating it, per CurrentSchemaVersion.
func (s *Store) LoadState() (State, string, error) {
	data, err := os.ReadFile(s.statePath)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, "", nil
	}
	if err != nil {
		return State{}, "", fmt.Errorf("reading %s: %w", s.statePath, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		s.log.Warn("state file unreadable, starting fresh", "path", s.statePath, "err", err)
		return State{}, "state file was unreadable, starting fresh", nil
	}
	if st.SchemaVersion != CurrentSchemaVersion {
		reason := fmt.Sprintf("state schema changed (v%d -> v%d), starting fresh", st.SchemaVersion, CurrentSchemaVersion)
		s.log.Warn("discarding persisted state", "path", s.statePath, "reason", reason)
		return State{}, reason, nil
	}
	return st, "", nil
}

// SaveState atomically rewrites the state file, stamping the current
// schema version.
func (s *Store) SaveState(st State) error {
	st.SchemaVersion = CurrentSchemaVersion
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		return fmt.Errorf("replacing %s: %w", s.statePath, err)
	}
	s.log.Debug("state saved", "path", s.statePath, "alerting", st.Alerting, "fail_count", st.FailCount)
	return nil
}

// AppendEvent adds one event to the history file.
func (s *Store) AppendEvent(ev Event) error {
	f, err := os.OpenFile(s.historyPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", s.historyPath, err)
	}
	line, err := json.Marshal(ev)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("encoding event: %w", err)
	}
	_, werr := f.Write(append(line, '\n'))
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("appending to %s: %w", s.historyPath, werr)
	}
	if cerr != nil {
		return fmt.Errorf("closing %s: %w", s.historyPath, cerr)
	}
	return nil
}

// LoadHistory returns up to limit most recent events, oldest first.
// Malformed lines are skipped.
func (s *Store) LoadHistory(limit int) ([]Event, error) {
	f, err := os.Open(s.historyPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", s.historyPath, err)
	}
	defer func() { _ = f.Close() }()

	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			s.log.Warn("skipping malformed history line", "err", err)
			continue
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return events, fmt.Errorf("reading %s: %w", s.historyPath, err)
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}
