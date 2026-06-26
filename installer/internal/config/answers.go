// Package config holds the installer's answer model — the decisions a wizard run
// (or a replayed answers.yaml) captures before anything is rendered or deployed:
// posture, sizing, selected services, SSO, domain, and secrets mode. These are
// plain data structs with load/save helpers so the CLI and TUI share one model.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Posture selects the security/hardening profile and, downstream, the image
// flavor (upstream vs Iron Bank registry1) and FIPS/netpol/retention defaults.
type Posture string

const (
	// PostureBaseline is the lab/connected profile: upstream images, relaxed defaults.
	PostureBaseline Posture = "Baseline"
	// PostureDoD is the hardened profile: registry1 (Iron Bank) images, FIPS, strict netpol.
	PostureDoD Posture = "DoD"
)

// Sizing selects the resource envelope (replicas, requests/limits, PG instances,
// storage) for the substrate.
type Sizing string

const (
	// SizingSmall targets a single-node lab (~4 vCPU / 16 GiB, slim core).
	SizingSmall Sizing = "Small"
	// SizingMedium targets full UDS Core on a modest node (12+ vCPU / 32+ GiB).
	SizingMedium Sizing = "Medium"
	// SizingLarge targets an HA, production-shaped envelope.
	SizingLarge Sizing = "Large"
)

// SSOMode selects how identity is provided to the substrate and its apps.
type SSOMode string

const (
	// SSOKeycloak deploys the bundled Keycloak (the core-identity-authorization layer).
	SSOKeycloak SSOMode = "Keycloak"
	// SSOExternalOIDC points at an external OIDC provider instead of deploying Keycloak.
	SSOExternalOIDC SSOMode = "ExternalOIDC"
	// SSONone disables SSO (lab-only; consoles fall back to direct access).
	SSONone SSOMode = "None"
)

// SecretsMode selects how cluster secrets are managed.
type SecretsMode string

const (
	// SecretsSOPSAge encrypts secrets with a SOPS age key (the documented default).
	SecretsSOPSAge SecretsMode = "SOPSAge"
	// SecretsExternal defers to an external secrets manager / out-of-band provisioning.
	SecretsExternal SecretsMode = "External"
)

// Answers is the complete installer answer model. One Answers value fully
// determines the two rendered files (uds-config.yaml + values.overlay.yaml).
type Answers struct {
	// Posture is the security profile (Baseline | DoD).
	Posture Posture `yaml:"posture"`
	// Sizing is the resource envelope (Small | Medium | Large).
	Sizing Sizing `yaml:"sizing"`
	// Services lists the catalog entry IDs to deploy (required entries are implied).
	Services []string `yaml:"services"`
	// SSO selects the identity mode (Keycloak | ExternalOIDC | None).
	SSO SSOMode `yaml:"sso"`
	// OIDCIssuer is the external issuer URL, used only when SSO == ExternalOIDC.
	OIDCIssuer string `yaml:"oidcIssuer,omitempty"`
	// Domain is the base domain for ingress (e.g. "uds.dev").
	Domain string `yaml:"domain"`
	// Secrets selects the secrets-management mode (SOPSAge | External).
	Secrets SecretsMode `yaml:"secrets"`
	// AgePublicKey is the SOPS age recipient, used only when Secrets == SOPSAge.
	AgePublicKey string `yaml:"agePublicKey,omitempty"`
}

// Default returns a sensible starting point for an interactive run: the lab
// profile (Baseline / Small / Keycloak / SOPS) over the uds.dev domain.
func Default() Answers {
	return Answers{
		Posture:  PostureBaseline,
		Sizing:   SizingSmall,
		Services: []string{"core-identity-authorization", "core-runtime-security", "core-monitoring"},
		SSO:      SSOKeycloak,
		Domain:   "uds.dev",
		Secrets:  SecretsSOPSAge,
	}
}

// HasService reports whether the given catalog entry ID is selected.
func (a Answers) HasService(id string) bool {
	for _, s := range a.Services {
		if s == id {
			return true
		}
	}
	return false
}

// Validate checks the answers for internal consistency, returning a combined
// error describing every problem found (or nil when the answers are coherent).
func (a Answers) Validate() error {
	var errs []string

	switch a.Posture {
	case PostureBaseline, PostureDoD:
	default:
		errs = append(errs, fmt.Sprintf("posture %q is not one of Baseline|DoD", a.Posture))
	}

	switch a.Sizing {
	case SizingSmall, SizingMedium, SizingLarge:
	default:
		errs = append(errs, fmt.Sprintf("sizing %q is not one of Small|Medium|Large", a.Sizing))
	}

	switch a.SSO {
	case SSOKeycloak, SSONone:
	case SSOExternalOIDC:
		if a.OIDCIssuer == "" {
			errs = append(errs, "sso ExternalOIDC requires oidcIssuer to be set")
		}
	default:
		errs = append(errs, fmt.Sprintf("sso %q is not one of Keycloak|ExternalOIDC|None", a.SSO))
	}

	switch a.Secrets {
	case SecretsSOPSAge, SecretsExternal:
	default:
		errs = append(errs, fmt.Sprintf("secrets %q is not one of SOPSAge|External", a.Secrets))
	}

	if a.Domain == "" {
		errs = append(errs, "domain must not be empty")
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid answers: %v", errs)
	}
	return nil
}

// Load reads and parses an answers.yaml file from path.
func Load(path string) (*Answers, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var a Answers
	if err := yaml.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &a, nil
}

// Save writes the answers to path as YAML with 0o644 permissions.
func (a Answers) Save(path string) error {
	data, err := yaml.Marshal(a)
	if err != nil {
		return fmt.Errorf("config: marshal answers: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}
