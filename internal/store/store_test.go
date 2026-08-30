package store

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type testPaths struct {
	state   string
	history string
}

func newTestStore(t *testing.T) (*Store, testPaths) {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	p := testPaths{
		state:   filepath.Join(dir, "state.json"),
		history: filepath.Join(dir, "history.jsonl"),
	}
	return New(p.state, p.history, log), p
}

func TestStateRoundTrip(t *testing.T) {
	s, _ := newTestStore(t)

	empty, reason, err := s.LoadState()
	if err != nil {
		t.Fatalf("LoadState on missing file: %v", err)
	}
	if empty.Baseline != "" || empty.Alerting || reason != "" {
		t.Errorf("zero state expected, got %+v (reason %q)", empty, reason)
	}

	want := State{
		Baseline:     "alte Störung",
		Alerting:     true,
		ChangedText:  "neuer Text",
		FailCount:    2,
		FailNotified: false,
		LastCheckAt:  time.Now().Truncate(time.Second),
	}
	if err := s.SaveState(want); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	got, reason, err := s.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if reason != "" {
		t.Errorf("unexpected reset reason %q", reason)
	}
	if got.SchemaVersion != CurrentSchemaVersion {
		t.Errorf("SchemaVersion = %d, want stamped %d", got.SchemaVersion, CurrentSchemaVersion)
	}
	if got.Baseline != want.Baseline || got.Alerting != want.Alerting ||
		got.ChangedText != want.ChangedText || got.FailCount != want.FailCount ||
		!got.LastCheckAt.Equal(want.LastCheckAt) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadStateResetsOnSchemaMismatch(t *testing.T) {
	s, paths := newTestStore(t)

	// A pre-versioning file (no schema_version field parses as 0).
	old := `{"baseline":"alter Text","alerting":true,"fail_count":2}`
	if err := os.WriteFile(paths.state, []byte(old), 0o600); err != nil {
		t.Fatalf("writing old state: %v", err)
	}

	st, reason, err := s.LoadState()
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if reason == "" {
		t.Error("want non-empty reset reason for schema mismatch")
	}
	// The discarded state was mid-alert; the reset silently drops that
	// alert, so the reason must call it out.
	if !strings.Contains(reason, "ACTIVE ALERT") {
		t.Errorf("reset reason does not mention the discarded active alert: %q", reason)
	}
	if st.Baseline != "" || st.Alerting || st.FailCount != 0 {
		t.Errorf("state not reset: %+v", st)
	}
}

func TestLoadStateResetsOnCorruptFile(t *testing.T) {
	s, paths := newTestStore(t)

	if err := os.WriteFile(paths.state, []byte(`{"baseline": truncated`), 0o600); err != nil {
		t.Fatalf("writing corrupt state: %v", err)
	}

	st, reason, err := s.LoadState()
	if err != nil {
		t.Fatalf("LoadState must not fail on corrupt state: %v", err)
	}
	if reason == "" {
		t.Error("want non-empty reset reason for corrupt file")
	}
	if st.Baseline != "" || st.Alerting {
		t.Errorf("state not reset: %+v", st)
	}
}

func TestHistoryAppendAndLoad(t *testing.T) {
	s, _ := newTestStore(t)

	if events, err := s.LoadHistory(10); err != nil || len(events) != 0 {
		t.Fatalf("empty history expected, got %d events, err=%v", len(events), err)
	}

	for i := 0; i < 5; i++ {
		ev := Event{Time: time.Now(), Kind: "check", Level: "info", Message: "check ok"}
		if err := s.AppendEvent(ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}

	events, err := s.LoadHistory(3)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("limit not applied: got %d events, want 3", len(events))
	}
	for _, ev := range events {
		if ev.Kind != "check" || ev.Message != "check ok" {
			t.Errorf("unexpected event: %+v", ev)
		}
	}
}

func TestHistorySkipsMalformedLines(t *testing.T) {
	s, paths := newTestStore(t)

	good := Event{Time: time.Now(), Kind: "check", Level: "info", Message: "ok"}
	if err := s.AppendEvent(good); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	// Simulate a line truncated by an unclean shutdown mid-append.
	f, err := os.OpenFile(paths.history, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("opening history: %v", err)
	}
	if _, err := f.WriteString(`{"time":"2026-08-22T` + "\n"); err != nil {
		t.Fatalf("writing garbage: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}
	if err := s.AppendEvent(good); err != nil {
		t.Fatalf("AppendEvent after garbage: %v", err)
	}

	events, err := s.LoadHistory(0)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(events) != 2 {
		t.Errorf("got %d events, want 2 valid ones (garbage skipped)", len(events))
	}
}
