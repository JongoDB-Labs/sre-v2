package source

import (
	"fmt"
	"regexp"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// digestRe matches a sha256 OCI digest anywhere in zarf inspect output.
var digestRe = regexp.MustCompile(`sha256:[a-f0-9]{64}`)

// OCI resolves a registry ref to a digest-pinned ref by inspecting the package.
// It is airgap-safe against an in-cluster/airgap registry (spec §4). The tag is
// pinned to a digest so verify and deploy act on immutable content.
type OCI struct {
	// Zarf orchestrates `zarf package inspect`; tests inject a fake.
	Zarf Zarf
}

// Resolve inspects the OCI ref, extracts its sha256 digest, and returns the
// digest-pinned ref plus the digest.
func (o OCI) Resolve(e appcatalog.Entry) (string, string, error) {
	out, err := o.Zarf.Inspect(e.Source.Ref)
	if err != nil {
		return "", "", fmt.Errorf("source(oci): inspect %s: %w", e.Source.Ref, err)
	}
	digest, err := parseDigest(out)
	if err != nil {
		return "", "", fmt.Errorf("source(oci): %s: %w", e.Source.Ref, err)
	}
	return e.Source.Ref + "@" + digest, digest, nil
}

// parseDigest extracts the first sha256:… digest from zarf inspect output.
func parseDigest(inspectOutput []byte) (string, error) {
	if m := digestRe.Find(inspectOutput); m != nil {
		return string(m), nil
	}
	return "", fmt.Errorf("no sha256 digest found in package metadata")
}
