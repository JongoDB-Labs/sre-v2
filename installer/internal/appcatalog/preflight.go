package appcatalog

import (
	"fmt"
	"regexp"
)

// packageCRRe detects a UDS Package CR in zarf inspect output. Its presence means
// the UDS Operator will auto-wire cohesion (expose/sso/allow) on deploy; absence
// means the app will deploy but not self-wire — worth a warning, not a block.
// NOTE: this regex assumes zarf inspect's YAML output; it would miss a future --output json format.
var packageCRRe = regexp.MustCompile(`(?m)^\s*kind:\s*Package\b`)

// Warning is one advisory preflight finding. Preflight is advisory (spec §5.3):
// findings never block the deploy; they inform the operator.
type Warning struct {
	// Code is a stable machine label, e.g. "no-package-cr" or "missing-require".
	Code string
	// Message is the human-readable advisory.
	Message string
}

// Inspector is the slice of zarf this check needs. It matches source.Zarf so the
// production caller passes the same wrapper; tests pass a fake.
type Inspector interface {
	Inspect(ref string) ([]byte, error)
}

// Preflight inspects the resolved package and returns advisory warnings: one if
// no UDS Package CR is present (no auto-cohesion), and one per `requires` service
// missing from the live cluster. It returns an error ONLY when the package cannot
// be inspected at all (an I/O problem the caller should surface); cohesion and
// requires gaps are warnings, because the deploy proceeds (the post-deploy confirm
// in step 6 is authoritative).
//
// Advisory error contract: the returned error is advisory — it signals only that
// the cohesion scan could not run (e.g. a zarf Inspect I/O failure). Per spec §5.3,
// the install MUST proceed even if preflight cannot scan. Callers SHOULD log this
// error but MUST NOT abort the install on it. Genuine cohesion / requires gaps are
// surfaced as Warnings, never as errors.
func Preflight(z Inspector, e Entry, ref string, installedRequires map[string]bool) ([]Warning, error) {
	out, err := z.Inspect(ref)
	if err != nil {
		return nil, fmt.Errorf("preflight: inspect %s: %w", ref, err)
	}
	var warns []Warning
	if !hasPackageCR(out) {
		warns = append(warns, Warning{
			Code:    "no-package-cr",
			Message: fmt.Sprintf("%s has no UDS Package CR; it will deploy but not auto-wire cohesion (ingress/SSO/netpol)", e.Name),
		})
	}
	for _, req := range e.Requires {
		if !installedRequires[req] {
			warns = append(warns, Warning{
				Code:    "missing-require",
				Message: fmt.Sprintf("%s requires %q but it is not installed on the substrate; the app may degrade", e.Name, req),
			})
		}
	}
	return warns, nil
}

// hasPackageCR reports whether zarf inspect output contains a UDS Package CR.
func hasPackageCR(inspectOutput []byte) bool {
	return packageCRRe.Match(inspectOutput)
}
