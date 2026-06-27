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
