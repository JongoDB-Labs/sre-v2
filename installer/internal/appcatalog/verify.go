package appcatalog

import (
	"fmt"
	"os/exec"
)

// commandContext builds external commands for this package. Like the source
// package's, it is a swappable var so the real exec wrappers' command assembly
// is unit-tested without running the real binaries.
var commandContext = exec.Command

// Cosign is the slice of the `cosign` CLI this package orchestrates for keyless
// signature verification. We shell out to cosign — never reimplement it.
type Cosign interface {
	// Verify runs a keyless cosign verification of ref against the expected signer
	// identity regexp and OIDC issuer; a non-nil error means verification failed.
	Verify(ref, identityRegexp, issuer string) error
}

// execCosign is the real Cosign wrapper.
type execCosign struct{}

// NewCosign returns the production Cosign wrapper.
func NewCosign() Cosign { return execCosign{} }

// Verify runs `cosign verify <ref> --certificate-identity-regexp … --certificate-oidc-issuer …`.
func (execCosign) Verify(ref, identityRegexp, issuer string) error {
	cmd := commandContext("cosign", "verify", ref,
		"--certificate-identity-regexp", identityRegexp,
		"--certificate-oidc-issuer", issuer)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cosign verify %s: %w: %s", ref, err, out)
	}
	return nil
}

// CheckSignature is the fail-closed signature gate (spec §5.2, §8). It refuses
// to proceed unless the entry declares a valid expected signer identity, then
// delegates to cosign. ANY error — bad policy or a failed/absent signature —
// aborts the deploy, naming the expected identity so the operator knows what
// was required.
//
// Named CheckSignature (not Verify) to avoid a name collision with the Verify
// struct type that carries the catalog policy fields.
func CheckSignature(c Cosign, e Entry, ref string) error {
	if e.Verify.IdentityRegexp == "" || e.Verify.Issuer == "" {
		return fmt.Errorf("verify: %s has no expected signer identity/issuer configured; refusing to deploy unverified (fail-closed)", e.Name)
	}
	if !reValid(e.Verify.IdentityRegexp) {
		return fmt.Errorf("verify: %s identityRegexp %q is not a valid regexp; refusing to deploy (fail-closed)", e.Name, e.Verify.IdentityRegexp)
	}
	if err := c.Verify(ref, e.Verify.IdentityRegexp, e.Verify.Issuer); err != nil {
		return fmt.Errorf("verify: signature check failed for %s (expected signer identity %q, issuer %q): %w",
			e.Name, e.Verify.IdentityRegexp, e.Verify.Issuer, err)
	}
	return nil
}
