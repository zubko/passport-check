package engine

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"zubko.io/passport-check/internal/checker"
	"zubko.io/passport-check/internal/notify"
	"zubko.io/passport-check/internal/store"
)

// stubFetcher returns a programmable notice or error.
type stubFetcher struct {
	mu     sync.Mutex
	notice string
	err    error
}

func (f *stubFetcher) set(notice string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notice, f.err = notice, err
}

func (f *stubFetcher) Fetch(context.Context) (checker.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return checker.Result{}, f.err
	}
	return checker.Result{Notice: f.notice, HTTPStatus: 200}, nil
}

// stubNotifier records every successfully sent message and can be told to
// fail sends.
type stubNotifier struct {
	mu   sync.Mutex
	fail bool
	sent []notify.Message
}

func (n *stubNotifier) setFail(v bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fail = v
}

func (n *stubNotifier) Send(_ context.Context, m notify.Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fail {
		return errors.New("simulated send failure")
	}
	n.sent = append(n.sent, m)
	return nil
}

func (n *stubNotifier) messages() []notify.Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]notify.Message, len(n.sent))
	copy(out, n.sent)
	return out
}

// countTitled returns how many delivered messages contain substr in their
// title.
func (n *stubNotifier) countTitled(substr string) int {
	count := 0
	for _, m := range n.messages() {
		if strings.Contains(m.Title, substr) {
			count++
		}
	}
	return count
}

const errorNotice = "Leider ist die Dienstleistung derzeit online nicht nutzbar."

type fixture struct {
	eng      *Engine
	fetcher  *stubFetcher
	notifier *stubNotifier
	store    *store.Store
}

func newFixture(t *testing.T, persisted store.State) *fixture {
	return newFixtureNotice(t, persisted, errorNotice)
}

// newFixtureNotice starts an engine whose fetcher initially serves the
// given notice text (the first check runs immediately on startup).
func newFixtureNotice(t *testing.T, persisted store.State, notice string) *fixture {
	t.Helper()
	dir := t.TempDir()
	log := slog.New(slog.DiscardHandler)
	st := store.New(filepath.Join(dir, "state.json"), filepath.Join(dir, "history.jsonl"), log)

	f := &stubFetcher{notice: notice}
	n := &stubNotifier{}
	// Long check interval: checks are driven manually via CheckNow.
	// Short alert interval so repeat alerts are observable.
	eng := New(f, n, st, persisted, "https://example.test/page", time.Hour, 60*time.Millisecond, log)

	ctx, cancel := context.WithCancel(context.Background())
	go eng.Run(ctx)
	t.Cleanup(func() {
		cancel()
		<-eng.Done()
	})
	return &fixture{eng: eng, fetcher: f, notifier: n, store: st}
}

// waitFor polls until cond is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// runCheck drives one manual check to completion.
func runCheck(t *testing.T, fx *fixture) {
	t.Helper()
	before := fx.eng.Snapshot().ChecksDone
	fx.eng.CheckNow()
	waitFor(t, "check completed", func() bool { return fx.eng.Snapshot().ChecksDone > before })
}

// assertNoMoreSends verifies no further notifications arrive within a few
// alert intervals (the fixture's alert interval is 60ms).
func assertNoMoreSends(t *testing.T, fx *fixture, context string) {
	t.Helper()
	count := len(fx.notifier.messages())
	time.Sleep(150 * time.Millisecond)
	if got := len(fx.notifier.messages()); got != count {
		t.Errorf("notifications continued after %s: %d -> %d", context, count, got)
	}
}

func TestFirstCheckCapturesBaseline(t *testing.T) {
	fx := newFixture(t, store.State{})
	waitFor(t, "baseline captured", func() bool {
		return fx.eng.Snapshot().Baseline == errorNotice
	})
	if got := fx.notifier.messages(); len(got) != 0 {
		t.Errorf("no notifications expected on baseline capture, got %d", len(got))
	}
	if st := fx.eng.Snapshot().Status; st != StatusOK {
		t.Errorf("status = %s, want OK", st)
	}
}

func TestChangeTriggersRepeatingHighPriorityAlerts(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})

	fx.fetcher.set("Die Dienstleistung ist wieder online verfügbar.", nil)
	fx.eng.CheckNow()

	waitFor(t, "CHANGED status", func() bool {
		return fx.eng.Snapshot().Status == StatusChanged
	})
	waitFor(t, "at least 2 alerts (initial + one repeat)", func() bool {
		return len(fx.notifier.messages()) >= 2
	})
	for _, m := range fx.notifier.messages()[:2] {
		if m.Priority != notify.PriorityHigh {
			t.Errorf("alert priority = %d, want high", m.Priority)
		}
		if m.Sound != notify.SoundPositive {
			t.Errorf("alert sound = %q, want %q", m.Sound, notify.SoundPositive)
		}
		if !strings.Contains(m.Body, "wieder online") {
			t.Errorf("alert body missing new notice text: %q", m.Body)
		}
	}
	if next := fx.eng.Snapshot().NextAlertAt; next.IsZero() {
		t.Error("NextAlertAt should be set while alerting")
	}
}

