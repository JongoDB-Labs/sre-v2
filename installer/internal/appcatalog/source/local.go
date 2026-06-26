package source

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// Local resolves a directory or a *.tar.zst package on disk. It is fully airgap
// -safe: no network, no cluster. A tarball yields a sha256 digest of its bytes;
// a directory yields an empty digest (a directory is not content-addressable).
type Local struct{}

// Resolve returns the on-disk ref and, for a regular file, its sha256 digest.
func (Local) Resolve(e appcatalog.Entry) (string, string, error) {
	info, err := os.Stat(e.Source.Ref)
	if err != nil {
		return "", "", fmt.Errorf("source(local): stat %s: %w", e.Source.Ref, err)
	}
	if info.IsDir() {
		return e.Source.Ref, "", nil
	}
	digest, err := sha256File(e.Source.Ref)
	if err != nil {
		return "", "", err
	}
	return e.Source.Ref, digest, nil
}

// sha256File returns "sha256:"+hex of the file at path.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("source(local): open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("source(local): hash %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
