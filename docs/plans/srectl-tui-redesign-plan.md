# srectl TUI redesign (tview wizard + monitor) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild `srectl`'s TUI on tview so `srectl install` is a whiptail-look wizard and `srectl monitor` is a k9s-style live console, replacing the bubbletea skeleton — one static binary.

**Architecture:** A front-end swap. The wizard's decision logic lives in a rendering-independent `Flow` controller that POPULATES the existing `config.Answers`; the existing `render` package (Answers → `uds-config.yaml` + `values.overlay.yaml`) is untouched. The monitor reuses the app-catalog `State` + `kubectl` exec-wrapper, with pure row-builders behind the tview `Table`. tview rendering is manual/smoke-tested; the `Flow` and the row-builders are unit-tested.

**Tech Stack:** Go 1.25, `github.com/rivo/tview` + `github.com/gdamore/tcell/v2` (MIT), cobra. Removes `github.com/charmbracelet/bubbletea` + `github.com/charmbracelet/lipgloss`.

## Global Constraints

These apply to every task; copied verbatim from the spec + task brief.

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` commands run from `/Users/JonWFH/jondev/sre-v2/installer` unless noted.
- **Dependencies:** add `github.com/rivo/tview` + `github.com/gdamore/tcell/v2` (MIT — fine to depend on); remove bubbletea + lipgloss by the end of Task 4. Single static binary, no whiptail/dialog runtime dependency.
- **Front-end swap only (binding):** the wizard ONLY populates the existing `config.Answers`; the existing `render.Render` and the answer model are UNCHANGED. Do NOT add fields to `config.Answers`. The `--from answers.yaml --non-interactive` headless path stays and must keep working.
- **Branding (binding):** title bar reads `SRE Setup — <version>` (wizard) / `SRE Monitor — <version>` (monitor), using an em dash `—`. NEVER "Security Onion". Write ORIGINAL tview code; do not copy `so-setup`/`so-whiptail`. The blue-dialog look is the generic newt aesthetic.
- **Testing discipline:** keep the wizard `Flow` and the monitor row-builders unit-tested; tview rendering is manual/smoke. Reuse the app-catalog `kubectl` exec-wrapper + `State` for the monitor (orchestrate kubectl, never reimplement a k8s client).
- **Commits:** author with the noreply email. Each task's final commit:
  `git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "..."`
- **Deliberate deferral (the spec's "masked `InputField` for secrets"):** NOT built. `config.Answers` has no secret-value field, and adding one would change the render contract this front-end swap must preserve. The age recipient (`AgePublicKey`) is a PUBLIC value and uses a normal (non-masked) `InputField`. This is the one spec §4 primitive intentionally skipped — flagged to the user at wizard handoff.

## Build / deliver / smoke loop (used in Tasks 4 and 7)

Cross-compile Mac → bastion → VM:
```bash
cd /Users/JonWFH/jondev/sre-v2/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && scp -o StrictHostKeyChecking=accept-new /tmp/srectl cosmos@192.168.86.59:/tmp/srectl'
```
Headless wizard smoke (answers already at `/tmp/answers.yaml` on the VM):
```bash
ssh -J cosmos@cosmos-ssh.fightingsmartcyber.com cosmos@192.168.86.59 '/tmp/srectl install --from /tmp/answers.yaml --non-interactive --dry-run --out /tmp/out'
```
Interactive wizard (user drives): `ssh -J cosmos@cosmos-ssh.fightingsmartcyber.com cosmos@192.168.86.59 -t /tmp/srectl install`
Monitor (against the lab cluster, from the bastion which has kubeconfig): `ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor`

---

## File Structure

**Create:**
- `installer/internal/tui/theme.go` (package `tui`) — `ApplyTheme()`, `Title()`, `CenteredDialog()`, the palette. Shared by wizard + monitor.
- `installer/internal/tui/theme_test.go` — `Title()` unit test.
- `installer/internal/tui/wizard/flow.go` (package `wizard`) — the `Flow` controller (the testable answer-building logic).
- `installer/internal/tui/wizard/flow_test.go` — `Flow` unit tests.
- `installer/internal/tui/wizard/screens.go` — per-screen tview builders + the option lists.
- `installer/internal/tui/wizard/wizard.go` — `Run(cat, version)`: the `Pages` app + navigation.
- `installer/internal/tui/monitor/views.go` (package `monitor`) — `PackageRow`/`AppRow` + `buildPackageRows`/`buildAppRows` (pure).
- `installer/internal/tui/monitor/views_test.go` — row-builder unit tests.
- `installer/internal/tui/monitor/monitor.go` — `Run(version, state)`: the `Flex` app + refresh loop + `Table`.
- `installer/cmd/srectl/monitor.go` — `newMonitorCmd()` wiring.

**Modify:**
- `installer/cmd/srectl/install.go` — call `wizard.Run(cat, version)` instead of `tui.RunWizard(cat)`.
- `installer/cmd/srectl/main.go` — register `newMonitorCmd()`; update the package doc.
- `installer/internal/appcatalog/state.go` — add `CurrentContext()` to the `Kube` interface + `execKube`.
- `installer/internal/appcatalog/state_test.go` — add `CurrentContext` to `fakeKube`.
- `installer/go.mod` / `installer/go.sum` — deps churn (Tasks 1 + 4).

**Delete (Task 4):**
- `installer/internal/tui/wizard.go`, `views.go`, `options.go`, `review.go`, `styles.go`, `nav.go` (the bubbletea skeleton). `internal/tui/` keeps only `theme.go` + `theme_test.go`.

---

## Task 1: tview/tcell deps + shared theme

**Files:**
- Create: `installer/internal/tui/theme.go`
- Test: `installer/internal/tui/theme_test.go`
- Modify: `installer/go.mod`, `installer/go.sum`

**Interfaces:**
- Produces: `tui.ApplyTheme()`, `tui.Title(prefix, version string) string`, `tui.CenteredDialog(title string, inner tview.Primitive, w, h int) tview.Primitive`, and the exported palette colors (`tui.ColorScreen`, `tui.ColorDialog`, `tui.ColorAccent`, …). Consumed by Tasks 3 and 7.

> Note: the bubbletea `internal/tui` files still exist after this task (they are deleted in Task 4). Both `styles.go` and `theme.go` carry a `package tui` doc comment transiently — Go compiles this fine; `styles.go`'s is removed in Task 4.

- [ ] **Step 1: Add the deps**

```bash
cd /Users/JonWFH/jondev/sre-v2/installer
go get github.com/rivo/tview@latest
go get github.com/gdamore/tcell/v2@latest
```
Expected: `go.mod` now `require`s both; `go.sum` updated. (bubbletea/lipgloss are still present — they are removed in Task 4.)

- [ ] **Step 2: Write the failing test**

Create `installer/internal/tui/theme_test.go`:
```go
package tui

