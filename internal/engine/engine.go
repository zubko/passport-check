// Package engine runs the monitoring loop: periodic checks of the page,
// change detection against the persisted baseline, and repeated Pushover
// alerts while a change is unacknowledged.
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"zubko.io/passport-check/internal/checker"
	"zubko.io/passport-check/internal/notify"
	"zubko.io/passport-check/internal/store"
)

// Fetcher fetches the page. Implemented by *checker.Checker.
type Fetcher interface {
	Fetch(ctx context.Context) (checker.Result, error)
}

// Notifier sends notifications. Implemented by *notify.Client.
type Notifier interface {
	Send(ctx context.Context, m notify.Message) error
}

// Status is the engine's displayable condition.
type Status string

// Engine statuses, in display priority order.
const (
	StatusOK      Status = "OK"      // notice matches baseline
	StatusChanged Status = "CHANGED" // notice differs, alerting
	StatusFailing Status = "FAILING" // consecutive fetch failures
)

// failThreshold is the number of consecutive fetch failures after which a
// single "checks are failing" notification is sent.
const failThreshold = 3

// Snapshot is a copy of the engine state for rendering. Persisted fields
// come from the embedded store.State; everything else is session-only and
// derived at read time in Snapshot().
type Snapshot struct {
	store.State
	Status       Status
	LastResult   string
	NextCheckAt  time.Time
	NextAlertAt  time.Time // zero unless alerting
	ChecksDone   int
	AlertsSent   int
	CheckRunning bool
	TargetURL    string
	CheckEvery   time.Duration
	AlertEvery   time.Duration
}

func statusOf(st store.State) Status {
	switch {
	case st.Alerting:
		return StatusChanged
	case st.FailCount > 0:
		return StatusFailing
	default:
		return StatusOK
	}
}

type actionKind int

const (
	actionCheckNow actionKind = iota
	actionStopAlerts
	actionTestSuccess
	actionTestFailure
)

// Engine coordinates checks, alerts, persistence, and events.
type Engine struct {
	fetcher  Fetcher
	notifier Notifier
	store    *store.Store
	log      *slog.Logger

	targetURL  string
	checkEvery time.Duration
	alertEvery time.Duration

	actions chan actionKind

	// alertTimer drives the repeat-alert cadence. It is armed iff
	// alerting is active and is touched only by the Run goroutine
	// (Go 1.23+ timer semantics make Stop/Reset race-free regardless).
	alertTimer *time.Timer

	mu           sync.Mutex
	st           store.State // the single persisted source of truth
	lastResult   string
	nextCheckAt  time.Time
	nextAlertAt  time.Time
	checksDone   int
	alertsSent   int
	checkRunning bool
	onEvent      func(store.Event)

	doneOnce sync.Once
	done     chan struct{}
}

// New builds an engine resuming from the given persisted state.
func New(
	fetcher Fetcher,
	notifier Notifier,
	st *store.Store,
	persisted store.State,
	targetURL string,
	checkEvery, alertEvery time.Duration,
	log *slog.Logger,
) *Engine {
	alertTimer := time.NewTimer(alertEvery)
	alertTimer.Stop() // armed only while alerting
	return &Engine{
		fetcher:    fetcher,
		notifier:   notifier,
		store:      st,
		log:        log,
		targetURL:  targetURL,
		checkEvery: checkEvery,
		alertEvery: alertEvery,
		actions:    make(chan actionKind, 8),
		alertTimer: alertTimer,
		st:         persisted,
		done:       make(chan struct{}),
	}
}

// SetEventHandler registers the callback invoked for every event (already
// persisted to history). Must be set before Run.
func (e *Engine) SetEventHandler(fn func(store.Event)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onEvent = fn
}

// Snapshot composes a copy of the current state for rendering.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Snapshot{
		State:        e.st,
		Status:       statusOf(e.st),
		LastResult:   e.lastResult,
		NextCheckAt:  e.nextCheckAt,
		NextAlertAt:  e.nextAlertAt,
		ChecksDone:   e.checksDone,
		AlertsSent:   e.alertsSent,
		CheckRunning: e.checkRunning,
		TargetURL:    e.targetURL,
		CheckEvery:   e.checkEvery,
		AlertEvery:   e.alertEvery,
	}
}

