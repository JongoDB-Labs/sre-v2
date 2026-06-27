package wizard

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/preflight"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/render"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// radioOption pairs a stored answer value with its on-screen label + help.
type radioOption[T any] struct {
	value T
	label string
	help  string
}

var postureOptions = []radioOption[config.Posture]{
	{config.PostureBaseline, "Baseline", "upstream images, relaxed defaults — lab / connected"},
	{config.PostureDoD, "DoD-hardened", "Iron Bank (registry1) images, FIPS, strict netpol — ATO"},
}

var sizingOptions = []radioOption[config.Sizing]{
	{config.SizingSmall, "Small", "single-node lab, slim Core (~4 vCPU / 16 GiB)"},
	{config.SizingMedium, "Medium", "full UDS Core (12+ vCPU / 32+ GiB)"},
	{config.SizingLarge, "Large", "HA, production-shaped envelope"},
}

var ssoOptions = []radioOption[config.SSOMode]{
	{config.SSOKeycloak, "Keycloak", "deploy the bundled Keycloak IdP — the shared sign-in"},
	{config.SSOExternalOIDC, "External OIDC", "point at an OIDC provider you already run"},
	{config.SSONone, "None", "no SSO — consoles use direct access (lab only)"},
}

var secretsOptions = []radioOption[config.SecretsMode]{
	{config.SecretsSOPSAge, "SOPS age", "encrypt secrets with an age key, committed to git (default)"},
	{config.SecretsExternal, "External", "an external secrets manager provisions them out of band"},
}

// screenTitles maps a page id to the human label shown on the dialog border.
var screenTitles = map[string]string{
	"welcome":  "Welcome",
	"posture":  "Security posture",
	"sizing":   "Resource sizing",
	"domain":   "Base domain",
	"services": "Core services",
	"sso":      "Single sign-on",
	"oidc":     "OIDC issuer",
	"secrets":  "Secrets",
	"agekey":   "SOPS age key",
	"review":   "Review",
	"deploy":   "Deploy",
}

// dialogTitle composes the border title: "SRE Setup — <version> · <screen>".
func dialogTitle(version, page string) string {
	label := screenTitles[page]
	if label == "" {
		label = page
	}
	return tui.Title("SRE Setup", version) + " · " + label
}

// screenFrame stacks an intro blurb above the interactive body and a key-hint
// line below it — the consistent layout every wizard screen uses.
func screenFrame(intro string, body tview.Primitive, hint string) tview.Primitive {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	if intro != "" {
		tv := tview.NewTextView().SetDynamicColors(true).SetText(intro)
		flex.AddItem(tv, lineCount(intro), 0, false)
		flex.AddItem(tview.NewBox(), 1, 0, false) // blank line under the intro
	}
	flex.AddItem(body, 0, 1, true)
	if hint != "" {
		flex.AddItem(tview.NewBox(), 1, 0, false) // blank line above the hint
		flex.AddItem(tview.NewTextView().SetDynamicColors(true).SetText(hint), 1, 0, false)
	}
	return flex
}

// lineCount returns the number of text lines in s (1 for a non-empty single line).
func lineCount(s string) int { return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1 }

// radioScreen builds a single-select list (whiptail radiolist). Moving the
// highlight is navigation; Enter selects the row (onPick) and advances.
func radioScreen[T any](intro string, opts []radioOption[T], current T, onPick func(T)) tview.Primitive {
	list := tui.StyleList(tview.NewList().ShowSecondaryText(true))
	for i, o := range opts {
		o := o
		list.AddItem(o.label, "  "+o.help, rune('1'+i), func() { onPick(o.value) })
	}
	for i, o := range opts {
		if fmt.Sprint(o.value) == fmt.Sprint(current) {
			list.SetCurrentItem(i)
			break
		}
	}
	hint := "[#485260]↑/↓ move · 1–9 jump · Enter select · Esc back[-]"
	return screenFrame(intro, list, hint)
}

