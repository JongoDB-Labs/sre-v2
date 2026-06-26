package render

import (
	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
)

// udsConfig is the structure of uds-config.yaml — the UDS bundle variable set
// `uds deploy` reads. Field order here is the emitted order.
type udsConfig struct {
	// Flavor is the image flavor (upstream | registry1), resolved from posture.
	Flavor Flavor `yaml:"flavor"`
	// Domain is the base ingress domain.
	Domain string `yaml:"domain"`
	// Packages is the ordered list of catalog package IDs to deploy.
	Packages []string `yaml:"packages"`
	// Variables holds the bundle's shared variables.
	Variables udsVariables `yaml:"variables"`
}

// udsVariables are the shared bundle variables passed through to the packages.
type udsVariables struct {
	// Posture echoes the selected security profile for traceability.
	Posture config.Posture `yaml:"posture"`
	// Sizing echoes the selected sizing tier for traceability.
	Sizing config.Sizing `yaml:"sizing"`
	// SSOMode selects identity handling (Keycloak | ExternalOIDC | None).
	SSOMode config.SSOMode `yaml:"ssoMode"`
	// OIDCIssuer is set only for the ExternalOIDC mode.
	OIDCIssuer string `yaml:"oidcIssuer,omitempty"`
	// SecretsMode selects secrets handling (SOPSAge | External).
	SecretsMode config.SecretsMode `yaml:"secretsMode"`
	// AgePublicKey is the SOPS age recipient, set only for the SOPSAge mode.
	AgePublicKey string `yaml:"agePublicKey,omitempty"`
}

const udsConfigHeader = `# uds-config.yaml — UDS bundle variables for the SRE substrate.
# Rendered by srectl from answers.yaml. Re-runnable and safe to commit.
#   uds deploy <sre-bundle> --confirm   # consumes these variables
#
` + doNotEditLine + "\n"

// renderUDSConfig builds the uds-config.yaml content from the answers + catalog.
func renderUDSConfig(a config.Answers, cat *catalog.Catalog) (string, error) {
	cfg := udsConfig{
		Flavor:   FlavorFor(a.Posture),
		Domain:   a.Domain,
		Packages: selectedPackages(a, cat),
		Variables: udsVariables{
			Posture:      a.Posture,
			Sizing:       a.Sizing,
			SSOMode:      a.SSO,
			OIDCIssuer:   a.OIDCIssuer,
			SecretsMode:  a.Secrets,
			AgePublicKey: a.AgePublicKey,
		},
	}
	return marshalWithHeader(udsConfigHeader, cfg)
}
