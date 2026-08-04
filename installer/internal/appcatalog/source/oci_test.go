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
	calls  int
}

func (f *fakeZarf) RegistryDigest(ref string) ([]byte, error) {
	f.gotRef = ref
	f.calls++
	return f.out, f.err
}

func (f *fakeZarf) Inspect(ref string) ([]byte, error) {
	return f.out, f.err
}

var testDigest = "sha256:" + strings.Repeat("a", 64)

func TestOCI_ResolveAppendsVersionTag(t *testing.T) {
	fz := &fakeZarf{out: []byte(testDigest + "\n")}
	ref, digest, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Version: "1.27.0-1",
		Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/bundles/gitea"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fz.gotRef != "ghcr.io/x/bundles/gitea:1.27.0-1" {
		t.Errorf("digest lookup ref = %q, want the catalog version appended as the tag", fz.gotRef)
	}
	if digest != testDigest {
		t.Errorf("digest = %q, want %q", digest, testDigest)
	}
	if ref != "ghcr.io/x/bundles/gitea@"+testDigest {
		t.Errorf("ref = %q, want tagless ref pinned @digest", ref)
	}
}

func TestOCI_ResolveRespectsExistingTag(t *testing.T) {
	fz := &fakeZarf{out: []byte(testDigest + "\n")}
	ref, _, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Version: "9.9.9", // must be IGNORED — the ref's own tag wins
		Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/bundles/gitea:2.0.0"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fz.gotRef != "ghcr.io/x/bundles/gitea:2.0.0" {
		t.Errorf("digest lookup ref = %q, want the ref's own tag preserved", fz.gotRef)
	}
	if ref != "ghcr.io/x/bundles/gitea@"+testDigest {
		t.Errorf("ref = %q, want the tag replaced by @digest in the pinned ref", ref)
	}
}

func TestOCI_ResolveRegistryWithPort(t *testing.T) {
	// A registry host:port colon must not be mistaken for a tag.
	fz := &fakeZarf{out: []byte(testDigest + "\n")}
	_, _, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Version: "1.0.0",
		Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "registry.local:5000/bundles/gitea"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fz.gotRef != "registry.local:5000/bundles/gitea:1.0.0" {
		t.Errorf("digest lookup ref = %q, want version appended despite host:port colon", fz.gotRef)
	}
}

func TestOCI_ResolveAlreadyPinnedSkipsRegistry(t *testing.T) {
	pinned := "ghcr.io/x/bundles/gitea@" + testDigest
	fz := &fakeZarf{}
	ref, digest, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Source: appcatalog.Source{Type: appcatalog.SourceOCI, Ref: pinned},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fz.calls != 0 {
		t.Errorf("registry consulted %d times for an already-pinned ref, want 0 (airgap-friendly)", fz.calls)
	}
	if ref != pinned || digest != testDigest {
		t.Errorf("got (%q, %q), want the pinned ref echoed with its digest", ref, digest)
	}
}

func TestOCI_ResolveNoVersionNoTagFails(t *testing.T) {
	_, _, err := OCI{Zarf: &fakeZarf{}}.Resolve(appcatalog.Entry{
		Name:   "gitea",
		Source: appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/bundles/gitea"},
	})
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Errorf("err = %v, want an error telling the operator the entry needs a version (or the ref a tag)", err)
	}
}

func TestOCI_ResolveRejectsChattyDigestOutput(t *testing.T) {
	// The old parser grabbed the FIRST sha256 anywhere in `zarf package inspect`
	// output — wrong digest risk. The new contract: stdout must be exactly one
	// bare digest line, or we fail closed.
	fz := &fakeZarf{out: []byte("some warning\n" + testDigest + "\n")}
	_, _, err := OCI{Zarf: fz}.Resolve(appcatalog.Entry{
		Version: "1.0.0",
		Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/bundles/gitea"},
	})
	if err == nil {
		t.Error("Resolve should fail closed on non-bare digest output")
	}
}

func TestOCI_ResolveRegistryError(t *testing.T) {
	_, _, err := OCI{Zarf: &fakeZarf{err: errors.New("registry unreachable")}}.Resolve(
		appcatalog.Entry{Version: "1.0.0", Source: appcatalog.Source{Ref: "ghcr.io/x/bundles/gitea"}})
	if err == nil {
		t.Error("Resolve should propagate a registry error")
	}
}
