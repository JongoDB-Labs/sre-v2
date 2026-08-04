package source

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// digestRe matches exactly one bare sha256 OCI digest (strict — the old
// find-anywhere match against `zarf package inspect` output could pin the
// wrong digest; see spec §5.2).
var digestRe = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// OCI resolves a registry ref to a digest-pinned ref via the registry's
// manifest digest (`zarf tools registry digest`, i.e. crane). The digest is
// the exact artifact cosign signed, so verify and deploy act on immutable,
// signature-checked content. Airgap-safe against an in-cluster registry.
type OCI struct {
	// Zarf resolves a tagged ref to its manifest digest; tests inject a fake.
	Zarf Zarf
}

// Resolve pins e.Source.Ref to a digest. An untagged ref gets the catalog
// entry's version appended as its tag (so catalog.yaml's `version:` is the
// single source of what gets installed); a ref already pinned `@sha256:…` is
// returned as-is without touching the registry.
func (o OCI) Resolve(e appcatalog.Entry) (string, string, error) {
	ref := e.Source.Ref
	if i := strings.Index(ref, "@"); i >= 0 {
		digest := ref[i+1:]
		if !digestRe.MatchString(digest) {
			return "", "", fmt.Errorf("source(oci): %s: malformed digest in pinned ref", ref)
		}
		return ref, digest, nil
	}
	tagged, err := taggedRef(e)
	if err != nil {
		return "", "", err
	}
	out, err := o.Zarf.RegistryDigest(tagged)
	if err != nil {
		return "", "", fmt.Errorf("source(oci): digest %s: %w", tagged, err)
	}
	digest, err := parseDigest(out)
	if err != nil {
		return "", "", fmt.Errorf("source(oci): %s: %w", tagged, err)
	}
	return trimTag(tagged) + "@" + digest, digest, nil
}

// taggedRef returns the ref to resolve: the ref's own tag wins; otherwise the
// catalog entry's version becomes the tag. No version + no tag = operator
// error (we refuse to silently resolve :latest).
func taggedRef(e appcatalog.Entry) (string, error) {
	ref := e.Source.Ref
	slash := strings.LastIndex(ref, "/")
	if strings.Contains(ref[slash+1:], ":") {
		return ref, nil
	}
	if e.Version == "" {
		return "", fmt.Errorf("source(oci): %s: entry %q has no version and the ref has no tag — set the catalog entry's version", ref, e.Name)
	}
	return ref + ":" + e.Version, nil
}

// trimTag strips the tag from a tagged ref (registry host:port colons are
// left intact).
func trimTag(tagged string) string {
	slash := strings.LastIndex(tagged, "/")
	if i := strings.LastIndex(tagged, ":"); i > slash {
		return tagged[:i]
	}
	return tagged
}

// parseDigest expects stdout to be exactly one bare digest line (crane's
// contract). Anything else fails closed.
func parseDigest(out []byte) (string, error) {
	s := strings.TrimSpace(string(out))
	if digestRe.MatchString(s) {
		return s, nil
	}
	return "", fmt.Errorf("expected a bare sha256 digest, got %q", s)
}
