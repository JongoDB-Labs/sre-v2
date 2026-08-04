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
type Zarf interface {
	// RegistryDigest returns the OCI manifest digest for a tagged ref, via
	// `zarf tools registry digest` (embedded crane). Output is one bare
	// `sha256:…` line — the exact digest cosign signs and verifies.
	RegistryDigest(ref string) ([]byte, error)
	// Inspect returns `zarf package inspect <ref>` output (manifest + metadata).
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

// Inspect runs `zarf package inspect <ref>` and returns its stdout (for preflight checks).
func (execZarf) Inspect(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "package", "inspect", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf package inspect %s: %w", ref, err)
	}
	return out, nil
}
