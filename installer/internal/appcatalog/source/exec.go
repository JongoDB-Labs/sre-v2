package source

import (
	"fmt"
	"os/exec"
)

// commandContext builds external commands. It is a package var so tests can swap
// it for a fake that records argv and returns a harmless command — letting us
// unit-test command assembly without running the real binary.
var commandContext = exec.Command

// Zarf is the slice of the `zarf` CLI this package orchestrates. We shell out to
// zarf — we never reimplement it (spec §3). Tests use a fake Zarf.
//
// The interface has two methods due to a compile-time constraint: cmd/srectl/app.go's
// newAppDeps assigns the same source.Zarf value to both the Zarf field (used by the OCI
// source adapter) and the Inspect field (which expects appcatalog.Inspector, used by
// preflight checks). Interface-to-interface assignability requires Inspect in this
// method set, even though the new OCI resolver (S1) only calls RegistryDigest.
type Zarf interface {
	// RegistryDigest returns the OCI manifest digest for a tagged ref, via
	// `zarf tools registry digest` (embedded crane). Output is one bare
	// `sha256:…` line — the exact digest cosign signs and verifies.
	// Used by the oci source adapter to resolve refs to digest-pinned refs.
	RegistryDigest(ref string) ([]byte, error)
	// Inspect returns `zarf package inspect definition <ref>` output (manifest +
	// metadata). zarf >= ~v0.79 requires a subcommand after `package inspect`
	// ("definition", "sbom", ...); the bare `zarf package inspect <ref>` form
	// errors on every current zarf. Used by preflight checks via the
	// appcatalog.Inspector interface.
	Inspect(ref string) ([]byte, error)
}

// execZarf is the real Zarf wrapper.
type execZarf struct{}

// NewZarf returns the production Zarf wrapper.
func NewZarf() Zarf { return execZarf{} }

// RegistryDigest runs `zarf tools registry digest <ref>` and returns its stdout.
func (execZarf) RegistryDigest(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "tools", "registry", "digest", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf tools registry digest %s: %w", ref, err)
	}
	return out, nil
}

// Inspect runs `zarf package inspect definition <ref>` and returns its stdout
// (for preflight checks). The `definition` subcommand is required on modern
// zarf (>= ~v0.79) — `zarf package inspect <ref>` alone errors with "requires
// a subcommand".
func (execZarf) Inspect(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "package", "inspect", "definition", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf package inspect definition %s: %w", ref, err)
	}
	return out, nil
}
