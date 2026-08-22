// Package tui implements the terminal UI: a status panel with countdowns,
// an actions panel, a scrollable history panel, and a status bar.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"zubko.io/passport-check/internal/engine"
	"zubko.io/passport-check/internal/store"
)

type panel int

const (
	panelStatus panel = iota
	panelActions
	panelHistory
	panelCount
)

func (p panel) String() string {
	switch p {
	case panelStatus:
		return "Status"
	case panelActions:
		return "Actions"
	case panelHistory:
		return "History"
	default:
		return "?"
	}
}

// EventMsg wraps an engine event delivered via Program.Send.
type EventMsg struct{ Event store.Event }

type tickMsg time.Time

type action struct {
	name string
	hint string
	run  func()
}

// Model is the root Bubble Tea model.
type Model struct {
	eng    *engine.Engine
	keys   keyMap
	styles styles
	help   help.Model

	width, height int
	ready         bool
	focus         panel
	showHelp      bool

	snap engine.Snapshot

	statusVP  viewport.Model
	historyVP viewport.Model

	actions   []action
	actionIdx int

	events []store.Event
}

// New builds the TUI model. history seeds the history panel (oldest first).
func New(eng *engine.Engine, history []store.Event) Model {
	m := Model{
		eng:    eng,
		keys:   newKeyMap(),
		styles: newStyles(),
		help:   help.New(),
		focus:  panelActions,
		snap:   eng.Snapshot(),
		events: history,
	}
	m.actions = []action{
		{"Check now", "run a check immediately; resets the countdown", eng.CheckNow},
		{"Stop alerts", "acknowledge the change and stop repeating alerts", eng.StopAlerts},
		{"Test: change alert", "send a [TEST] emergency notification", eng.TestSuccess},
		{"Test: error-is-back", "send a [TEST] normal notification", eng.TestFailure},
		{"Quit", "exit the app (stops all checking)", nil},
	}
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		m.layout()
		m.ready = true
		return m, nil

	case tickMsg:
		m.snap = m.eng.Snapshot()
		m.refreshStatusContent()
		return m, tick()

	case EventMsg:
		m.events = append(m.events, msg.Event)
		if len(m.events) > maxEvents {
			m.events = m.events[len(m.events)-maxEvents:]
		}
		m.snap = m.eng.Snapshot()
		wasAtBottom := m.historyVP.AtBottom()
		m.refreshHistoryContent()
		if wasAtBottom {
			m.historyVP.GotoBottom()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.showHelp = !m.showHelp
		m.layout()
		return m, nil
	case key.Matches(msg, m.keys.NextPanel):
		m.focus = (m.focus + 1) % panelCount
		return m, nil
	case key.Matches(msg, m.keys.PrevPanel):
		m.focus = (m.focus + panelCount - 1) % panelCount
		return m, nil
	case key.Matches(msg, m.keys.CheckNow):
		m.eng.CheckNow()
		return m, nil
	case key.Matches(msg, m.keys.StopAlerts):
		m.eng.StopAlerts()
		return m, nil
	case key.Matches(msg, m.keys.TestSuccess):
		m.eng.TestSuccess()
		return m, nil
	case key.Matches(msg, m.keys.TestFailure):
		m.eng.TestFailure()
		return m, nil
	}

	switch m.focus {
	case panelActions:
		switch {
		case key.Matches(msg, m.keys.MoveDown):
			m.actionIdx = (m.actionIdx + 1) % len(m.actions)
		case key.Matches(msg, m.keys.MoveUp):
			m.actionIdx = (m.actionIdx + len(m.actions) - 1) % len(m.actions)
		case key.Matches(msg, m.keys.Select):
			a := m.actions[m.actionIdx]
			if a.run == nil { // Quit
				return m, tea.Quit
			}
			a.run()
		}
	case panelHistory:
		m.historyVP = m.scrollViewport(m.historyVP, msg)
	case panelStatus:
		m.statusVP = m.scrollViewport(m.statusVP, msg)
	}
	return m, nil
}

