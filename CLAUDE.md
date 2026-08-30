# passport-check

Long-running Go TUI app that watches https://service.berlin.de/dienstleistung/318998/
for changes to its service-notice text and sends Pushover notifications:
repeated high-priority alerts every 10 minutes while the notice differs from the
baseline, and a single notification when the error notice returns.

## Hard rules

- **NEVER read, cat, grep, print, or otherwise access the `.env` file.** It
  contains Pushover secrets. Only the running app loads it (via godotenv).
  If env config needs changing, edit `.env.example` and ask the user to
  update their `.env` themselves.
- Never log or echo `PUSHOVER_TOKEN` / `PUSHOVER_USER` values.

## Commands

- `make build` / `make run` — build/run the TUI (binary: `./passport-check`)
- `make test` — run all tests
- `make lint` — golangci-lint (falls back to `go run` if not installed)
- `make vet`, `make fmt`, `make tidy`
- Go lives at `/usr/local/go/bin` on this machine (may not be on PATH).

## Architecture

- `cmd/passport-check/main.go` — wiring: config → logger → store → engine → Bubble Tea program; engine runs in a goroutine, events reach the TUI via `program.Send`.
- `internal/config` — env/.env loading and validation.
- `internal/checker` — HTTP GET (5MB body cap) + goquery extraction of ALL notice blocks (`#layout-grid__area--maincontent div.message`), whitespace-normalized and joined in page order. `StripVolatile` then removes the site's `[Stand: ...]` freshness marker (any block, case-insensitive) so a republish that only bumps that timestamp is not a change — **anything added there changes the persisted notice format and must bump `store.CurrentSchemaVersion`**. Missing block → sentinel `NoticeMissing`; present-but-empty (or left empty by stripping) → `NoticeEmpty`. ExtractNotice never returns `""` — the engine reserves `""` for "no baseline captured yet".
- `internal/notify` — minimal Pushover REST client. Change alerts use priority 1 (high) — not emergency — because the engine already repeats them every `ALERT_INTERVAL`; stacking Pushover's own acknowledge-or-retry loop on top would double the nagging. Per-message `sound`: `good-1` (`SoundPositive`) for change alerts, `alert-2` (`SoundNegative`) for revert notifications; fail/recover notifications use the device default.
- `internal/engine` — state machine. Baseline notice text persisted; any difference → CHANGED, high-priority alert repeated every `ALERT_INTERVAL` until "Stop alerts" (adopts new text as baseline) or the text reverts to the old baseline (single priority-0 notification). 3 consecutive fetch failures → single notification (retried on later failures if the send itself failed); recovery → single notification. Resuming with an active alert sends an alert immediately so restarts never silence the cadence. State is saved on every completed check (incl. the unchanged path); a check aborted by shutdown is not recorded. `Snapshot` embeds `store.State` and is composed at read time — persisted state has a single source of truth (`Engine.st`).
- `internal/store` — `state.json` (atomic rewrite) + `history.jsonl` (append-only event log, reloaded into the history panel on startup). `state.json` carries a `schema_version` (`store.CurrentSchemaVersion`): **this app never migrates state**. On version mismatch or an unreadable file, `LoadState` discards the state automatically (baseline re-captures on the next check) and the reset is logged and shown in the History panel. **Any change to the `store.State` shape or to the semantics of persisted values (e.g. the notice-text format) must bump `CurrentSchemaVersion` — never write a migration.** History is unversioned by design (events are display-only and forward-tolerant).
- `internal/tui` — Bubble Tea model: Status / Actions / History panels, Tab/arrows switch focus, j/k scroll, 1-second tick drives countdowns, `?` help, status bar.
- `internal/logging` — slog → rotating `app.log` (lumberjack). Stdout belongs to the TUI; never print to it.

## Runtime files (gitignored)

`.env` (secrets), `state.json`, `history.jsonl`, `app.log*`, `passport-check` binary.

## Testing notes

- Checker tests parse `internal/checker/testdata/page.html` (a saved snapshot of the live page).
- Notify tests use `httptest.Server` via `notify.NewWithURL`.
- Engine tests use stub Fetcher/Notifier implementations; the engine is driven by calling exported methods and `Run` with short intervals.
- To manually simulate a change: serve a modified copy of the fixture with `python3 -m http.server` and set `TARGET_URL` accordingly.