// Done is closed when Run has fully exited.
func (e *Engine) Done() <-chan struct{} { return e.done }

// The action methods below are safe to call from the TUI goroutine. They
// never block: if the buffer is full the action is dropped (the loop is
// wedged anyway).

// CheckNow runs a check immediately and resets the check countdown.
func (e *Engine) CheckNow() { e.enqueue(actionCheckNow) }

// StopAlerts acknowledges an active change: alerts stop and the current
// notice text becomes the new baseline.
func (e *Engine) StopAlerts() { e.enqueue(actionStopAlerts) }

// TestSuccess sends a [TEST] emergency-priority change alert.
func (e *Engine) TestSuccess() { e.enqueue(actionTestSuccess) }

// TestFailure sends a [TEST] normal-priority error-is-back notification.
func (e *Engine) TestFailure() { e.enqueue(actionTestFailure) }

func (e *Engine) enqueue(a actionKind) {
	select {
	case e.actions <- a:
	default:
		e.log.Warn("action queue full, dropping action", "action", a)
	}
}

// armAlert (re)starts the repeat-alert countdown.
func (e *Engine) armAlert() {
	e.alertTimer.Reset(e.alertEvery)
	e.setNextAlert(time.Now().Add(e.alertEvery))
}

// disarmAlert stops the repeat-alert countdown.
func (e *Engine) disarmAlert() {
	e.alertTimer.Stop()
	e.setNextAlert(time.Time{})
}

// Run executes the monitor loop until ctx is canceled. It performs an
// immediate first check on startup; if it resumes with an active alert it
// also sends an alert immediately so restarts never silence the cadence.
func (e *Engine) Run(ctx context.Context) {
	defer e.doneOnce.Do(func() { close(e.done) })

	e.log.Info("engine started",
		"url", e.targetURL,
		"check_interval", e.checkEvery,
		"alert_interval", e.alertEvery,
		"resumed_alerting", e.st.Alerting,
		"resumed_fail_count", e.st.FailCount,
	)

	checkTimer := time.NewTimer(0) // immediate first check
	defer checkTimer.Stop()
	defer e.alertTimer.Stop()

	if e.st.Alerting {
		e.emit("info", "info", "Resumed with an active alert — alerting now and every "+e.alertEvery.String())
		e.sendChangeAlert(ctx, false)
		e.armAlert()
	}

	for {
		select {
		case <-ctx.Done():
			e.log.Info("engine stopping", "reason", ctx.Err())
			return

		case <-checkTimer.C:
			e.doCheck(ctx, "scheduled")
			checkTimer.Reset(e.checkEvery)
			e.setNextCheck(time.Now().Add(e.checkEvery))

		case <-e.alertTimer.C:
			if e.Snapshot().Alerting {
				e.sendChangeAlert(ctx, false)
				e.armAlert()
			}

		case a := <-e.actions:
			switch a {
			case actionCheckNow:
				e.emit("action", "info", "Manual check requested")
				e.log.Info("action: check now")
				checkTimer.Stop()
				e.doCheck(ctx, "manual")
				checkTimer.Reset(e.checkEvery)
				e.setNextCheck(time.Now().Add(e.checkEvery))

			case actionStopAlerts:
				e.handleStopAlerts()

			case actionTestSuccess:
				e.emit("action", "info", "Test: sending 'service changed' alert (emergency priority)")
				e.log.Info("action: test success notification")
				e.sendNotification(ctx, notify.Message{
					Title:    "[TEST] Berlin service page changed!",
					Body:     "This is a test of the change alert. The notice text on " + e.targetURL + " would have changed.",
					Priority: notify.PriorityEmergency,
					Sound:    notify.SoundPositive,
				})

			case actionTestFailure:
				e.emit("action", "info", "Test: sending 'error is back' notification (normal priority)")
				e.log.Info("action: test failure notification")
				e.sendNotification(ctx, notify.Message{
					Title:    "[TEST] Error message is back",
					Body:     "This is a test of the revert notification. The page would be showing the error notice again.",
					Priority: notify.PriorityNormal,
					Sound:    notify.SoundNegative,
				})
			}
		}
	}
}