func (m Model) scrollViewport(vp viewport.Model, msg tea.KeyMsg) viewport.Model {
	switch {
	case key.Matches(msg, m.keys.ScrollDown):
		vp.ScrollDown(1)
	case key.Matches(msg, m.keys.ScrollUp):
		vp.ScrollUp(1)
	case key.Matches(msg, m.keys.Top):
		vp.GotoTop()
	case key.Matches(msg, m.keys.Bottom):
		vp.GotoBottom()
	}
	return vp
}

// layout recomputes panel dimensions from the window size.
func (m *Model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	topH, historyH := m.heights()
	statusW, _ := m.topWidths()

	// Interior sizes: subtract 2 for borders, 2 for horizontal padding,
	// and 1 for the title line.
	m.statusVP.Width = max(1, statusW-4)
	m.statusVP.Height = max(1, topH-3)
	m.historyVP.Width = max(1, m.width-4)
	m.historyVP.Height = max(1, historyH-3)

	m.refreshStatusContent()
	m.refreshHistoryContent()
	m.historyVP.GotoBottom()
}

const (
	topRowHeight    = 13
	statusBarHeight = 1
	helpBoxHeight   = 6
	minPanelHeight  = 3
	maxEvents       = 1000
)

// helpVisible reports whether the toggled help box actually fits; on very
// small terminals it is suppressed rather than overflowing the frame.
func (m Model) helpVisible() bool {
	return m.showHelp && m.height >= 2*minPanelHeight+statusBarHeight+helpBoxHeight
}

// heights computes the vertical layout — the top-row and history panel
// heights — accounting for the status bar and the optional help box. It is
// the single source of truth used by both layout() and View().
func (m Model) heights() (topH, historyH int) {
	helpH := 0
	if m.helpVisible() {
		helpH = helpBoxHeight
	}
	avail := m.height - statusBarHeight - helpH
	topH = topRowHeight
	if avail < topRowHeight+minPanelHeight {
		topH = max(minPanelHeight, avail/2)
	}
	historyH = max(minPanelHeight, avail-topH)
	return topH, historyH
}

func (m Model) topWidths() (statusW, actionsW int) {
	statusW = m.width * 3 / 5
	actionsW = m.width - statusW
	if actionsW < 30 && m.width >= 34 {
		actionsW = 30
		statusW = m.width - actionsW
	}
	return statusW, actionsW
}

// --- content rendering ---

func (m *Model) refreshStatusContent() {
	s := m.styles
	var b strings.Builder

	b.WriteString(m.stateBadge(false) + "\n\n")

	b.WriteString(s.dim.Render("Target      ") + s.text.Render(truncate(m.snap.TargetURL, m.statusVP.Width-12)) + "\n")

	last := "never"
	if !m.snap.LastCheckAt.IsZero() {
		last = m.snap.LastCheckAt.Format("15:04:05")
		if m.snap.LastResult != "" {
			last += "  " + m.snap.LastResult
		}
	}
	if m.snap.CheckRunning {
		last = "checking now…"
	}
	b.WriteString(s.dim.Render("Last check  ") + s.text.Render(truncate(last, m.statusVP.Width-12)) + "\n")

	next := "—"
	if !m.snap.NextCheckAt.IsZero() {
		next = countdown(time.Until(m.snap.NextCheckAt)) + s.dim.Render("  (every "+m.snap.CheckEvery.String()+")")
	}
	b.WriteString(s.dim.Render("Next check  ") + s.accent.Render(next) + "\n")

	if m.snap.Alerting {
		alertNext := "—"
		if !m.snap.NextAlertAt.IsZero() {
			alertNext = countdown(time.Until(m.snap.NextAlertAt))
		}
		b.WriteString(s.dim.Render("Next alert  ") + s.errs.Render(alertNext+"  (every "+m.snap.AlertEvery.String()+", press 's' to stop)") + "\n")
	}

	b.WriteString(s.dim.Render("Checks/Alerts  ") + s.text.Render(fmt.Sprintf("%d checks, %d alerts sent this session", m.snap.ChecksDone, m.snap.AlertsSent)) + "\n")

	if m.snap.FailCount > 0 {
		b.WriteString(s.warn.Render(fmt.Sprintf("Consecutive failures: %d", m.snap.FailCount)) + "\n")
	}

	b.WriteString("\n" + s.dim.Render("Baseline notice:") + "\n")
	if m.snap.Baseline == "" {
		b.WriteString(s.dim.Render("  (not captured yet)") + "\n")
	} else {
		b.WriteString(s.text.Render(wrap(m.snap.Baseline, m.statusVP.Width)) + "\n")
	}
	if m.snap.Alerting && m.snap.ChangedText != "" {
		b.WriteString("\n" + s.errs.Render("Current (changed) notice:") + "\n")
		b.WriteString(s.text.Render(wrap(m.snap.ChangedText, m.statusVP.Width)) + "\n")
	}

	m.statusVP.SetContent(b.String())
}

