package wizard

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/preflight"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/render"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/rivo/tview"
)

// radioOption pairs a stored answer value with its on-screen label + help.
type radioOption[T any] struct {
	value T
	label string
	help  string
}

var postureOptions = []radioOption[config.Posture]{
	{config.PostureBaseline, "Baseline", "upstream images, relaxed defaults (lab / connected)"},
	{config.PostureDoD, "DoD-hardened", "registry1 (Iron Bank) images, FIPS, strict netpol (ATO)"},
}

var sizingOptions = []radioOption[config.Sizing]{
	{config.SizingSmall, "Small", "single-node lab, slim Core (~4 vCPU / 16 GiB)"},
	{config.SizingMedium, "Medium", "full UDS Core (12+ vCPU / 32+ GiB)"},
	{config.SizingLarge, "Large", "HA, production-shaped envelope"},
}

var ssoOptions = []radioOption[config.SSOMode]{
	{config.SSOKeycloak, "Keycloak", "deploy the bundled Keycloak IdP (core-identity-authorization)"},
	{config.SSOExternalOIDC, "External OIDC", "point at an existing OIDC provider"},
	{config.SSONone, "None", "no SSO (lab only)"},
}

var secretsOptions = []radioOption[config.SecretsMode]{
	{config.SecretsSOPSAge, "SOPS age", "encrypt secrets with a SOPS age key (default)"},
	{config.SecretsExternal, "External", "defer to an external secrets manager"},
}

// radioScreen builds a single-select list (whiptail radiolist). Moving the
// highlight is navigation; Enter selects the row (onPick) and advances.
func radioScreen[T any](title string, opts []radioOption[T], current T, onPick func(T)) tview.Primitive {
	list := tview.NewList().ShowSecondaryText(true)
	for i, o := range opts {
		o := o
		shortcut := rune('1' + i)
		list.AddItem(o.label, o.help, shortcut, func() { onPick(o.value) })
	}
	// Start the highlight on the current value.
	for i, o := range opts {
		if fmt.Sprint(o.value) == fmt.Sprint(current) {
			list.SetCurrentItem(i)
			break
		}
	}
	return list
}

// checklistScreen builds the optional-services checklist as a Form of checkboxes
// plus Continue/Back buttons. Required services are shown read-only above.
func checklistScreen(f *Flow, onNext, onBack func()) tview.Primitive {
	form := tview.NewForm()
	for _, e := range f.cat.Optional() {
		e := e
		label := e.ID
		if e.Pending() {
			label += " (packaging pending)"
		}
		form.AddCheckbox(label, f.ServiceChecked(e.ID), func(checked bool) {
			if checked != f.ServiceChecked(e.ID) {
				f.ToggleService(e.ID)
			}
		})
	}
	form.AddButton("Continue", onNext)
	form.AddButton("Back", onBack)

	required := tview.NewTextView().SetDynamicColors(true)
	var b strings.Builder
	b.WriteString("required (always deployed):\n")
	for _, e := range f.cat.Required() {
		fmt.Fprintf(&b, "  [x] %s — %s\n", e.ID, e.Description)
	}
	required.SetText(b.String())

	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(required, len(f.cat.Required())+1, 0, false).
		AddItem(form, 0, 1, true)
}

// inputScreen builds a single-field Form (domain / OIDC issuer / age key) with
// Continue/Back buttons. onDone receives the field's current text.
func inputScreen(label, value, placeholder string, onDone func(string), onNext, onBack func()) tview.Primitive {
	var current = value
	form := tview.NewForm()
	form.AddInputField(label, value, 0, nil, func(text string) { current = text })
	form.AddButton("Continue", func() { onDone(current); onNext() })
	form.AddButton("Back", onBack)
	if placeholder != "" {
		if fld, ok := form.GetFormItem(0).(*tview.InputField); ok {
			fld.SetPlaceholder(placeholder)
		}
	}
	return form
}

// welcomeScreen shows the intro + a host-preflight summary (run as today) with
// Continue/Quit buttons.
func welcomeScreen(onNext, onQuit func()) tview.Primitive {
	report := preflight.Run()
	tv := tview.NewTextView().SetDynamicColors(true)
	var b strings.Builder
	b.WriteString("Welcome to the SRE substrate installer.\n\n")
	b.WriteString("This wizard captures your install answers and renders the UDS bundle\n")
	b.WriteString("config + Helm overlay that drive the deploy.\n\n")
	b.WriteString("Host preflight:\n")
	for _, r := range report.Results {
		fmt.Fprintf(&b, "  %-7s %-14s %s\n", r.Status, r.Name, r.Detail)
	}
	fmt.Fprintf(&b, "\n  %d passed, %d warnings, %d failed\n", report.Passes(), report.Warns(), report.Fails())
	if !report.OK() {
		b.WriteString("\n  failing checks should be fixed before a real deploy.\n")
	}
	tv.SetText(b.String())

	form := tview.NewForm().
		AddButton("Continue", onNext).
		AddButton("Quit", onQuit)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(form, 3, 0, true)
}

// reviewScreen shows the answer summary + the two rendered files, with
// Deploy/Back buttons.
func reviewScreen(f *Flow, cat *catalog.Catalog, onNext, onBack func()) tview.Primitive {
	tv := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	a := f.Answers()
	var b strings.Builder
	row := func(k, v string) { fmt.Fprintf(&b, "  %-12s %s\n", k+":", v) }
	row("Posture", string(a.Posture))
	row("Sizing", string(a.Sizing))
	row("Domain", a.Domain)
	row("SSO", string(a.SSO))
	if a.OIDCIssuer != "" {
		row("OIDC issuer", a.OIDCIssuer)
	}
	row("Secrets", string(a.Secrets))
	row("Flavor", string(render.FlavorFor(a.Posture)))
	services := a.Services
	if len(services) == 0 {
		services = []string{"(required only)"}
	}
	row("Services", strings.Join(services, ", "))

	b.WriteString("\nrendered files (preview):\n")
	if files, err := render.Render(a, cat); err != nil {
		fmt.Fprintf(&b, "  render error: %s\n", err)
	} else {
		for _, file := range files {
			fmt.Fprintf(&b, "\n# ---- %s ----\n%s\n", file.Name, file.Content)
		}
	}
	tv.SetText(b.String())

	form := tview.NewForm().
		AddButton("Deploy", onNext).
		AddButton("Back", onBack)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(form, 3, 0, true)
}

// deployScreen is the deploy-stub screen (parity with today's CLI stub) with
// Finish/Back buttons.
func deployScreen(onFinish, onBack func()) tview.Primitive {
	tv := tview.NewTextView().SetDynamicColors(true)
	tv.SetText("[stub] deploy is not yet implemented.\n\n" +
		"On Finish, srectl writes the rendered files to --out; the real\n" +
		"`uds deploy` orchestration is wired in build-order step 2.\n")
	form := tview.NewForm().
		AddButton("Finish", onFinish).
		AddButton("Back", onBack)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(form, 3, 0, true)
}

// dialogTitle composes the border title for a wizard screen.
func dialogTitle(version, screen string) string {
	return tui.Title("SRE Setup", version) + " · " + screen
}