func TestResumeActiveAlertAlertsImmediately(t *testing.T) {
	// The page still shows the changed text after the restart.
	fx := newFixtureNotice(t, store.State{
		Baseline:    errorNotice,
		Alerting:    true,
		ChangedText: "Neuer Text",
	}, "Neuer Text")

	// Restarting with an active alert must alert right away — waiting a
	// full alert interval would let frequent restarts silence alerts.
	waitFor(t, "immediate resume alert", func() bool {
		return len(fx.notifier.messages()) >= 1
	})
	first := fx.notifier.messages()[0]
	if first.Priority != notify.PriorityHigh {
		t.Errorf("resume alert priority = %d, want high", first.Priority)
	}
	if !strings.Contains(first.Body, "Neuer Text") {
		t.Errorf("resume alert body missing changed text: %q", first.Body)
	}
	if st := fx.eng.Snapshot().Status; st != StatusChanged {
		t.Errorf("status = %s, want CHANGED", st)
	}
	waitFor(t, "repeat alert after resume", func() bool {
		return len(fx.notifier.messages()) >= 2
	})
}

func TestRevertSendsSingleNormalNotification(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})

	fx.fetcher.set("Neuer Text", nil)
	fx.eng.CheckNow()
	waitFor(t, "alerting", func() bool { return fx.eng.Snapshot().Alerting })

	fx.fetcher.set(errorNotice, nil)
	fx.eng.CheckNow()
	waitFor(t, "alerting stopped", func() bool { return !fx.eng.Snapshot().Alerting })

	msgs := fx.notifier.messages()
	last := msgs[len(msgs)-1]
	if last.Priority != notify.PriorityNormal {
		t.Errorf("revert notification priority = %d, want normal", last.Priority)
	}
	if last.Sound != notify.SoundNegative {
		t.Errorf("revert notification sound = %q, want %q", last.Sound, notify.SoundNegative)
	}
	if !strings.Contains(last.Title, "error message is back") {
		t.Errorf("unexpected revert title: %q", last.Title)
	}
	if fx.eng.Snapshot().Baseline != errorNotice {
		t.Errorf("baseline should remain the error notice")
	}

	assertNoMoreSends(t, fx, "revert")
}

func TestStopAlertsAdoptsNewBaseline(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})

	const newText = "Alles wieder gut"
	fx.fetcher.set(newText, nil)
	fx.eng.CheckNow()
	waitFor(t, "alerting", func() bool { return fx.eng.Snapshot().Alerting })

	fx.eng.StopAlerts()
	waitFor(t, "alerting stopped", func() bool { return !fx.eng.Snapshot().Alerting })

	snap := fx.eng.Snapshot()
	if snap.Baseline != newText {
		t.Errorf("baseline = %q, want adopted new text %q", snap.Baseline, newText)
	}
	if snap.Status != StatusOK {
		t.Errorf("status = %s, want OK", snap.Status)
	}

	assertNoMoreSends(t, fx, "stop alerts")
}

func TestStopAlertsAdoptsSentinelBaseline(t *testing.T) {
	// A vanished notice block must be adoptable as a baseline: Stop Alerts
	// must silence the alert cycle, not restart it on the next check.
	fx := newFixture(t, store.State{Baseline: errorNotice})

	fx.fetcher.set(checker.NoticeMissing, nil)
	fx.eng.CheckNow()
	waitFor(t, "alerting on missing block", func() bool { return fx.eng.Snapshot().Alerting })

	fx.eng.StopAlerts()
	waitFor(t, "alerting stopped", func() bool { return !fx.eng.Snapshot().Alerting })
	if got := fx.eng.Snapshot().Baseline; got != checker.NoticeMissing {
		t.Errorf("baseline = %q, want sentinel %q", got, checker.NoticeMissing)
	}

	// Next check with the same result must NOT re-trigger alerting.
	runCheck(t, fx)
	if fx.eng.Snapshot().Alerting {
		t.Error("alerting re-triggered after adopting sentinel baseline")
	}
}