// checklistScreen builds the optional-services checklist as one navigable list:
// service rows toggle with Space/Enter ([x]/[ ]); the trailing action rows
// advance or go back. Required layers are named in the intro.
func checklistScreen(f *Flow, onNext, onBack func()) tview.Primitive {
	opt := f.cat.Optional()
	list := tui.StyleList(tview.NewList().ShowSecondaryText(true))

	setRow := func(i int) {
		e := opt[i]
		list.SetItemText(i, checkboxLabel(f.ServiceChecked(e.ID), e.Name), "  "+serviceHelp(e))
	}
	for range opt {
		list.AddItem("", "", 0, nil)
	}
	for i := range opt {
		setRow(i)
	}
	list.AddItem("‹ Continue ›", "", 0, onNext)
	list.AddItem("‹ Back ›", "", 0, onBack)

	toggle := func(i int) {
		if i >= 0 && i < len(opt) {
			f.ToggleService(opt[i].ID)
			setRow(i)
		}
	}
	// Space (and Enter on a service row) toggles; Enter on an action row runs it.
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		cur := list.GetCurrentItem()
		onService := cur < len(opt)
		if ev.Key() == tcell.KeyRune && ev.Rune() == ' ' {
			toggle(cur) // no-op on action rows
			return nil
		}
		if ev.Key() == tcell.KeyEnter && onService {
			toggle(cur)
			return nil
		}
		return ev
	})

	intro := "UDS Core layers + data operators to deploy.\n" +
		"[#485260]Always included: " + strings.Join(requiredIDs(f.cat), " · ") + "[-]"
	hint := "[#485260]↑/↓ move · Space toggle · Enter on ‹Continue› · Esc back[-]"
	return screenFrame(intro, list, hint)
}

// inputScreen builds a single-field form (domain / OIDC issuer / age key) with
// Continue/Back buttons. onDone receives the field's current text.
func inputScreen(intro, label, value, placeholder string, width int, onDone func(string), onNext, onBack func()) tview.Primitive {
	current := value
	form := tui.StyleForm(tview.NewForm())
	form.AddInputField(label, value, width, nil, func(text string) { current = text })
	if placeholder != "" {
		if fld, ok := form.GetFormItem(0).(*tview.InputField); ok {
			fld.SetPlaceholder(placeholder).SetPlaceholderTextColor(tui.ColorMuted)
		}
	}
	form.AddButton("Continue", func() { onDone(current); onNext() })
	form.AddButton("Back", onBack)
	hint := "[#485260]Tab/↑↓ move · type to edit · Enter on ‹Continue› · Esc back[-]"
	return screenFrame(intro, form, hint)
}

// welcomeScreen shows the intro + a colour-coded host-readiness summary.
func welcomeScreen(onNext, onQuit func()) tview.Primitive {
	report := preflight.Run()
	tv := tview.NewTextView().SetDynamicColors(true)
	var b strings.Builder
	b.WriteString("This wizard captures your install answers, then renders the UDS bundle\n")
	b.WriteString("config + Helm overlay that drive the deploy.\n\n")
	b.WriteString("[#003080::b]Host readiness[-:-:-]\n")
	for _, r := range report.Results {
		fmt.Fprintf(&b, "  %s  %s [#485260]%s[-]\n", statusTag(r.Status), padRight(r.Name, 13), r.Detail)
	}
	fmt.Fprintf(&b, "\n  %d passed · %d warnings · %d failed", report.Passes(), report.Warns(), report.Fails())
	if report.OK() {
		b.WriteString("   [#1a7f37::b]ready[-:-:-]")
	} else {
		b.WriteString("\n  [#9a6700]failing checks should be fixed before a real deploy[-]")
	}
	tv.SetText(b.String())

	form := tui.StyleForm(tview.NewForm())
	form.AddButton("Continue", onNext)
	form.AddButton("Quit", onQuit)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(form, 3, 0, true)
}

