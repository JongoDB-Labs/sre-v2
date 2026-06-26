package tui

import tea "github.com/charmbracelet/bubbletea"

// maxCursor returns the highest valid cursor index for the active screen.
func (m model) maxCursor() int {
	switch m.step {
	case stepPosture:
		return len(postureOptions) - 1
	case stepSizing:
		return len(sizingOptions) - 1
	case stepServices:
		return len(m.cat.Optional()) - 1
	case stepSSO:
		return len(ssoOptions) - 1
	case stepSecrets:
		return len(secretsOptions) - 1
	default:
		// Preflight, review, and deploy screens have no movable cursor.
		return 0
	}
}

// back moves to the previous step (preflight is the first; deploy/done are
// terminal and do not go back), resetting the cursor.
func (m model) back() model {
	if m.step > stepPreflight && m.step < stepDeploy {
		m.step--
		m.cursor = 0
	}
	return m
}

// toggle flips the selection on the services screen; it is a no-op elsewhere.
func (m model) toggle() model {
	if m.step == stepServices {
		m.selected[m.cursor] = !m.selected[m.cursor]
	}
	return m
}

// advance commits the active screen's selection into the answers and moves to
// the next step. On the deploy screen it marks the wizard finished and quits.
func (m model) advance() (tea.Model, tea.Cmd) {
	switch m.step {
	case stepPosture:
		m.answers.Posture = postureOptions[m.cursor].value
	case stepSizing:
		m.answers.Sizing = sizingOptions[m.cursor].value
	case stepServices:
		m.answers.Services = m.selectedServiceIDs()
	case stepSSO:
		m.answers.SSO = ssoOptions[m.cursor].value
	case stepSecrets:
		m.answers.Secrets = secretsOptions[m.cursor].value
	case stepDeploy:
		m.finished = true
		return m, tea.Quit
	}

	if m.step < stepDeploy {
		m.step++
		m.cursor = 0
	}
	return m, nil
}

// selectedServiceIDs returns the catalog IDs of the optional services the user
// checked, in catalog order.
func (m model) selectedServiceIDs() []string {
	opt := m.cat.Optional()
	var ids []string
	for i, e := range opt {
		if m.selected[i] {
			ids = append(ids, e.ID)
		}
	}
	return ids
}
