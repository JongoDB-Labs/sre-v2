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
	// Inspect returns `zarf package inspect <ref>` output (manifest + metadata).
	Inspect(ref string) ([]byte, error)
}

// execZarf is the real Zarf wrapper.
type execZarf struct{}

// NewZarf returns the production Zarf wrapper.
func NewZarf() Zarf { return execZarf{} }

// Inspect runs `zarf package inspect <ref>` and returns its stdout.
func (execZarf) Inspect(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "package", "inspect", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf package inspect %s: %w", ref, err)
	}
	return out, nil
}
