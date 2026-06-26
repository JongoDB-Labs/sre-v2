// Package tui implements the bubbletea install wizard: a linear flow stepping
// preflight -> posture -> sizing -> services -> SSO -> secrets -> review ->
// deploy. The deploy step is a stub that prints the `uds deploy` command it
// would run. The wizard shares the config, catalog, preflight, and render
// packages with the CLI so both surfaces capture and render identical answers.
package tui

import "github.com/charmbracelet/lipgloss"

// Shared lipgloss styles for the wizard screens. Kept minimal and functional —
// the goal is a clear flow, not a showcase.
var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle     = lipgloss.NewStyle().Faint(true)
	cursorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
	passStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	descStyle     = lipgloss.NewStyle().Faint(true)
)

// navHelp is the one-line key hint shown on every screen.
const navHelp = "↑/↓ move · space toggle · enter next · b back · q quit"