// reviewScreen shows the captured answers and what will be rendered, with
// Deploy/Back buttons. The full files print on a --dry-run; here we summarise.
func reviewScreen(f *Flow, _ *catalog.Catalog, onNext, onBack func()) tview.Primitive {
	a := f.Answers()
	tv := tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	var b strings.Builder
	row := func(k, v string) { fmt.Fprintf(&b, "  [#485260]%s[-] %s\n", padRight(k+":", 10), v) }
	row("Posture", string(a.Posture))
	row("Sizing", string(a.Sizing))
	row("Domain", a.Domain)
	row("SSO", string(a.SSO))
	if a.OIDCIssuer != "" {
		row("OIDC", a.OIDCIssuer)
	}
	row("Secrets", string(a.Secrets))
	row("Flavor", string(render.FlavorFor(a.Posture)))
	svc := a.Services
	if len(svc) == 0 {
		svc = []string{"(required only)"}
	}
	row("Services", strings.Join(svc, ", "))
	b.WriteString("\n[#485260]srectl renders into the output dir:[-]\n")
	b.WriteString("  • uds-config.yaml      [#485260]— UDS bundle variables[-]\n")
	b.WriteString("  • values.overlay.yaml  [#485260]— Helm value overlay[-]\n")
	b.WriteString("[#485260](re-run with --dry-run to print the full files)[-]")
	tv.SetText(b.String())

	form := tui.StyleForm(tview.NewForm())
	form.AddButton("Deploy", onNext)
	form.AddButton("Back", onBack)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(form, 3, 0, true)
}

// deployScreen is the deploy-stub screen (parity with the CLI stub).
func deployScreen(onFinish, onBack func()) tview.Primitive {
	tv := tview.NewTextView().SetDynamicColors(true)
	tv.SetText("[#9a6700::b]Deploy is not wired in this build.[-:-:-]\n\n" +
		"On [::b]Finish[::-], srectl writes the rendered files to the output dir.\n" +
		"The real `uds deploy` orchestration lands in the next build-order step.")
	form := tui.StyleForm(tview.NewForm())
	form.AddButton("Finish", onFinish)
	form.AddButton("Back", onBack)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tv, 0, 1, false).
		AddItem(tview.NewBox(), 1, 0, false).
		AddItem(form, 3, 0, true)
}

// checkboxLabel renders a service row's leading [x]/[ ] box plus its name. The
// brackets are escaped so tview does not parse them as colour tags.
func checkboxLabel(checked bool, name string) string {
	box := "[ ] "
	if checked {
		box = "[x] "
	}
	return tview.Escape(box + name)
}

// serviceBlurbs are concise, fits-on-one-line descriptions for the checklist
// (the catalog's full Description is longer and truncates against the border).
var serviceBlurbs = map[string]string{
	"core-identity-authorization": "Keycloak + Authservice — shared sign-in for every app",
	"core-runtime-security":       "Falco — runtime threat detection",
	"core-monitoring":             "Prometheus + Grafana — metrics and dashboards",
	"minio":                       "object storage operator",
}

// serviceHelp is the one-line description shown under a service row.
func serviceHelp(e catalog.Entry) string {
	s := serviceBlurbs[e.ID]
	if s == "" {
		s = e.Description
	}
	if e.Pending() {
		s += "  (packaging pending)"
	}
	return s
}

// requiredIDs lists the catalog IDs that are always deployed.
func requiredIDs(c *catalog.Catalog) []string {
	var ids []string
	for _, e := range c.Required() {
		ids = append(ids, e.ID)
	}
	return ids
}

// statusTag renders a colour-coded preflight status chip.
func statusTag(s preflight.Status) string {
	switch s {
	case preflight.StatusPass:
		return "[#1a7f37]PASS[-]"
	case preflight.StatusWarn:
		return "[#9a6700]WARN[-]"
	default:
		return "[#cf222e]FAIL[-]"
	}
}

// padRight pads s with spaces to width n (no truncation).
func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
