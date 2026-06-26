package appcatalog

import (
	"fmt"
)

// UDS is the slice of the `uds` CLI this package orchestrates. We shell out to
// uds (the UDS Operator does the actual cohesion reconcile) — never reimplement it.
type UDS interface {
	// Deploy runs `uds deploy <ref> --confirm`.
	Deploy(ref string) error
	// Remove runs `uds remove <name> --confirm`.
	Remove(name string) error
}

// execUDS is the real UDS wrapper.
type execUDS struct{}

// NewUDS returns the production UDS wrapper.
func NewUDS() UDS { return execUDS{} }

// Deploy runs `uds deploy <ref> --confirm`, streaming uds/zarf output.
func (execUDS) Deploy(ref string) error {
	if out, err := commandContext("uds", "deploy", ref, "--confirm").CombinedOutput(); err != nil {
		return fmt.Errorf("uds deploy %s: %w: %s", ref, err, out)
	}
	return nil
}

// Remove runs `uds remove <name> --confirm`.
func (execUDS) Remove(name string) error {
	if out, err := commandContext("uds", "remove", name, "--confirm").CombinedOutput(); err != nil {
		return fmt.Errorf("uds remove %s: %w: %s", name, err, out)
	}
	return nil
}

// Deploy actuates the package and, on failure, best-effort removes it so a
// half-wired app is not left behind (spec §5 step 4, §10). The original deploy
// error is what the caller sees; a rollback-remove failure is swallowed (the
// deploy error is the actionable one). The caller writes the install record only
// after Deploy returns nil.
func Deploy(u UDS, ref string) error {
	if err := u.Deploy(ref); err != nil {
		_ = u.Remove(ref) // best-effort cleanup; ignore its error
		return fmt.Errorf("deploy: %w", err)
	}
	return nil
}

// Remove tears the app down via `uds remove`.
func Remove(u UDS, name string) error {
	if err := u.Remove(name); err != nil {
		return fmt.Errorf("remove: %w", err)
	}
	return nil
}
