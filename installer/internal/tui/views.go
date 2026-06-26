package tui

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/preflight"
)

// renderSingleSelect renders a titled single-select menu with a cursor and a
// per-row help line, returning the assembled screen text.
func renderSingleSelect[T any](title string, opts []option[T], cursor int) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title) + "\n\n")
	for i, o := range opts {
		marker := "  "
		label := o.label
		if i == cursor {
			marker = cursorStyle.Render("▸ ")
			label = selectedStyle.Render(o.label)
		}
		b.WriteString(fmt.Sprintf("%s%s\n", marker, label))
		b.WriteString("    " + descStyle.Render(o.help) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render(navHelp) + "\n")
	return b.String()
}

// statusStyle maps a preflight status to its colour style.
func statusStyle(s preflight.Status) string {
	switch s {
	case preflight.StatusPass:
		return passStyle.Render(string(s))
	case preflight.StatusWarn:
		return warnStyle.Render(string(s))
	default:
		return failStyle.Render(string(s))
	}
}

// viewPreflight renders the host-readiness screen.
func (m model) viewPreflight() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Preflight — host readiness") + "\n\n")
	for _, r := range m.report.Results {
		b.WriteString(fmt.Sprintf("  %-7s %-14s %s\n", statusStyle(r.Status), r.Name, r.Detail))
		if r.Remediation != "" {
			b.WriteString("          " + descStyle.Render(r.Remediation) + "\n")
		}
	}
	b.WriteString(fmt.Sprintf("\n  %d passed, %d warnings, %d failed\n",
		m.report.Passes(), m.report.Warns(), m.report.Fails()))
	if !m.report.OK() {
		b.WriteString(warnStyle.Render("  failing checks should be fixed before deploy") + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("enter continue · q quit") + "\n")
	return b.String()
}

// viewPosture renders the posture single-select screen.
func (m model) viewPosture() string {
	return renderSingleSelect("Posture — security profile", postureOptions, m.cursor)
}

// viewSizing renders the sizing single-select screen.
func (m model) viewSizing() string {
	return renderSingleSelect("Sizing — resource envelope", sizingOptions, m.cursor)
}

// viewSSO renders the SSO single-select screen.
func (m model) viewSSO() string {
	return renderSingleSelect("SSO — identity provider", ssoOptions, m.cursor)
}

// viewSecrets renders the secrets single-select screen.
func (m model) viewSecrets() string {
	return renderSingleSelect("Secrets — management mode", secretsOptions, m.cursor)
}

// viewServices renders the multi-select services screen. Required packages are
// listed (locked on) for context; optional ones are toggleable.
func (m model) viewServices() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Core services — packages to deploy") + "\n\n")

	b.WriteString(descStyle.Render("  required (always deployed):") + "\n")
	for _, e := range m.cat.Required() {
		b.WriteString(fmt.Sprintf("    %s %s — %s\n", selectedStyle.Render("[x]"), e.ID, e.Description))
	}

	b.WriteString("\n" + descStyle.Render("  optional:") + "\n")
	for i, e := range m.cat.Optional() {
		box := "[ ]"
		if m.selected[i] {
			box = selectedStyle.Render("[x]")
		}
		marker := "  "
		if i == m.cursor {
			marker = cursorStyle.Render("▸ ")
		}
		pending := ""
		if e.Pending() {
			pending = warnStyle.Render(" (packaging pending)")
		}
		b.WriteString(fmt.Sprintf("  %s%s %s%s — %s\n", marker, box, e.ID, pending, e.Description))
	}
	b.WriteString("\n" + helpStyle.Render(navHelp) + "\n")
	return b.String()
}