// doCheck fetches the page and updates state, arming or disarming the
// repeat-alert timer as alerting starts or stops.
func (e *Engine) doCheck(ctx context.Context, trigger string) {
	e.setCheckRunning(true)
	defer e.setCheckRunning(false)

	fctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	res, err := e.fetcher.Fetch(fctx)
	cancel()

	// A quit mid-fetch aborts the request with a cancellation error;
	// recording that as a fetch failure would poison the persisted
	// fail streak, so drop the check entirely.
	if ctx.Err() != nil {
		return
	}

	e.mu.Lock()
	e.st.LastCheckAt = time.Now()
	e.checksDone++
	e.mu.Unlock()

	if err != nil {
		e.handleFetchFailure(ctx, err)
		return
	}
	e.handleFetchSuccess(ctx, res, trigger)
}

func (e *Engine) handleFetchFailure(ctx context.Context, err error) {
	e.mu.Lock()
	e.st.FailCount++
	failCount := e.st.FailCount
	shouldNotify := failCount >= failThreshold && !e.st.FailNotified
	e.lastResult = "fetch failed: " + err.Error()
	e.mu.Unlock()

	e.log.Warn("check failed", "err", err, "consecutive_fails", failCount)
	e.emit("fail", "error", fmt.Sprintf("Check failed (%d in a row): %v", failCount, err))
	e.saveState()

	if shouldNotify {
		sent := e.sendNotification(ctx, notify.Message{
			Title:    "passport-check: checks are failing",
			Body:     fmt.Sprintf("%d consecutive checks of %s have failed. Latest error: %v", failCount, e.targetURL, err),
			Priority: notify.PriorityNormal,
		})
		// Mark notified only on success so a failed send is retried on
		// the next failed check instead of being suppressed forever.
		if sent {
			e.mu.Lock()
			e.st.FailNotified = true
			e.mu.Unlock()
			e.saveState()
		}
	}
}

func (e *Engine) handleFetchSuccess(ctx context.Context, res checker.Result, trigger string) {
	e.mu.Lock()
	wasFailing := e.st.FailCount >= failThreshold && e.st.FailNotified
	hadFails := e.st.FailCount > 0
	e.st.FailCount = 0
	e.st.FailNotified = false
	baseline := e.st.Baseline
	alerting := e.st.Alerting
	e.mu.Unlock()

	if hadFails {
		e.emit("info", "good", "Fetching works again")
	}
	if wasFailing {
		e.sendNotification(ctx, notify.Message{
			Title:    "passport-check: checks recovered",
			Body:     "Fetching " + e.targetURL + " works again.",
			Priority: notify.PriorityNormal,
		})
	}

	notice := res.Notice
	e.log.Info("check completed",
		"trigger", trigger,
		"http_status", res.HTTPStatus,
		"duration", res.Duration,
		"notice_len", len(notice),
		"changed", baseline != "" && notice != baseline,
	)
	e.log.Debug("notice text", "notice", notice)

	switch {
	case baseline == "":
		// First ever successful check: adopt whatever is shown now.
		e.mu.Lock()
		e.st.Baseline = notice
		e.lastResult = "baseline captured"
		e.mu.Unlock()
		e.emit("check", "info", "First check: captured current notice as baseline — "+snippet(notice, 120))
		e.saveState()

	case notice == baseline:
		if alerting {
			// The text reverted to the baseline: the error is back.
			e.mu.Lock()
			e.st.Alerting = false
			e.st.ChangedText = ""
			e.lastResult = "notice reverted to baseline"
			e.mu.Unlock()
			e.disarmAlert()
			e.log.Info("notice reverted to baseline, alerting stopped")
			e.emit("revert", "warn", "Notice reverted to the previous text (error is back). Alerts stopped.")
			e.saveState()
			e.sendNotification(ctx, notify.Message{
				Title:    "Berlin service: error message is back",
				Body:     "The page is showing the previous notice again:\n\n" + snippet(notice, 400),
				Priority: notify.PriorityNormal,
				Sound:    notify.SoundNegative,
			})
		} else {
			e.mu.Lock()
			e.lastResult = "notice unchanged"
			e.mu.Unlock()
			e.emit("check", "info", "Check OK — notice unchanged")
			// Persist even on the no-op path: fail-counter resets and
			// LastCheckAt must survive a restart.
			e.saveState()
		}

	default: // notice differs from baseline
		e.mu.Lock()
		firstDetection := !e.st.Alerting
		e.st.Alerting = true
		e.st.ChangedText = notice
		e.lastResult = "notice CHANGED"
		e.mu.Unlock()
		e.saveState()

		if firstDetection {
			e.log.Info("change detected", "new_notice", notice)
			e.emit("change", "good", "CHANGE DETECTED! New notice: "+snippet(notice, 200))
			e.sendChangeAlert(ctx, true)
			e.armAlert()
		} else {
			e.emit("check", "good", "Still changed — notice remains different from baseline")
		}
	}
}

