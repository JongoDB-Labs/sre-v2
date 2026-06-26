// Package catalog describes the SRE substrate packages the installer can deploy:
// the UDS Core layers and the platform data-service operators. The catalog is
// embedded from catalog.yaml (kept in lock-step with bundle/uds-bundle.yaml) so
// the binary is self-contained and the CLI and TUI read the same source of truth.
package catalog

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed catalog.yaml
var embedded []byte

// Kind classifies a catalog entry by its role in the substrate.
type Kind string

const (
	// KindInit is the Zarf init package (registry + mutating agent).
	KindInit Kind = "init"
	// KindCore is a UDS Core layer (base, identity, runtime-security, monitoring).
	KindCore Kind = "core"
	// KindOperator is a platform data-service operator (PGO, MinIO).
	KindOperator Kind = "operator"
)

// Status flags an entry whose packaging is not yet final.
type Status string

const (
	// StatusReady marks an entry that is packaged and deployable.
	StatusReady Status = ""
	// StatusPending marks an entry whose packaging is still being decided.
	StatusPending Status = "pending"
)

// Entry is a single deployable package in the catalog.
type Entry struct {
	// ID is the UDS package name (matches bundle/uds-bundle.yaml), e.g. "core-base".
	ID string `yaml:"id"`
	// Name is a short human label for the entry.
	Name string `yaml:"name"`
	// Kind classifies the entry (init, core, operator).
	Kind Kind `yaml:"kind"`
	// Required is true for entries that are always deployed and not user-deselectable.
	Required bool `yaml:"required"`
	// Status flags entries that are not yet fully packaged (e.g. MinIO).
	Status Status `yaml:"status"`
	// Description is a one-line summary shown in the wizard and CLI.
	Description string `yaml:"description"`
}

// Optional reports whether the entry can be toggled by the operator.
func (e Entry) Optional() bool { return !e.Required }

// Pending reports whether the entry's packaging is still in flux.
func (e Entry) Pending() bool { return e.Status == StatusPending }

// Catalog is the full set of substrate packages, split into UDS Core layers and
// platform data-service operators.
type Catalog struct {
	// Version tracks the UDS Core version the bundle pins.
	Version string `yaml:"version"`
	// Layers are the Zarf init package and the UDS Core layers, in deploy order.
	Layers []Entry `yaml:"layers"`
	// Operators are the platform data-service operators (PGO, MinIO).
	Operators []Entry `yaml:"operators"`
}

// Load parses the embedded catalog.yaml. It returns an error only if the embedded
// asset is malformed, which is a build-time problem rather than a runtime one.
func Load() (*Catalog, error) {
	var c Catalog
	if err := yaml.Unmarshal(embedded, &c); err != nil {
		return nil, fmt.Errorf("catalog: parse embedded catalog.yaml: %w", err)
	}
	return &c, nil
}

// MustLoad is like Load but panics on error. The catalog is embedded at build
// time, so a failure here means the binary itself is broken.
func MustLoad() *Catalog {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// All returns every entry, layers first (deploy order) then operators.
func (c *Catalog) All() []Entry {
	out := make([]Entry, 0, len(c.Layers)+len(c.Operators))
	out = append(out, c.Layers...)
	out = append(out, c.Operators...)
	return out
}

// Required returns the entries that are always deployed (init, core-base, pgo).
func (c *Catalog) Required() []Entry {
	var out []Entry
	for _, e := range c.All() {
		if e.Required {
			out = append(out, e)
		}
	}
	return out
}

// Optional returns the entries the operator may select or skip.
func (c *Catalog) Optional() []Entry {
	var out []Entry
	for _, e := range c.All() {
		if e.Optional() {
			out = append(out, e)
		}
	}
	return out
}

// Find returns the entry with the given ID and whether it was found.
func (c *Catalog) Find(id string) (Entry, bool) {
	for _, e := range c.All() {
		if e.ID == id {
			return e, true
		}
	}
	return Entry{}, false
}
