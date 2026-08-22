package tui

import "github.com/charmbracelet/lipgloss"

// Palette loosely modeled on OpenCode's look: dark, muted borders, a warm
// peach accent for the focused element.
var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#d97706", Dark: "#fab283"}
	colorBorder = lipgloss.AdaptiveColor{Light: "#b8b8b8", Dark: "#3b4261"}
	colorDim    = lipgloss.AdaptiveColor{Light: "#787878", Dark: "#6b7089"}
	colorText   = lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#c8d3f5"}
	colorGood   = lipgloss.AdaptiveColor{Light: "#0a7d33", Dark: "#95e6cb"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#ffd173"}
	colorErr    = lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f28779"}
	colorBarBg  = lipgloss.AdaptiveColor{Light: "#e8e8e8", Dark: "#1f2430"}
)

type styles struct {
	panel        lipgloss.Style
	panelFocused lipgloss.Style
	title        lipgloss.Style
	titleFocused lipgloss.Style

	dim    lipgloss.Style
	text   lipgloss.Style
	good   lipgloss.Style
	warn   lipgloss.Style
	errs   lipgloss.Style
	accent lipgloss.Style

	badgeOK      lipgloss.Style
	badgeChanged lipgloss.Style
	badgeFailing lipgloss.Style

	actionRow      lipgloss.Style
	actionSelected lipgloss.Style

	statusBar     lipgloss.Style
	statusSection lipgloss.Style
	helpBox       lipgloss.Style
}

func newStyles() styles {
	badge := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	return styles{
		panel: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1),
		panelFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1),
		title:        lipgloss.NewStyle().Foreground(colorDim).Bold(true),
		titleFocused: lipgloss.NewStyle().Foreground(colorAccent).Bold(true),

		dim:    lipgloss.NewStyle().Foreground(colorDim),
		text:   lipgloss.NewStyle().Foreground(colorText),
		good:   lipgloss.NewStyle().Foreground(colorGood),
		warn:   lipgloss.NewStyle().Foreground(colorWarn),
		errs:   lipgloss.NewStyle().Foreground(colorErr),
		accent: lipgloss.NewStyle().Foreground(colorAccent),

		badgeOK:      badge.Foreground(lipgloss.Color("#0d1117")).Background(colorGood),
		badgeChanged: badge.Foreground(lipgloss.Color("#0d1117")).Background(colorErr),
		badgeFailing: badge.Foreground(lipgloss.Color("#0d1117")).Background(colorWarn),

		actionRow: lipgloss.NewStyle().Foreground(colorText).Padding(0, 1),
		actionSelected: lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true).
			Padding(0, 1),

		statusBar:     lipgloss.NewStyle().Background(colorBarBg).Foreground(colorDim).Padding(0, 1),
		statusSection: lipgloss.NewStyle().Foreground(colorText).Bold(true),
		helpBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1),
	}
}
