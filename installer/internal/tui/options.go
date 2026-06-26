package tui

import "github.com/JongoDB-Labs/sre-v2/installer/internal/config"

// option pairs a stored answer value with its on-screen label and one-line help.
// The slices below are the single-select menus; slice order is the cursor index
// space and the on-screen order.
type option[T any] struct {
	value T
	label string
	help  string
}

// postureOptions back the posture screen.
var postureOptions = []option[config.Posture]{
	{config.PostureBaseline, "Baseline", "upstream images, relaxed defaults (lab / connected)"},
	{config.PostureDoD, "DoD-hardened", "registry1 (Iron Bank) images, FIPS, strict netpol (ATO)"},
}

// sizingOptions back the sizing screen.
var sizingOptions = []option[config.Sizing]{
	{config.SizingSmall, "Small", "single-node lab, slim Core (~4 vCPU / 16 GiB)"},
	{config.SizingMedium, "Medium", "full UDS Core (12+ vCPU / 32+ GiB)"},
	{config.SizingLarge, "Large", "HA, production-shaped envelope"},
}

// ssoOptions back the SSO screen.
var ssoOptions = []option[config.SSOMode]{
	{config.SSOKeycloak, "Keycloak", "deploy the bundled Keycloak IdP (core-identity-authorization)"},
	{config.SSOExternalOIDC, "External OIDC", "point at an existing OIDC provider"},
	{config.SSONone, "None", "no SSO (lab only)"},
}

// secretsOptions back the secrets screen.
var secretsOptions = []option[config.SecretsMode]{
	{config.SecretsSOPSAge, "SOPS age", "encrypt secrets with a SOPS age key (default)"},
	{config.SecretsExternal, "External", "defer to an external secrets manager"},
}