func (m *Model) refreshHistoryContent() {
	s := m.styles
	if len(m.events) == 0 {
		m.historyVP.SetContent(s.dim.Render("No history yet — the first check runs on startup."))
		return
	}
	lines := make([]string, 0, len(m.events))
	var lastDay string
	for _, ev := range m.events {
		day := ev.Time.Format("2006-01-02")
		if day != lastDay {
			lines = append(lines, s.dim.Render("── "+day+" ──"))
			lastDay = day
		}
		var icon, msg string
		switch ev.Level {
		case "good":
			icon, msg = s.good.Render("✓"), s.good.Render(ev.Message)
		case "warn":
			icon, msg = s.warn.Render("!"), s.warn.Render(ev.Message)
		case "error":
			icon, msg = s.errs.Render("✗"), s.errs.Render(ev.Message)
		default:
			icon, msg = s.dim.Render("·"), s.text.Render(ev.Message)
		}
		ts := s.dim.Render(ev.Time.Format("15:04:05"))
		lines = append(lines, wrapIndent(ts+" "+icon+" "+msg, m.historyVP.Width, 11))
	}
	m.historyVP.SetContent(strings.Join(lines, "\n"))
}

// stateBadge renders the colored status badge; compact yields the short
// form used in the status bar so the key hints keep their space.
func (m Model) stateBadge(compact bool) string {
	long := map[engine.Status]string{
		engine.StatusChanged: "● CHANGED — service notice differs!",
		engine.StatusFailing: "● FAILING — fetch errors",
		engine.StatusOK:      "● OK — watching for changes",
	}
	label := long[m.snap.Status]
	if compact {
		label = "● " + string(m.snap.Status)
	}
	switch m.snap.Status {
	case engine.StatusChanged:
		return m.styles.badgeChanged.Render(label)
	case engine.StatusFailing:
		return m.styles.badgeFailing.Render(label)
	default:
		return m.styles.badgeOK.Render(label)
	}
}

// --- View ---

// View implements tea.Model.
func (m Model) View() string {
	if !m.ready {
		return "loading…"
	}
	s := m.styles

	topH, historyH := m.heights()
	statusW, actionsW := m.topWidths()

	statusPanel := m.renderPanel("Status", m.statusVP.View(), statusW, topH, m.focus == panelStatus)
	actionsPanel := m.renderPanel("Actions", m.renderActions(actionsW-4), actionsW, topH, m.focus == panelActions)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, statusPanel, actionsPanel)

	historyTitle := "History"
	if !m.historyVP.AtBottom() {
		historyTitle = fmt.Sprintf("History (%d%%)", int(m.historyVP.ScrollPercent()*100))
	}
	historyPanel := m.renderPanel(historyTitle, m.historyVP.View(), m.width, historyH, m.focus == panelHistory)

	parts := []string{topRow, historyPanel}
	if m.helpVisible() {
		fullHelp := m.help.FullHelpView(m.keys.FullHelp())
		parts = append(parts, s.helpBox.Width(m.width-2).MaxHeight(helpBoxHeight).Render(fullHelp))
	}
	parts = append(parts, m.renderStatusBar())

	// Never emit more lines than the terminal has — an overflowing frame
	// garbles the whole altscreen.
	return lipgloss.NewStyle().MaxHeight(m.height).Render(
		lipgloss.JoinVertical(lipgloss.Left, parts...))
}