import "testing"

func TestTitle(t *testing.T) {
	got := Title("SRE Setup", "1.2.3")
	want := "SRE Setup — 1.2.3"
	if got != want {
		t.Fatalf("Title = %q, want %q", got, want)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/tui/ -run TestTitle -v`
Expected: FAIL — `undefined: Title`.

- [ ] **Step 4: Write theme.go**

Create `installer/internal/tui/theme.go`:
```go
// Package tui holds the shared tview theme for srectl's terminal UIs — the
// whiptail-style install wizard and the k9s-style monitor. It owns the global
// tview.Styles palette and the centered-dialog frame both surfaces reuse. The
// blue-on-gray look is the generic newt/ncurses aesthetic; the colors and text
// are original (never Security Onion's).
package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// SRE whiptail-style palette: a blue backdrop with a light-gray dialog body.
var (
	// ColorScreen is the full-screen blue backdrop.
	ColorScreen = tcell.NewRGBColor(0, 40, 104) // #002868 deep SRE blue
	// ColorDialog is the light-gray dialog body.
	ColorDialog = tcell.NewRGBColor(198, 198, 198) // #C6C6C6
	// ColorDialogText is the dark text on the gray dialog body.
	ColorDialogText = tcell.ColorBlack
	// ColorBorder is the dialog border + graphics.
	ColorBorder = tcell.ColorBlack
	// ColorAccent is the blue used for titles and the selection background.
	ColorAccent = tcell.NewRGBColor(0, 40, 104)
	// ColorSelectedText is the inverse text on a selected (blue) row.
	ColorSelectedText = tcell.ColorWhite
)

// ApplyTheme sets the global tview styles to the SRE whiptail palette. Call once
// before building any tview primitive.
func ApplyTheme() {
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    ColorDialog,       // dialog bodies are gray
		ContrastBackgroundColor:     ColorAccent,       // selection / active = blue
		MoreContrastBackgroundColor: ColorAccent,
		BorderColor:                 ColorBorder,
		TitleColor:                  ColorAccent,
		GraphicsColor:               ColorBorder,
		PrimaryTextColor:            ColorDialogText,   // dark text on gray
		SecondaryTextColor:          ColorDialogText,
		TertiaryTextColor:           ColorDialogText,
		InverseTextColor:            ColorSelectedText, // text on the blue selection
		ContrastSecondaryTextColor:  ColorSelectedText,
	}
}

// Title returns a title-bar string, e.g. Title("SRE Setup", "0.0.0-dev") →
// "SRE Setup — 0.0.0-dev".
func Title(prefix, version string) string {
	return prefix + " — " + version
}

// CenteredDialog wraps inner in a gray bordered box of size w×h, centered on the
// blue screen backdrop — the reusable whiptail dialog frame. title is shown on
// the box border (already prefixed by the caller, e.g. "SRE Setup — v1 · Posture").
func CenteredDialog(title string, inner tview.Primitive, w, h int) tview.Primitive {
	box := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(inner, 0, 1, true)
	box.SetBorder(true).
		SetTitle(" " + title + " ").
		SetBorderColor(ColorBorder).
		SetTitleColor(ColorAccent).
		SetBackgroundColor(ColorDialog)

	row := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(blueSpacer(), 0, 1, false).
		AddItem(box, w, 0, true).
		AddItem(blueSpacer(), 0, 1, false)
	outer := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(blueSpacer(), 0, 1, false).
		AddItem(row, h, 0, true).
		AddItem(blueSpacer(), 0, 1, false)
	outer.SetBackgroundColor(ColorScreen)
	return outer
}

// blueSpacer is an empty box painted screen-blue, used as centering padding.
func blueSpacer() *tview.Box {
	return tview.NewBox().SetBackgroundColor(ColorScreen)
}
```

- [ ] **Step 5: Run the test to verify it passes + build**

Run: `go test ./internal/tui/ -run TestTitle -v && go build ./...`
Expected: PASS; build succeeds (bubbletea still compiles).

- [ ] **Step 6: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/tui/theme.go installer/internal/tui/theme_test.go installer/go.mod installer/go.sum
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(tui): add tview deps + shared whiptail theme"
```

---

## Task 2: wizard Flow controller (the testable logic)

**Files:**
- Create: `installer/internal/tui/wizard/flow.go`
- Test: `installer/internal/tui/wizard/flow_test.go`

**Interfaces:**
- Consumes: `catalog.Catalog` (`.Optional()`, `.Find()`), `config` (`Default`, `Posture`, `Sizing`, `SSOMode`, `SecretsMode`, `Answers`).
- Produces (consumed by Task 3):
  - `wizard.NewFlow(cat *catalog.Catalog) *Flow`
  - `(*Flow).Answers() config.Answers`
  - `(*Flow).SetPosture(config.Posture)`, `SetSizing(config.Sizing)`, `SetDomain(string)`
  - `(*Flow).ToggleService(id string)`, `ServiceChecked(id string) bool`
  - `(*Flow).SetSSO(config.SSOMode)`, `SetOIDCIssuer(string)`, `NeedsOIDCIssuer() bool`
  - `(*Flow).SetSecrets(config.SecretsMode)`, `SetAgePublicKey(string)`, `NeedsAgeKey() bool`
  - `(*Flow).Validate() error`

- [ ] **Step 1: Write the failing tests**

Create `installer/internal/tui/wizard/flow_test.go`:
```go
package wizard

import (
	"reflect"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
)

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return c
}

func TestNewFlow_MatchesDefaults(t *testing.T) {
	f := NewFlow(testCatalog(t))
	got := f.Answers()
	want := config.Default()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default Answers mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestFlow_SettersPopulateAnswers(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.SetPosture(config.PostureDoD)
	f.SetSizing(config.SizingLarge)
	f.SetDomain("  example.gov  ") // trimmed
	if a := f.Answers(); a.Posture != config.PostureDoD || a.Sizing != config.SizingLarge || a.Domain != "example.gov" {
		t.Fatalf("setters not applied: %+v", f.Answers())
	}
}

func TestFlow_ToggleService(t *testing.T) {
	f := NewFlow(testCatalog(t))
	// minio is optional and NOT default-selected.
	if f.ServiceChecked("minio") {
		t.Fatal("minio should start unchecked")
	}
	f.ToggleService("minio")
	f.ToggleService("core-monitoring") // default-on → off
	a := f.Answers()
	if !a.HasService("minio") {
		t.Errorf("minio should be selected after toggle; got %v", a.Services)
	}
	if a.HasService("core-monitoring") {
		t.Errorf("core-monitoring should be deselected; got %v", a.Services)
	}
	// Services are emitted in catalog (deploy) order: identity, runtime-security, minio.
	want := []string{"core-identity-authorization", "core-runtime-security", "minio"}
	if !reflect.DeepEqual(a.Services, want) {
		t.Errorf("service order = %v, want %v", a.Services, want)
	}
}

func TestFlow_ToggleService_IgnoresRequired(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.ToggleService("pgo") // required → no-op
	if f.ServiceChecked("pgo") {
		t.Fatal("required service must not become a toggleable selection")
	}
}

func TestFlow_SSO_ConditionalIssuer(t *testing.T) {
	f := NewFlow(testCatalog(t))
	f.SetSSO(config.SSOExternalOIDC)
	if !f.NeedsOIDCIssuer() {
		t.Fatal("ExternalOIDC must require an issuer screen")
	}
	if err := f.Validate(); err == nil {
		t.Fatal("ExternalOIDC without an issuer should fail validation")
	}
	f.SetOIDCIssuer("https://idp.example.gov/realms/x")
	if err := f.Validate(); err != nil {
		t.Fatalf("ExternalOIDC + issuer should validate: %v", err)
	}
	// Switching away clears the stale issuer.
	f.SetSSO(config.SSOKeycloak)
	if f.NeedsOIDCIssuer() || f.Answers().OIDCIssuer != "" {
		t.Fatalf("switching away from ExternalOIDC must clear the issuer: %+v", f.Answers())
	}
}

func TestFlow_Secrets_ConditionalAgeKey(t *testing.T) {
	f := NewFlow(testCatalog(t))
	if !f.NeedsAgeKey() { // SOPSAge is the default
		t.Fatal("SOPSAge default must request an age key screen")
	}
	f.SetAgePublicKey("age1exampledonotusexxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxq0")
	f.SetSecrets(config.SecretsExternal)
	if f.NeedsAgeKey() || f.Answers().AgePublicKey != "" {
		t.Fatalf("switching to External must clear the age key: %+v", f.Answers())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/wizard/ -v`
Expected: FAIL — `undefined: NewFlow` (package does not compile yet).

- [ ] **Step 3: Write flow.go**

Create `installer/internal/tui/wizard/flow.go`:
```go
// Package wizard implements srectl's tview install wizard. The decision logic —
// which screen sets which Answers field — lives in Flow, kept separate from the
// tview rendering so it is unit-testable (given setter calls → produced Answers).
// The wizard only POPULATES the shared config.Answers; render is unchanged.
package wizard

import (
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
)

// Flow is the rendering-independent wizard controller. It seeds the default
// answers and exposes a setter per screen; the tview layer calls these on user
// input, then reads Answers() at review/deploy.
type Flow struct {
	cat      *catalog.Catalog
	answers  config.Answers
	selected map[string]bool // optional service ID → checked
}

// NewFlow builds a Flow seeded with config.Default() and the matching default
// service selection (the optional services Default() pre-selects, checked).
func NewFlow(cat *catalog.Catalog) *Flow {
	a := config.Default()
	sel := map[string]bool{}
	for _, e := range cat.Optional() {
		if a.HasService(e.ID) {
			sel[e.ID] = true
		}
	}
	return &Flow{cat: cat, answers: a, selected: sel}
}

// Answers returns the answers built so far, with Services recomputed from the
// current selection in catalog (deploy) order.
func (f *Flow) Answers() config.Answers {
	a := f.answers
	a.Services = f.selectedServiceIDs()
	return a
}

// selectedServiceIDs returns the checked optional service IDs in catalog order.
func (f *Flow) selectedServiceIDs() []string {
	var ids []string
	for _, e := range f.cat.Optional() {
		if f.selected[e.ID] {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// SetPosture records the security posture.
func (f *Flow) SetPosture(p config.Posture) { f.answers.Posture = p }

// SetSizing records the resource envelope.
func (f *Flow) SetSizing(s config.Sizing) { f.answers.Sizing = s }

// SetDomain records the ingress base domain (trimmed).
func (f *Flow) SetDomain(d string) { f.answers.Domain = strings.TrimSpace(d) }

// ToggleService flips an optional service's checked state. Required or unknown
// IDs are ignored (required services are always deployed, never toggled here).
func (f *Flow) ToggleService(id string) {
	if e, ok := f.cat.Find(id); !ok || e.Required {
		return
	}
	f.selected[id] = !f.selected[id]
}

// ServiceChecked reports whether an optional service is currently checked.
func (f *Flow) ServiceChecked(id string) bool { return f.selected[id] }

// SetSSO records the identity mode. Switching away from ExternalOIDC clears any
// captured issuer so a stale value cannot leak into the rendered config.
func (f *Flow) SetSSO(mode config.SSOMode) {
	f.answers.SSO = mode
	if mode != config.SSOExternalOIDC {
		f.answers.OIDCIssuer = ""
	}
}

// SetOIDCIssuer records the external OIDC issuer URL (trimmed).
func (f *Flow) SetOIDCIssuer(url string) { f.answers.OIDCIssuer = strings.TrimSpace(url) }

// NeedsOIDCIssuer reports whether the issuer-input screen should be shown.
func (f *Flow) NeedsOIDCIssuer() bool { return f.answers.SSO == config.SSOExternalOIDC }

// SetSecrets records the secrets-management mode. Switching away from SOPSAge
// clears any captured age key.
func (f *Flow) SetSecrets(mode config.SecretsMode) {
	f.answers.Secrets = mode
	if mode != config.SecretsSOPSAge {
		f.answers.AgePublicKey = ""
	}
}

// SetAgePublicKey records the SOPS age recipient (trimmed; a PUBLIC value).
func (f *Flow) SetAgePublicKey(k string) { f.answers.AgePublicKey = strings.TrimSpace(k) }

// NeedsAgeKey reports whether the age-key-input screen should be shown.
func (f *Flow) NeedsAgeKey() bool { return f.answers.Secrets == config.SecretsSOPSAge }

// Validate reports whether the captured answers are internally consistent.
func (f *Flow) Validate() error { return f.Answers().Validate() }
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/wizard/ -v`
Expected: PASS (all six tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/tui/wizard/flow.go installer/internal/tui/wizard/flow_test.go
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(wizard): testable Flow controller over config.Answers"
```

---

## Task 3: wizard tview screens + Pages app

**Files:**
- Create: `installer/internal/tui/wizard/screens.go`, `installer/internal/tui/wizard/wizard.go`

**Interfaces:**
- Consumes: `tui.ApplyTheme`, `tui.Title`, `tui.CenteredDialog`; `Flow` (Task 2); `catalog`, `config`, `preflight`, `render`.
- Produces (consumed by Task 4): `wizard.Run(cat *catalog.Catalog, version string) (*config.Answers, error)` — launches the tview wizard; returns the captured answers, or an error if cancelled.

**Flow / page order** (the spec's backbone `welcome → posture → sizing → core services → SSO → secrets → review → deploy`, expanded so the spec §4 `Form` InputFields for domain / OIDC issuer / age key have a home):

`welcome → posture → sizing → domain → services → sso → [oidc-issuer?] → secrets → [age-key?] → review → deploy`

`[oidc-issuer?]` is shown only when `flow.NeedsOIDCIssuer()`; `[age-key?]` only when `flow.NeedsAgeKey()`.

> Rendering is manual/smoke-tested. Steps 1–2 below add complete, intent-correct builders; the Task 4 build + the VM smoke verify and tune the tview specifics.

- [ ] **Step 1: Write screens.go**

Create `installer/internal/tui/wizard/screens.go`:
```go
package wizard

import (
	"fmt"
	"strings"

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
```

- [ ] **Step 2: Write wizard.go (the Pages navigator)**

Create `installer/internal/tui/wizard/wizard.go`:
```go
package wizard

import (
	"fmt"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// page is one wizard screen: a name, a builder, and an optional predicate that
// decides whether the screen is shown for the current answers.
type page struct {
	name    string
	build   func() tview.Primitive
	visible func() bool // nil ⇒ always visible
}

// Run launches the tview install wizard and returns the captured answers, or an
// error if the user cancels before finishing.
func Run(cat *catalog.Catalog, version string) (*config.Answers, error) {
	tui.ApplyTheme()
	app := tview.NewApplication()
	pages := tview.NewPages()
	flow := NewFlow(cat)
	finished := false

	w := &wiz{app: app, pages: pages, flow: flow, version: version}

	// Screen builders, in flow order. Conditional screens carry a predicate.
	w.order = []page{
		{name: "welcome", build: func() tview.Primitive {
			return welcomeScreen(w.next, w.quit)
		}},
		{name: "posture", build: func() tview.Primitive {
			return radioScreen("Posture — security profile", postureOptions, flow.Answers().Posture,
				func(v config.Posture) { flow.SetPosture(v); w.next() })
		}},
		{name: "sizing", build: func() tview.Primitive {
			return radioScreen("Sizing — resource envelope", sizingOptions, flow.Answers().Sizing,
				func(v config.Sizing) { flow.SetSizing(v); w.next() })
		}},
		{name: "domain", build: func() tview.Primitive {
			return inputScreen("Base domain", flow.Answers().Domain, "uds.dev",
				flow.SetDomain, w.next, w.prev)
		}},
		{name: "services", build: func() tview.Primitive {
			return checklistScreen(flow, w.next, w.prev)
		}},
		{name: "sso", build: func() tview.Primitive {
			return radioScreen("SSO — identity provider", ssoOptions, flow.Answers().SSO,
				func(v config.SSOMode) { flow.SetSSO(v); w.next() })
		}},
		{name: "oidc", visible: flow.NeedsOIDCIssuer, build: func() tview.Primitive {
			return inputScreen("OIDC issuer URL", flow.Answers().OIDCIssuer, "https://idp.example/realms/x",
				flow.SetOIDCIssuer, w.next, w.prev)
		}},
		{name: "secrets", build: func() tview.Primitive {
			return radioScreen("Secrets — management mode", secretsOptions, flow.Answers().Secrets,
				func(v config.SecretsMode) { flow.SetSecrets(v); w.next() })
		}},
		{name: "agekey", visible: flow.NeedsAgeKey, build: func() tview.Primitive {
			return inputScreen("SOPS age recipient (public)", flow.Answers().AgePublicKey, "age1...",
				flow.SetAgePublicKey, w.next, w.prev)
		}},
		{name: "review", build: func() tview.Primitive {
			return reviewScreen(flow, cat, w.next, w.prev)
		}},
		{name: "deploy", build: func() tview.Primitive {
			return deployScreen(func() { finished = true; app.Stop() }, w.prev)
		}},
	}

	// Global keys: Esc = back, Ctrl-C = cancel.
	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape:
			w.prev()
			return nil
		case tcell.KeyCtrlC:
			w.quit()
			return nil
		}
		return ev
	})

	w.show(0)
	if err := app.Run(); err != nil {
		return nil, fmt.Errorf("wizard: run: %w", err)
	}
	if !finished {
		return nil, fmt.Errorf("wizard cancelled")
	}
	answers := flow.Answers()
	return &answers, nil
}

// wiz holds the running wizard's tview state + the page order.
type wiz struct {
	app     *tview.Application
	pages   *tview.Pages
	flow    *Flow
	version string
	order   []page
	idx     int
}

// show rebuilds and displays the page at index i, framed by the whiptail dialog.
func (w *wiz) show(i int) {
	w.idx = i
	p := w.order[i]
	name := p.name
	inner := p.build()
	framed := tui.CenteredDialog(dialogTitle(w.version, name), inner, 76, 22)
	w.pages.AddAndSwitchToPage(name, framed, true)
	w.app.SetFocus(framed)
}

// next advances to the next visible page; past the end finishes the deploy step.
func (w *wiz) next() {
	for i := w.idx + 1; i < len(w.order); i++ {
		if w.order[i].visible == nil || w.order[i].visible() {
			w.show(i)
			return
		}
	}
}

// prev steps back to the previous visible page (welcome is the floor).
func (w *wiz) prev() {
	for i := w.idx - 1; i >= 0; i-- {
		if w.order[i].visible == nil || w.order[i].visible() {
			w.show(i)
			return
		}
	}
}

// quit stops the app without marking the wizard finished (a cancel).
func (w *wiz) quit() { w.app.Stop() }
```

- [ ] **Step 3: Build the package**

Run: `go build ./internal/tui/...`
Expected: compiles. If tview API mismatches surface (method names/signatures), fix against the installed tview version (`go doc github.com/rivo/tview.Form`) — these are rendering-layer adjustments, not logic changes. Re-run until it builds.

- [ ] **Step 4: Re-run the Flow unit tests (guard against regressions)**

Run: `go test ./internal/tui/wizard/ -v`
Expected: PASS (the Task 2 tests still pass; screens/wizard add no testable logic).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/tui/wizard/screens.go installer/internal/tui/wizard/wizard.go
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(wizard): tview whiptail screens + Pages navigator"
```

---

## Task 4: swap install.go to the tview wizard; remove bubbletea — WIZARD COMPLETE

**Files:**
- Modify: `installer/cmd/srectl/install.go`
- Delete: `installer/internal/tui/{wizard.go,views.go,options.go,review.go,styles.go,nav.go}`
- Modify: `installer/go.mod`, `installer/go.sum`

**Interfaces:**
- Consumes: `wizard.Run(cat, version)` (Task 3). `version` is the `main` package var (`cmd/srectl/version.go`).

- [ ] **Step 1: Point install.go at the new wizard**

In `installer/cmd/srectl/install.go`, change the import block: replace
`"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"` with
`"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/wizard"`.

Then change the `resolveAnswers` tail (the final return) from:
```go
	return tui.RunWizard(cat)
```
to:
```go
	return wizard.Run(cat, version)
```

- [ ] **Step 2: Delete the bubbletea skeleton**

```bash
cd /Users/JonWFH/jondev/sre-v2/installer
git rm internal/tui/wizard.go internal/tui/views.go internal/tui/options.go internal/tui/review.go internal/tui/styles.go internal/tui/nav.go
```
Expected: `internal/tui/` now contains only `theme.go` + `theme_test.go`.

- [ ] **Step 3: Drop the unused deps**

Run: `go mod tidy`
Expected: bubbletea, lipgloss, and their transitive `// indirect` deps disappear from `go.mod`/`go.sum`; tview + tcell remain.

- [ ] **Step 4: Build + full test suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds; ALL tests pass (render tests, appcatalog tests, the new Flow + theme tests). No reference to `internal/tui.RunWizard` remains.

- [ ] **Step 5: Headless smoke (the non-interactive path still works)**

```bash
cd /Users/JonWFH/jondev/sre-v2/installer
cat > /tmp/answers.yaml <<'YAML'
posture: Baseline
sizing: Small
services: [core-identity-authorization, core-runtime-security, core-monitoring]
sso: Keycloak
domain: uds.dev
secrets: SOPSAge
YAML
go run ./cmd/srectl install --from /tmp/answers.yaml --non-interactive --dry-run --out /tmp/out
```
Expected: prints the two rendered files (`uds-config.yaml`, `values.overlay.yaml`) + `wrote 2 file(s) to /tmp/out` + `dry-run: not deploying.` — identical to pre-redesign behaviour (proves the front-end swap preserved render).

- [ ] **Step 6: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add -A installer/
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(install): swap wizard to tview; remove bubbletea/lipgloss"
```

- [ ] **Step 7: Deliver to the VM for the interactive smoke (user-driven)**

Run the cross-compile + deliver block from "Build / deliver / smoke loop" above, then **PING THE USER** to drive:
`ssh -J cosmos@cosmos-ssh.fightingsmartcyber.com cosmos@192.168.86.59 -t /tmp/srectl install`
Expected (manual): blue full-screen, gray bordered dialogs titled `SRE Setup — <version> · <screen>`, radiolists for posture/sizing/sso/secrets, a checklist for services, input fields for domain (+ conditional OIDC issuer / age key), a review screen previewing both files, and a deploy-stub. **This is the wizard-first checkpoint — do not start the monitor until the user confirms the wizard renders.**

---

## Task 5: add `CurrentContext()` to the app-catalog Kube wrapper

**Files:**
- Modify: `installer/internal/appcatalog/state.go`, `installer/internal/appcatalog/state_test.go`

**Interfaces:**
- Produces (consumed by Task 7): `Kube.CurrentContext() (string, error)` — returns `kubectl config current-context`; on `execKube`, shells out; the monitor header shows it.

- [ ] **Step 1: Write the failing test**

Append to `installer/internal/appcatalog/state_test.go`:
```go
func TestFakeKube_CurrentContext(t *testing.T) {
	fk := &fakeKube{context: "lab-rke2"}
	got, err := fk.CurrentContext()
	if err != nil || got != "lab-rke2" {
		t.Fatalf("CurrentContext = %q, %v; want \"lab-rke2\", nil", got, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/appcatalog/ -run TestFakeKube_CurrentContext -v`
Expected: FAIL — `fk.CurrentContext undefined` and `unknown field 'context'`.

- [ ] **Step 3: Extend the interface + the fake + the real wrapper**

In `installer/internal/appcatalog/state.go`, add `CurrentContext` to the `Kube` interface (after `ListPackages`):
```go
type Kube interface {
	EnsureNamespace(ns string) error
	GetConfigMap(ns, name string) ([]byte, error)
	ApplyConfigMap(ns, name string, data map[string]string) error
	ListPackages() ([]byte, error)
	CurrentContext() (string, error)
}
```
And add the `execKube` implementation (after `ListPackages`):
```go
// CurrentContext returns the active kubeconfig context name, for the monitor header.
func (execKube) CurrentContext() (string, error) {
	out, err := commandContext("kubectl", "config", "current-context").Output()
	if err != nil {
		return "", fmt.Errorf("kubectl config current-context: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
```
In `installer/internal/appcatalog/state_test.go`, add a `context` field to the `fakeKube` struct and the method:
```go
func (f *fakeKube) CurrentContext() (string, error) { return f.context, nil }
```
(Add `context string` to the `fakeKube` struct definition.)

- [ ] **Step 4: Run the appcatalog suite to verify it passes**

Run: `go test ./internal/appcatalog/...`
Expected: PASS (the new test plus all existing appcatalog tests — the fake still satisfies `Kube`).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/appcatalog/state.go installer/internal/appcatalog/state_test.go
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(appcatalog): add Kube.CurrentContext for the monitor header"
```

---

## Task 6: monitor row-builders (the testable view logic)

**Files:**
- Create: `installer/internal/tui/monitor/views.go`, `installer/internal/tui/monitor/views_test.go`

**Interfaces:**
- Consumes: `appcatalog.Record`.
- Produces (consumed by Task 7):
  - `type PackageRow struct{ Namespace, Name, Phase string; Endpoints int }`
  - `type AppRow struct{ Name, Version, Source string; Live bool }`
  - `buildPackageRows(raw []byte) ([]PackageRow, error)` — parses `kubectl get packages -A -o json`, sorted by (namespace, name).
  - `buildAppRows(recs map[string]appcatalog.Record, live map[string]bool) []AppRow` — joins install records with live-package presence, sorted by name.

- [ ] **Step 1: Write the failing tests**

Create `installer/internal/tui/monitor/views_test.go` (the JSON fixture mirrors the real lab `kubectl get packages -A -o json` shape — `.items[].metadata.{namespace,name}` + `.status.phase` + `.status.endpoints`):
```go
package monitor

import (
	"reflect"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

const packagesJSON = `{"items":[
 {"metadata":{"name":"cosmos","namespace":"cosmos"},"status":{"phase":"Ready","endpoints":["cosmos.uds.dev"]}},
 {"metadata":{"name":"authservice","namespace":"authservice"},"status":{"phase":"Ready","endpoints":[]}},
 {"metadata":{"name":"keycloak","namespace":"keycloak"},"status":{"phase":"Pending","endpoints":["sso.uds.dev","keycloak.admin.uds.dev"]}}
]}`

func TestBuildPackageRows(t *testing.T) {
	got, err := buildPackageRows([]byte(packagesJSON))
	if err != nil {
		t.Fatalf("buildPackageRows: %v", err)
	}
	want := []PackageRow{
		{Namespace: "authservice", Name: "authservice", Phase: "Ready", Endpoints: 0},
		{Namespace: "cosmos", Name: "cosmos", Phase: "Ready", Endpoints: 1},
		{Namespace: "keycloak", Name: "keycloak", Phase: "Pending", Endpoints: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

func TestBuildPackageRows_Empty(t *testing.T) {
	got, err := buildPackageRows([]byte(`{"items":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 rows, got %d", len(got))
	}
}

func TestBuildPackageRows_Malformed(t *testing.T) {
	if _, err := buildPackageRows([]byte(`not json`)); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestBuildAppRows(t *testing.T) {
	recs := map[string]appcatalog.Record{
		"cosmos":  {Version: "2.102.0", Source: "oci:ghcr.io/jongodb-labs/bundles/cosmos"},
		"orphan":  {Version: "0.1.0", Source: "oci:example/orphan"},
	}
	live := map[string]bool{"cosmos": true} // orphan recorded but not live (drift)
	got := buildAppRows(recs, live)
	want := []AppRow{
		{Name: "cosmos", Version: "2.102.0", Source: "oci:ghcr.io/jongodb-labs/bundles/cosmos", Live: true},
		{Name: "orphan", Version: "0.1.0", Source: "oci:example/orphan", Live: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/tui/monitor/ -v`
Expected: FAIL — `undefined: buildPackageRows` (package does not compile).

- [ ] **Step 3: Write views.go**

Create `installer/internal/tui/monitor/views.go`:
```go
// Package monitor implements srectl's k9s-style live console of the substrate.
// The per-view row-builders are pure (cluster data → []row) and unit-tested with
// the app-catalog fakes; the tview Table rendering + refresh loop is smoke-tested.
package monitor

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// PackageRow is one row of the packages view: a live UDS Package + its status.
type PackageRow struct {
	Namespace string
	Name      string
	Phase     string
	Endpoints int
}

// AppRow is one row of the apps view: an install record joined with live presence.
type AppRow struct {
	Name    string
	Version string
	Source  string
	Live    bool
}

// buildPackageRows parses `kubectl get packages -A -o json` into rows, sorted by
// (namespace, name) for stable display.
func buildPackageRows(raw []byte) ([]PackageRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Status struct {
				Phase     string   `json:"phase"`
				Endpoints []string `json:"endpoints"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("monitor: parse packages json: %w", err)
	}
	rows := make([]PackageRow, 0, len(list.Items))
	for _, it := range list.Items {
		rows = append(rows, PackageRow{
			Namespace: it.Metadata.Namespace,
			Name:      it.Metadata.Name,
			Phase:     it.Status.Phase,
			Endpoints: len(it.Status.Endpoints),
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

// buildAppRows joins the install records with the live-package set, sorted by
// name. Live is false when an app is recorded but has no live UDS Package (drift).
func buildAppRows(recs map[string]appcatalog.Record, live map[string]bool) []AppRow {
	rows := make([]AppRow, 0, len(recs))
	for name, r := range recs {
		rows = append(rows, AppRow{
			Name:    name,
			Version: r.Version,
			Source:  r.Source,
			Live:    live[name],
		})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/tui/monitor/ -v`
Expected: PASS (all four tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/tui/monitor/views.go installer/internal/tui/monitor/views_test.go
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): pure packages/apps row-builders"
```

---

## Task 7: monitor tview app + refresh loop + CLI wiring — MONITOR COMPLETE

**Files:**
- Create: `installer/internal/tui/monitor/monitor.go`, `installer/cmd/srectl/monitor.go`
- Modify: `installer/cmd/srectl/main.go`

**Interfaces:**
- Consumes: `tui.ApplyTheme`, `tui.Title`; `appcatalog.State` (`.Load()`, `.InstalledPackages()`, `.Kube.ListPackages()`, `.Kube.CurrentContext()`); the Task 6 row-builders.
- Produces: `monitor.Run(version string, state appcatalog.State) error`; `newMonitorCmd()` registered on the root command.

> Rendering + the refresh goroutine are smoke-tested against the lab cluster; the testable logic (row-builders) is already covered in Task 6.

- [ ] **Step 1: Write monitor.go**

Create `installer/internal/tui/monitor/monitor.go`:
```go
package monitor

import (
	"fmt"
	"time"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// viewKind selects which dataset the table shows.
type viewKind int

const (
	viewPackages viewKind = iota
	viewApps
)

// refreshInterval is how often the background loop re-polls the cluster.
const refreshInterval = 5 * time.Second

// Run launches the k9s-style monitor: header + table + footer, with a background
// refresh loop. Read-only; views switch with 1 (packages) / 2 (apps); q quits.
func Run(version string, state appcatalog.State) error {
	tui.ApplyTheme()
	app := tview.NewApplication()

	header := tview.NewTextView().SetDynamicColors(true)
	ctx, _ := state.Kube.CurrentContext() // best-effort; header is cosmetic
	header.SetText(fmt.Sprintf("%s   context: %s", tui.Title("SRE Monitor", version), ctx))

	table := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	footer := tview.NewTextView().SetDynamicColors(true).
		SetText("[::b]1[::-] packages  [::b]2[::-] apps  [::b]j/k[::-] move  [::b]q[::-] quit")

	layout := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).
		AddItem(table, 0, 1, true).
		AddItem(footer, 1, 0, false)

	m := &monitor{app: app, state: state, table: table, view: viewPackages}
	m.refresh() // initial paint

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case '1':
			m.view = viewPackages
			m.refresh()
			return nil
		case '2':
			m.view = viewApps
			m.refresh()
			return nil
		case 'q':
			app.Stop()
			return nil
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		if ev.Key() == tcell.KeyEscape {
			app.Stop()
			return nil
		}
		return ev
	})

	// Background refresh loop: re-poll on an interval, redraw via QueueUpdateDraw.
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(refreshInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				app.QueueUpdateDraw(func() { m.refresh() })
			}
		}
	}()
	defer close(stop)

	if err := app.SetRoot(layout, true).Run(); err != nil {
		return fmt.Errorf("monitor: run: %w", err)
	}
	return nil
}

// monitor holds the running view state.
type monitor struct {
	app   *tview.Application
	state appcatalog.State
	table *tview.Table
	view  viewKind
}

// refresh re-fetches the active view's data and repaints the table. Fetch errors
// are shown in-table rather than crashing the console.
func (m *monitor) refresh() {
	switch m.view {
	case viewApps:
		m.paintApps()
	default:
		m.paintPackages()
	}
}

// paintPackages fills the table from the live UDS Packages.
func (m *monitor) paintPackages() {
	raw, err := m.state.Kube.ListPackages()
	if err != nil {
		m.paintError("packages", err)
		return
	}
	rows, err := buildPackageRows(raw)
	if err != nil {
		m.paintError("packages", err)
		return
	}
	m.table.Clear()
	m.setHeaderRow("NAMESPACE", "PACKAGE", "PHASE", "ENDPOINTS")
	for i, r := range rows {
		m.table.SetCellSimple(i+1, 0, r.Namespace)
		m.table.SetCellSimple(i+1, 1, r.Name)
		m.table.GetCell(i+1, 1).SetReference(r) // for future drill-in
		m.table.SetCell(i+1, 2, phaseCell(r.Phase))
		m.table.SetCellSimple(i+1, 3, fmt.Sprintf("%d", r.Endpoints))
	}
}

// paintApps fills the table from the install records joined with live presence.
func (m *monitor) paintApps() {
	recs, err := m.state.Load()
	if err != nil {
		m.paintError("apps", err)
		return
	}
	live, err := m.state.InstalledPackages()
	if err != nil {
		m.paintError("apps", err)
		return
	}
	rows := buildAppRows(recs, live)
	m.table.Clear()
	m.setHeaderRow("APP", "VERSION", "SOURCE", "LIVE")
	for i, r := range rows {
		m.table.SetCellSimple(i+1, 0, r.Name)
		m.table.SetCellSimple(i+1, 1, r.Version)
		m.table.SetCellSimple(i+1, 2, r.Source)
		m.table.SetCell(i+1, 3, liveCell(r.Live))
	}
}

// setHeaderRow writes the fixed, non-selectable header row.
func (m *monitor) setHeaderRow(cols ...string) {
	for c, name := range cols {
		cell := tview.NewTableCell(name).SetSelectable(false).SetAttributes(tcell.AttrBold)
		m.table.SetCell(0, c, cell)
	}
}

// paintError replaces the table with a single error row.
func (m *monitor) paintError(view string, err error) {
	m.table.Clear()
	m.setHeaderRow(view)
	m.table.SetCell(1, 0, tview.NewTableCell(fmt.Sprintf("error: %v", err)).
		SetTextColor(tcell.ColorRed))
}

// phaseCell colors a package phase (Ready green, anything else amber).
func phaseCell(phase string) *tview.TableCell {
	c := tview.NewTableCell(phase)
	if phase == "Ready" {
		return c.SetTextColor(tcell.ColorGreen)
	}
	return c.SetTextColor(tcell.ColorYellow)
}

// liveCell renders the apps-view live flag.
func liveCell(live bool) *tview.TableCell {
	if live {
		return tview.NewTableCell("yes").SetTextColor(tcell.ColorGreen)
	}
	return tview.NewTableCell("DRIFT").SetTextColor(tcell.ColorRed)
}
```

- [ ] **Step 2: Wire the cobra command**

Create `installer/cmd/srectl/monitor.go`:
```go
package main

import (
	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor"
	"github.com/spf13/cobra"
)

// newMonitorCmd builds the `srectl monitor` command — the k9s-style live console.
func newMonitorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "monitor",
		Short: "Live k9s-style console of the substrate (packages + apps)",
		Long: "monitor opens a terminal console of the running substrate: live UDS Packages\n" +
			"and the installed mission apps, refreshed on an interval. Read-only.\n\n" +
			"Keys: 1 packages · 2 apps · j/k move · q quit.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			state := appcatalog.State{Kube: appcatalog.NewKube()}
			return monitor.Run(version, state)
		},
	}
}
```

- [ ] **Step 3: Register the command + update the package doc**

In `installer/cmd/srectl/main.go`, add `newMonitorCmd()` to the `root.AddCommand(...)` call:
```go
	root.AddCommand(
		newPreflightCmd(),
		newInstallCmd(),
		newMonitorCmd(),
		newAppCmd(),
		newVersionCmd(),
	)
```
And add a line to the command doc comment's subcommand list (above `newRootCmd`):
```go
//	srectl monitor     open the live k9s-style console of the substrate
```

- [ ] **Step 4: Build + full test suite**

Run: `cd /Users/JonWFH/jondev/sre-v2/installer && go build ./... && go test ./...`
Expected: build succeeds; all tests pass. If tview Table API mismatches surface, fix against the installed version (`go doc github.com/rivo/tview.Table`) — rendering-layer only.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2
git add installer/internal/tui/monitor/monitor.go installer/cmd/srectl/monitor.go installer/cmd/srectl/main.go
git -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): tview k9s-style console + srectl monitor command"
```

- [ ] **Step 6: Smoke against the lab cluster (user-visible)**

Cross-compile + deliver to the bastion (which holds the kubeconfig), then run the monitor there:
```bash
cd /Users/JonWFH/jondev/sre-v2/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor
```
Expected (manual): header `SRE Monitor — <version>   context: default`; the packages view lists the 6 live UDS Packages (authservice, cosmos, falco, grafana, keycloak, prometheus-stack) with `Ready` in green; `2` switches to the apps view (the `sre-appcatalog-installs` records, or an empty table if none recorded yet); `q` quits. Confirm the table re-renders on the refresh interval.

---

## Self-Review

**1. Spec coverage:**

| Spec section | Covered by |
| --- | --- |
| §2 Licensing/branding (tview/tcell MIT, "SRE Setup", original code) | Global Constraints; Tasks 1, 3 (`Title`, `dialogTitle`) |
| §3 Shared theme (`tview.Styles`, `centeredDialog`) | Task 1 (`ApplyTheme`, `CenteredDialog`) |
| §4 Install wizard (Pages welcome→…→deploy; Modal/List/checklist/Form/TextView; populate Answers; render unchanged; back-nav; `--from --non-interactive`) | Tasks 2 (Flow), 3 (screens + Pages navigator), 4 (install.go swap + headless smoke). **Masked secret InputField — deliberately deferred** (see Global Constraints; no Answers field to populate). |
| §5 Monitor (Flex header/table/footer; packages + apps views; live refresh via kubectl wrapper + State; SetInputCapture nav; read-only) | Tasks 5 (`CurrentContext`), 6 (row-builders), 7 (tview app + refresh + wiring) |
| §6 File structure | Matches, plus `wizard/flow.go` + `*_test.go` (the testable controller — a faithful refinement of the spec's wizard.go/screens.go split) |
| §7 Testing (Flow unit-tested; render tests; monitor row-builders unit-tested with appcatalog fakes; rendering manual) | Tasks 2, 4, 6; smoke in 4, 7 |
| §8 MVP scope (theme; full wizard → Answers, dry-run deploy; monitor packages + apps; defer pods/events/drill-in/Day-2/streaming) | All tasks; deferrals honored (number-key view switch only; no drill-in; deploy stub kept) |
| §9 Dependencies (add tview/tcell, remove bubbletea/lipgloss; static binary) | Tasks 1 + 4 |

**2. Placeholder scan:** No "TODO/implement later/add error handling" steps — every code step ships complete code; every run step states the exact command + expected output. Color/rendering values are concrete constants (tuned during smoke, not placeholders).

**3. Type consistency:** `Flow` setter names match between Task 2 (definition) and Task 3 (calls): `SetPosture/SetSizing/SetDomain/ToggleService/ServiceChecked/SetSSO/SetOIDCIssuer/NeedsOIDCIssuer/SetSecrets/SetAgePublicKey/NeedsAgeKey/Answers/Validate`. `Kube.CurrentContext()` defined in Task 5, consumed in Task 7. `buildPackageRows/buildAppRows/PackageRow/AppRow` defined in Task 6, consumed in Task 7. `wizard.Run(cat, version)` defined in Task 3, consumed in Task 4. `tui.ApplyTheme/Title/CenteredDialog` defined in Task 1, consumed in Tasks 3 + 7.

**Deliberate deviations from the spec (flagged for the user):**
1. **Masked secret `InputField` not built** — `config.Answers` has no secret-value field; the front-end swap must leave `render` unchanged, so there is nothing to bind. `AgePublicKey` (a public value) uses a normal input.
2. **`wizard/flow.go` added** beyond the spec's `wizard.go`/`screens.go` — isolates the unit-tested logic from the smoke-tested rendering (directly serves spec §7's "flow controller, unit-tested as given inputs → produced Answers").
3. **Domain captured on its own page** (after sizing) and **OIDC issuer / age key on conditional pages** — the spec lists these as Form InputFields without a dedicated step; per-field pages keep each screen single-responsibility and the navigation predicates testable.
