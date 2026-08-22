package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap defines every binding the TUI reacts to. It implements
// help.KeyMap for the bubbles help component.
type keyMap struct {
	NextPanel   key.Binding
	PrevPanel   key.Binding
	ScrollDown  key.Binding
	ScrollUp    key.Binding
	Top         key.Binding
	Bottom      key.Binding
	MoveDown    key.Binding
	MoveUp      key.Binding
	Select      key.Binding
	CheckNow    key.Binding
	StopAlerts  key.Binding
	TestSuccess key.Binding
	TestFailure key.Binding
	Help        key.Binding
	Quit        key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		NextPanel: key.NewBinding(
			key.WithKeys("tab", "right"),
			key.WithHelp("tab/→", "next panel"),
		),
		PrevPanel: key.NewBinding(
			key.WithKeys("shift+tab", "left"),
			key.WithHelp("shift+tab/←", "previous panel"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/↓", "scroll down"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/↑", "scroll up"),
		),
		Top: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", "go to top"),
		),
		Bottom: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "go to bottom"),
		),
		MoveDown: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/↓", "next action"),
		),
		MoveUp: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/↑", "previous action"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "run action"),
		),
		CheckNow: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "check now"),
		),
		StopAlerts: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "stop alerts"),
		),
		TestSuccess: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "test change alert"),
		),
		TestFailure: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "test error-back alert"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "toggle help"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
	}
}

// ShortHelp is shown in the status bar area when full help is hidden.
// Action keys come first: on narrow terminals the tail is truncated, and
// c/s must stay visible (s is how alerts are stopped).
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.CheckNow, k.StopAlerts, k.NextPanel, k.ScrollDown, k.Help, k.Quit}
}

// FullHelp is shown when help is toggled with '?'.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextPanel, k.PrevPanel, k.Select},
		{k.ScrollDown, k.ScrollUp, k.Top, k.Bottom},
		{k.CheckNow, k.StopAlerts, k.TestSuccess, k.TestFailure},
		{k.Help, k.Quit},
	}
}
