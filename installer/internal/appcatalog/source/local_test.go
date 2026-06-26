package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

func TestLocal_ResolveTarball(t *testing.T) {
	dir := t.TempDir()
	tar := filepath.Join(dir, "cosmos.tar.zst")
	if err := os.WriteFile(tar, []byte("fake-bundle-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref, digest, err := Local{}.Resolve(appcatalog.Entry{
		Name:   "cosmos",
		Source: appcatalog.Source{Type: appcatalog.SourceLocal, Ref: tar},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref != tar {
		t.Errorf("ref = %q, want %q", ref, tar)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Errorf("digest = %q, want sha256:<64-hex>", digest)
	}
}

func TestLocal_ResolveDir(t *testing.T) {
	dir := t.TempDir()
	ref, digest, err := Local{}.Resolve(appcatalog.Entry{
		Name:   "cosmos",
		Source: appcatalog.Source{Type: appcatalog.SourceLocal, Ref: dir},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ref != dir || digest != "" {
		t.Errorf("dir resolve = (%q,%q), want (%q,\"\")", ref, digest, dir)
	}
}

func TestLocal_ResolveMissing(t *testing.T) {
	_, _, err := Local{}.Resolve(appcatalog.Entry{
		Source: appcatalog.Source{Type: appcatalog.SourceLocal, Ref: "/no/such/path"},
	})
	if err == nil {
		t.Error("Resolve should error on a missing path")
	}
}
