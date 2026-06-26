package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// fakeZarf is a hand-written test double for the Zarf wrapper.
type fakeZarf struct {
	out    []byte
	err    error
	gotRef string
}

func (f *fakeZarf) Inspect(ref string) ([]byte, error) {
	f.gotRef = ref
	return f.out, f.err
}

func TestOCI_ResolvePinsDigest(t *testing.T) {
	fz := &fakeZarf{out: []byte("metadata:\n  aggregateChecksum: deadbeef\nmanifestDigest: sha256:" +
		strings.Repeat("a", 64) + "\n")}
	ref, digest, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Source: appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/cosmos"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fz.gotRef != "ghcr.io/x/cosmos" {
		t.Errorf("inspect ref = %q, want ghcr.io/x/cosmos", fz.gotRef)
	}
	wantDigest := "sha256:" + strings.Repeat("a", 64)
	if digest != wantDigest {
		t.Errorf("digest = %q, want %q", digest, wantDigest)
	}
	if ref != "ghcr.io/x/cosmos@"+wantDigest {
		t.Errorf("ref = %q, want pinned ref@digest", ref)
	}
}

func TestOCI_ResolveInspectError(t *testing.T) {
	_, _, err := OCI{Zarf: &fakeZarf{err: errors.New("registry unreachable")}}.Resolve(
		appcatalog.Entry{Source: appcatalog.Source{Ref: "ghcr.io/x/cosmos"}})
	if err == nil {
		t.Error("Resolve should propagate an inspect error")
	}
}

func TestParseDigest_NoDigest(t *testing.T) {
	if _, err := parseDigest([]byte("metadata:\n  name: cosmos\n")); err == nil {
		t.Error("parseDigest should error when no sha256 digest is present")
	}
}
