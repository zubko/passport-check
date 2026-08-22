# passport-check

A terminal app that watches the Berlin naturalization service page
(<https://service.berlin.de/dienstleistung/318998/>) for changes to its
service-notice text and alerts your phone via [Pushover](https://pushover.net).

- Checks the page **every hour** (configurable).
- When the notice text **changes** (e.g. the disruption message is removed),
  it sends an **emergency-priority** Pushover alert (sound `good-1`) and
  repeats it **every 10 minutes** until you press `s` (Stop alerts) or quit.
- If the old error notice **comes back**, it sends a single notification
  (sound `alert-2`) and stops alerting.
- If **3 consecutive checks fail** (network/site down), it sends one
  notification, and another when checks recover.
- State and history survive restarts (`state.json`, `history.jsonl`);
  restarting during an active alert re-alerts immediately. Detailed logs
  go to `app.log`. After an app upgrade that changes the state schema,
  `state.json` is discarded automatically and the baseline re-captures on
  the next check (history is kept).

## Setup

1. Install Go 1.24+.
2. Create a Pushover **application** at <https://pushover.net/apps/build>
   and note its **API token**. Your **user key** is on the Pushover
   dashboard.
3. Configure secrets:

   ```sh
   cp .env.example .env
   # edit .env: set PUSHOVER_TOKEN and PUSHOVER_USER
   ```

4. Run:

   ```sh
   make run
   ```

To keep it running around the clock, start it inside `tmux` (or `screen`):

```sh
tmux new -s passport-check 'make run'
# detach: Ctrl-b d   —   reattach: tmux attach -t passport-check
```

## Using the TUI

Three panels — **Status** (state, countdowns), **Actions**, **History**
(scrollable event log). The bottom bar shows the current state, active
panel, and key hints.

| Key | Action |
| --- | --- |
| `Tab` / `→`, `Shift+Tab` / `←` | switch panel |
| `j` / `k` (or `↓` / `↑`) | scroll focused panel / move in Actions |
| `g` / `G` | jump to top / bottom of a scrollable panel |
| `Enter` | run the selected action |
| `c` | check now (resets the hourly countdown) |
| `s` | stop alerts (adopts the new text as baseline) |
| `t` | send a `[TEST]` change alert (emergency priority) |
| `f` | send a `[TEST]` error-is-back notification |
| `?` | toggle full help |
| `q` / `Ctrl+C` | quit |

## Configuration

All settings live in `.env` (see `.env.example`): `PUSHOVER_TOKEN`,
`PUSHOVER_USER` (required); `CHECK_INTERVAL`, `ALERT_INTERVAL`,
`LOG_LEVEL`, `TARGET_URL`, and file paths (optional).

## Development

```sh
make test   # unit tests
make lint   # golangci-lint
make vet
```
