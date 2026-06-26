package tui

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/render"
)

// viewReview renders the answer summary plus the two files that would be written.
func (m model) viewReview() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Review — your answers") + "\n\n")

	a := m.answers
	row := func(k, v string) { b.WriteString(fmt.Sprintf("  %-12s %s\n", k+":", v)) }
	row("Posture", string(a.Posture))
	row("Sizing", string(a.Sizing))
	row("SSO", string(a.SSO))
	if a.OIDCIssuer != "" {
		row("OIDC issuer", a.OIDCIssuer)
	}
	row("Secrets", string(a.Secrets))
	row("Domain", a.Domain)
	row("Flavor", string(render.FlavorFor(a.Posture)))

	services := a.Services
	if len(services) == 0 {
		services = []string{"(required only)"}
	}
	row("Services", strings.Join(services, ", "))

	b.WriteString("\n" + descStyle.Render("  rendered files (preview):") + "\n")
	if files, err := render.Render(a, m.cat); err != nil {
		b.WriteString(failStyle.Render("  render error: "+err.Error()) + "\n")
	} else {
		for _, f := range files {
			b.WriteString("\n" + titleStyle.Render("  "+f.Name) + "\n")
			b.WriteString(indent(f.Content, "  ") + "\n")
		}
	}

	b.WriteString("\n" + helpStyle.Render("enter continue to deploy · b back · q quit") + "\n")
	return b.String()
}

// viewDeploy renders the deploy-stub screen — the command the wizard would run.
func (m model) viewDeploy() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Deploy") + "\n\n")
	b.WriteString(warnStyle.Render("  [stub] deploy is not yet implemented.") + "\n\n")
	b.WriteString("  On enter, srectl would render the files and run:\n\n")
	b.WriteString("    " + selectedStyle.Render(
		fmt.Sprintf("uds deploy <sre-bundle> --confirm --set-file config=./%s", render.UDSConfigFile)) + "\n")
	b.WriteString("\n  (the CLI writes the files to --out; this wizard only previews them)\n")
	b.WriteString("\n" + helpStyle.Render("enter finish · b back · q quit") + "\n")
	return b.String()
}

// indent prefixes every line of s with prefix.
func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