// sendChangeAlert sends the emergency-priority "page changed" alert. It is
// called on first detection, on resume, and by the alert ticker; each path
// re-verifies that alerting is still active.
func (e *Engine) sendChangeAlert(ctx context.Context, first bool) {
	e.mu.Lock()
	alerting := e.st.Alerting
	changed := e.st.ChangedText
	e.mu.Unlock()
	if !alerting {
		return
	}

	title := "Berlin service page changed!"
	if !first {
		title = "Reminder: Berlin service page changed"
	}
	sent := e.sendNotification(ctx, notify.Message{
		Title:    title,
		Body:     "The notice on " + e.targetURL + " changed. Current text:\n\n" + snippet(changed, 500) + "\n\nPress 's' in the app to stop these alerts.",
		Priority: notify.PriorityEmergency,
		Sound:    notify.SoundPositive,
	})
	if sent {
		e.mu.Lock()
		e.alertsSent++
		e.mu.Unlock()
	}
}

func (e *Engine) handleStopAlerts() {
	e.mu.Lock()
	if !e.st.Alerting {
		e.mu.Unlock()
		e.emit("action", "info", "Stop alerts: no active alert")
		return
	}
	e.st.Alerting = false
	// ChangedText is never empty while alerting (the checker returns
	// sentinels for missing/empty blocks), so adopt it unconditionally.
	e.st.Baseline = e.st.ChangedText
	e.st.ChangedText = ""
	e.mu.Unlock()

	e.disarmAlert()
	e.log.Info("action: stop alerts, new baseline adopted")
	e.emit("action", "info", "Alerts stopped. Current notice adopted as new baseline.")
	e.saveState()
}

// sendNotification delivers m, emitting success/failure events. It reports
// whether the message was accepted by the API.
func (e *Engine) sendNotification(ctx context.Context, m notify.Message) bool {
	nctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := e.notifier.Send(nctx, m); err != nil {
		e.emit("notify", "error", "Notification FAILED: "+err.Error())
		return false
	}
	e.emit("notify", "good", "Notification sent: "+m.Title)
	return true
}

func (e *Engine) saveState() {
	e.mu.Lock()
	st := e.st
	e.mu.Unlock()
	if err := e.store.SaveState(st); err != nil {
		e.log.Error("saving state failed", "err", err)
		e.emit("info", "error", "Saving state failed: "+err.Error())
	}
}

// emit persists the event to history and forwards it to the TUI.
func (e *Engine) emit(kind, level, msg string) {
	ev := store.Event{Time: time.Now(), Kind: kind, Level: level, Message: msg}
	if err := e.store.AppendEvent(ev); err != nil {
		e.log.Error("appending history failed", "err", err)
	}
	e.mu.Lock()
	fn := e.onEvent
	e.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

func (e *Engine) setNextCheck(t time.Time) {
	e.mu.Lock()
	e.nextCheckAt = t
	e.mu.Unlock()
}

func (e *Engine) setNextAlert(t time.Time) {
	e.mu.Lock()
	e.nextAlertAt = t
	e.mu.Unlock()
}

func (e *Engine) setCheckRunning(v bool) {
	e.mu.Lock()
	e.checkRunning = v
	e.mu.Unlock()
}

// snippet truncates s (by runes, the text is German) for use in
// notifications and history lines.
func snippet(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}