func TestFailThresholdNotifiesOnceAndRecovers(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})
	waitFor(t, "first check done", func() bool { return fx.eng.Snapshot().ChecksDone >= 1 })

	fx.fetcher.set("", errors.New("connection refused"))
	for i := 0; i < 4; i++ {
		runCheck(t, fx)
	}

	waitFor(t, "failing status", func() bool { return fx.eng.Snapshot().Status == StatusFailing })
	if got := fx.notifier.countTitled("checks are failing"); got != 1 {
		t.Errorf("failure notifications = %d, want exactly 1 (after threshold, not per failure)", got)
	}
	for _, m := range fx.notifier.messages() {
		if strings.Contains(m.Title, "checks are failing") && m.Priority != notify.PriorityNormal {
			t.Errorf("failure notification priority = %d, want normal", m.Priority)
		}
	}

	// Recovery: one "recovered" notification, no change alert.
	fx.fetcher.set(errorNotice, nil)
	fx.eng.CheckNow()
	waitFor(t, "recovered", func() bool { return fx.eng.Snapshot().Status == StatusOK })

	if got := fx.notifier.countTitled("recovered"); got != 1 {
		t.Errorf("recovered notifications = %d, want 1", got)
	}
	if fx.eng.Snapshot().Alerting {
		t.Error("fetch failures must never start alerting")
	}
}

func TestSuccessPersistsFailCounterReset(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})

	fx.fetcher.set("", errors.New("timeout"))
	runCheck(t, fx)
	runCheck(t, fx)

	fx.fetcher.set(errorNotice, nil)
	runCheck(t, fx)
	waitFor(t, "OK status", func() bool { return fx.eng.Snapshot().Status == StatusOK })

	// The reset must reach disk: a restart with stale fail_count would
	// break the "3 consecutive failures" contract.
	waitFor(t, "persisted reset", func() bool {
		st, reason, err := fx.store.LoadState()
		return err == nil && reason == "" && st.FailCount == 0 && !st.FailNotified && !st.LastCheckAt.IsZero()
	})
}

func TestFailedNotificationSendIsRetriedNextFailure(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})
	waitFor(t, "first check done", func() bool { return fx.eng.Snapshot().ChecksDone >= 1 })

	// Pushover unreachable while the fail streak crosses the threshold.
	fx.notifier.setFail(true)
	fx.fetcher.set("", errors.New("connection refused"))
	for i := 0; i < 3; i++ {
		runCheck(t, fx)
	}
	if len(fx.notifier.messages()) != 0 {
		t.Fatalf("no messages should have been delivered while notifier failing")
	}

	// Pushover reachable again, fetches still failing: the "checks are
	// failing" notification must be retried, not suppressed forever.
	fx.notifier.setFail(false)
	runCheck(t, fx)
	waitFor(t, "retried failure notification", func() bool {
		return fx.notifier.countTitled("checks are failing") >= 1
	})
}

func TestAlertsSentCountsOnlyDeliveredAlerts(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})
	waitFor(t, "first check done", func() bool { return fx.eng.Snapshot().ChecksDone >= 1 })

	// Change detected while Pushover is unreachable: alerts are attempted
	// but none delivered, so the "alerts sent" counter must stay at 0.
	fx.notifier.setFail(true)
	fx.fetcher.set("Neuer Text", nil)
	runCheck(t, fx)
	waitFor(t, "alerting", func() bool { return fx.eng.Snapshot().Alerting })
	if got := fx.eng.Snapshot().AlertsSent; got != 0 {
		t.Errorf("AlertsSent = %d after failed sends, want 0", got)
	}

	fx.notifier.setFail(false)
	waitFor(t, "delivered repeat alert counted", func() bool {
		return fx.eng.Snapshot().AlertsSent >= 1
	})
}

func TestTestActionsSendPrefixedMessages(t *testing.T) {
	fx := newFixture(t, store.State{Baseline: errorNotice})
	waitFor(t, "first check done", func() bool { return fx.eng.Snapshot().ChecksDone >= 1 })

	fx.eng.TestSuccess()
	waitFor(t, "test success sent", func() bool {
		for _, m := range fx.notifier.messages() {
			if strings.HasPrefix(m.Title, "[TEST]") && m.Priority == notify.PriorityHigh {
				return true
			}
		}
		return false
	})

	fx.eng.TestFailure()
	waitFor(t, "test failure sent", func() bool {
		for _, m := range fx.notifier.messages() {
			if strings.HasPrefix(m.Title, "[TEST]") && m.Priority == notify.PriorityNormal {
				return true
			}
		}
		return false
	})

	if fx.eng.Snapshot().Alerting {
		t.Error("test actions must not change engine state")
	}
}
