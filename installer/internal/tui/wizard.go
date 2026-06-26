package tui

import (
	"fmt"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/preflight"
	tea "github.com/charmbracelet/bubbletea"
)

// step identifies a wizard screen, in flow order.
type step int

const (
	stepPreflight step = iota
	stepPosture
	stepSizing
	stepServices
	stepSSO
	stepSecrets
	stepReview
	stepDeploy
	stepDone
)

// model is the bubbletea wizard state: the current step, the host preflight
// report, the answers built so far, and the per-screen cursor/selection.
type model struct {
	cat     *catalog.Catalog
	report  preflight.Report
	answers config.Answers

	step   step
	cursor int
	// selected tracks multi-select state for the services screen, keyed by index
	// into the optional-services list.
	selected map[int]bool

	quit     bool
	finished bool
	err      error
}

// newModel builds the initial wizard model, seeded with default answers and a
// fresh preflight report.
func newModel(cat *catalog.Catalog) model {
	answers := config.Default()
	m := model{
		cat:      cat,
		report:   preflight.Run(),
		answers:  answers,
		step:     stepPreflight,
		selected: map[int]bool{},
	}
	// Pre-check the services already implied by the default answers.
	for i, e := range cat.Optional() {
		if answers.HasService(e.ID) {
			m.selected[i] = true
		}
	}
	return m
}

// RunWizard launches the interactive wizard and returns the captured answers.
// It returns an error if the user quits before finishing or if the TUI fails.
func RunWizard(cat *catalog.Catalog) (*config.Answers, error) {
	final, err := tea.NewProgram(newModel(cat)).Run()
	if err != nil {
		return nil, fmt.Errorf("tui: run wizard: %w", err)
	}
	m, ok := final.(model)
	if !ok {
		return nil, fmt.Errorf("tui: unexpected final model type %T", final)
	}
	if m.err != nil {
		return nil, m.err
	}
	if !m.finished {
		return nil, fmt.Errorf("tui: wizard cancelled")
	}
	return &m.answers, nil
}

// Init implements tea.Model. The wizard is driven entirely by key events, so it
// has no startup command.
func (m model) Init() tea.Cmd { return nil }

// Update implements tea.Model: it routes key events to navigation and the active
// screen's handler.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch key.String() {
	case "ctrl+c", "q":
		m.quit = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < m.maxCursor() {
			m.cursor++
		}
		return m, nil
	case "b":
		return m.back(), nil
	case " ":
		return m.toggle(), nil
	case "enter":
		return m.advance()
	}
	return m, nil
}

// View implements tea.Model: it renders the active screen.
func (m model) View() string {
	if m.quit && !m.finished {
		return "cancelled.\n"
	}
	switch m.step {
	case stepPreflight:
		return m.viewPreflight()
	case stepPosture:
		return m.viewPosture()
	case stepSizing:
		return m.viewSizing()
	case stepServices:
		return m.viewServices()
	case stepSSO:
		return m.viewSSO()
	case stepSecrets:
		return m.viewSecrets()
	case stepReview:
		return m.viewReview()
	case stepDeploy, stepDone:
		return m.viewDeploy()
	}
	return ""
}
