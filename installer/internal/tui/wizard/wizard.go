package wizard

import (
	"fmt"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// dialogWidth is the fixed width of every wizard dialog (centered on the backdrop).
const dialogWidth = 78

// page is one wizard screen: a name, a builder, the dialog height it needs, and
// an optional predicate that decides whether the screen is shown.
type page struct {
	name    string
	build   func() tview.Primitive
	height  int
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
		{name: "welcome", height: 22, build: func() tview.Primitive {
			return welcomeScreen(w.next, w.quit)
		}},
		{name: "posture", height: 13, build: func() tview.Primitive {
			intro := "Security and image profile for the substrate.\n" +
				"[#485260]Sets the image source (upstream vs Iron Bank), FIPS, and netpol.[-]"
			return radioScreen(intro, postureOptions, flow.Answers().Posture,
				func(v config.Posture) { flow.SetPosture(v); w.next() })
		}},
		{name: "sizing", height: 15, build: func() tview.Primitive {
			intro := "Resource envelope the substrate is shaped for.\n" +
				"[#485260]Sets replicas, requests/limits, and Postgres sizing.[-]"
			return radioScreen(intro, sizingOptions, flow.Answers().Sizing,
				func(v config.Sizing) { flow.SetSizing(v); w.next() })
		}},
		{name: "domain", height: 12, build: func() tview.Primitive {
			intro := "The DNS base domain your services are published under.\n" +
				"[#485260]e.g. uds.dev → cosmos.uds.dev, sso.uds.dev[-]"
			return inputScreen(intro, "Base domain", flow.Answers().Domain, "uds.dev", 36,
				flow.SetDomain, w.next, w.prev)
		}},
		{name: "services", height: 20, build: func() tview.Primitive {
			return checklistScreen(flow, w.next, w.prev)
		}},
		{name: "sso", height: 15, build: func() tview.Primitive {
			intro := "How operators and app users sign in.\n" +
				"[#485260]Keycloak is the bundled IdP every app shares.[-]"
			return radioScreen(intro, ssoOptions, flow.Answers().SSO,
				func(v config.SSOMode) { flow.SetSSO(v); w.next() })
		}},
		{name: "oidc", height: 12, visible: flow.NeedsOIDCIssuer, build: func() tview.Primitive {
			intro := "Your existing OpenID Connect issuer (realm) URL.\n" +
				"[#485260]Used because you chose External OIDC.[-]"
			return inputScreen(intro, "OIDC issuer URL", flow.Answers().OIDCIssuer,
				"https://idp.example/realms/x", 48, flow.SetOIDCIssuer, w.next, w.prev)
		}},
		{name: "secrets", height: 13, build: func() tview.Primitive {
			intro := "How cluster secrets are encrypted at rest.\n" +
				"[#485260]SOPS age keeps them in git, encrypted; Flux decrypts in-cluster.[-]"
			return radioScreen(intro, secretsOptions, flow.Answers().Secrets,
				func(v config.SecretsMode) { flow.SetSecrets(v); w.next() })
		}},
		{name: "agekey", height: 12, visible: flow.NeedsAgeKey, build: func() tview.Primitive {
			intro := "The age public key (recipient) secrets are encrypted to.\n" +
				"[#485260]Generate one with: age-keygen -o key.txt[-]"
			return inputScreen(intro, "Age recipient", flow.Answers().AgePublicKey,
				"age1...", 52, flow.SetAgePublicKey, w.next, w.prev)
		}},
		{name: "review", height: 20, build: func() tview.Primitive {
			return reviewScreen(flow, cat, w.next, w.prev)
		}},
		{name: "deploy", height: 13, build: func() tview.Primitive {
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

	// Wire the Pages as the application root before showing the first screen.
	app.SetRoot(pages, true)
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
	h := p.height
	if h == 0 {
		h = 22
	}
	framed := tui.CenteredDialog(dialogTitle(w.version, p.name), p.build(), dialogWidth, h)
	w.pages.AddAndSwitchToPage(p.name, framed, true)
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
