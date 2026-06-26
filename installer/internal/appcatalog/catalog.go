// Package appcatalog deploys mission apps onto a running SRE substrate from a
// catalog.yaml: it resolves an app's source to a verifiable package ref, checks
// the cosign signature (fail-closed), advisory-preflights cohesion, deploys via
// uds, and records the install in a ConfigMap. It is the shared backend behind
// the `srectl app` commands (and, later, `srectl serve`). It deliberately shares
// no types with the round-1 internal/catalog (substrate services vs mission apps).
package appcatalog

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// SourceType selects which source adapter resolves an app entry to a package ref.
type SourceType string

const (
	// SourceLocal resolves a directory or *.tar.zst on disk (airgap-ok).
	SourceLocal SourceType = "local"
	// SourceOCI resolves a bundle/package in an OCI registry by tag (airgap-ok).
	SourceOCI SourceType = "oci"
	// SourceGitHub resolves a release asset on a repo (connected-only; DEFERRED).
	SourceGitHub SourceType = "github"
)

// Source is where an app's package comes from.
type Source struct {
	// Type selects the adapter (local | oci | github).
	Type SourceType `yaml:"type"`
	// Ref is the adapter-specific locator: a path (local) or a registry ref (oci).
	Ref string `yaml:"ref"`
}

// Verify is the expected cosign keyless signer identity for an app's package.
type Verify struct {
	// IdentityRegexp matches the signing workflow's certificate identity.
	IdentityRegexp string `yaml:"identityRegexp"`
	// Issuer is the expected OIDC issuer (e.g. GitHub Actions).
	Issuer string `yaml:"issuer"`
}

// Entry is one mission app in the catalog.
type Entry struct {
	// Name is the unique app key, e.g. "cosmos".
	Name string `yaml:"name"`
	// Version is the app version the entry pins, e.g. "2.102.0".
	Version string `yaml:"version"`
	// Description is a one-line human summary.
	Description string `yaml:"description"`
	// Source is where the package is resolved from.
	Source Source `yaml:"source"`
	// Verify is the expected signer identity (cosign keyless).
	Verify Verify `yaml:"verify"`
	// Requires lists substrate services the app needs (preflight hints).
	Requires []string `yaml:"requires"`
}

// Catalog is the full set of deployable mission apps.
type Catalog struct {
	// APIVersion is the catalog schema version, e.g. "sre/v1".
	APIVersion string `yaml:"apiVersion"`
	// Apps lists the mission-app entries.
	Apps []Entry `yaml:"apps"`
}

// Load reads, parses, and validates catalog.yaml at path. A missing or invalid
// catalog is an operator error (clear message, non-zero exit), not a build error.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("appcatalog: read %s: %w", path, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("appcatalog: parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Find returns the entry with the given name and whether it was found.
func (c *Catalog) Find(name string) (Entry, bool) {
	for _, e := range c.Apps {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Validate checks the catalog for structural problems, returning a combined error
// describing every issue found (or nil when the catalog is coherent).
func (c *Catalog) Validate() error {
	var errs []string
	if c.APIVersion == "" {
		errs = append(errs, "apiVersion must be set")
	}
	if len(c.Apps) == 0 {
		errs = append(errs, "catalog has no apps")
	}
	seen := map[string]bool{}
	for i, e := range c.Apps {
		where := fmt.Sprintf("apps[%d]", i)
		if e.Name == "" {
			errs = append(errs, where+": name must not be empty")
		} else if seen[e.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate app name %q", where, e.Name))
		}
		seen[e.Name] = true
		switch e.Source.Type {
		case SourceLocal, SourceOCI, SourceGitHub:
		default:
			errs = append(errs, fmt.Sprintf("%s: source.type %q is not one of local|oci|github", where, e.Source.Type))
		}
		if e.Source.Ref == "" {
			errs = append(errs, where+": source.ref must not be empty")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid catalog: %v", errs)
	}
	return nil
}

// writeFile writes s to path with 0o644 permissions. Shared by tests and the
// state writer; kept here so the package has one small file helper.
func writeFile(path, s string) error {
	return os.WriteFile(path, []byte(s), 0o644)
}

// reValid reports whether expr compiles as a regexp (used by verify input checks).
func reValid(expr string) bool {
	_, err := regexp.Compile(expr)
	return err == nil
}
