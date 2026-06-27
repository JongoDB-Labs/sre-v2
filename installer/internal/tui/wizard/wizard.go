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