func (m Model) renderPanel(title, content string, width, height int, focused bool) string {
	s := m.styles
	style, titleStyle := s.panel, s.title
	if focused {
		style, titleStyle = s.panelFocused, s.titleFocused
	}
	// Clip content to the panel interior: lipgloss .Height is only a
	// minimum, so overlong content would otherwise grow the panel and
	// push the frame past the terminal height.
	inner := clipLines(titleStyle.Render(title)+"\n"+content, max(1, height-2))
	return style.Width(width - 2).Height(height - 2).MaxHeight(height).Render(inner)
}

// clipLines keeps at most n lines of s.
func clipLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

func (m Model) renderActions(width int) string {
	s := m.styles
	var b strings.Builder
	for i, a := range m.actions {
		cursor := "  "
		rowStyle := s.actionRow
		if i == m.actionIdx {
			cursor = "▸ "
			rowStyle = s.actionSelected
		}
		b.WriteString(rowStyle.Render(cursor+a.name) + "\n")
		if i == m.actionIdx {
			b.WriteString(s.dim.Render("    "+wrapIndent(a.hint, max(20, width-4), 4)) + "\n")
		}
	}
	if m.snap.Alerting {
		b.WriteString("\n" + s.errs.Render("⚠ alerting active"))
	}
	return b.String()
}

func (m Model) renderStatusBar() string {
	s := m.styles
	left := m.stateBadge(true)
	section := s.statusSection.Render(" panel: "+m.focus.String()+" ") + s.statusBar.Render("│ ")

	// Give the key help only the width that actually remains (badge,
	// section, bar padding, one gap cell) so it truncates itself
	// gracefully instead of being clipped mid-hint.
	help := m.help
	help.Width = max(0, m.width-lipgloss.Width(left)-lipgloss.Width(section)-3)
	mid := section + help.ShortHelpView(m.keys.ShortHelp())

	gap := max(1, m.width-lipgloss.Width(left)-lipgloss.Width(mid))
	bar := left + strings.Repeat(" ", gap) + mid
	// MaxWidth truncates ANSI-styled text safely on narrow terminals.
	return s.statusBar.Width(m.width).MaxWidth(m.width).MaxHeight(1).Render(bar)
}

// --- helpers ---

func countdown(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	mm := int(d.Minutes()) % 60
	ss := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, mm, ss)
	}
	return fmt.Sprintf("%02d:%02d", mm, ss)
}

// truncate shortens s to at most width display cells, ANSI-aware. An
// exhausted width budget yields "" rather than the untruncated string.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// wrap word-wraps s to the given display width, ANSI-aware.
func wrap(s string, width int) string {
	if width < 10 {
		width = 10
	}
	return ansi.Wordwrap(s, width, "")
}

// wrapIndent wraps s to width, indenting continuation lines by indent
// spaces. Styled (ANSI) segments are kept intact per word.
func wrapIndent(s string, width, indent int) string {
	if width < 20 {
		return s
	}
	words := strings.Fields(s)
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	lineLen := 0
	for i, w := range words {
		wl := lipgloss.Width(w)
		if lineLen > 0 && lineLen+1+wl > width {
			b.WriteString("\n" + pad)
			lineLen = indent
		} else if i > 0 {
			b.WriteString(" ")
			lineLen++
		}
		b.WriteString(w)
		lineLen += wl
	}
	return b.String()
}
