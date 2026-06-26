// Package source resolves a catalog entry to a deployable, verifiable package
// ref via pluggable adapters: local (dir/tarball on disk) and oci (registry, by
// tag → digest) are MVP and airgap-safe; github (release asset) is connected-only
// and DEFERRED. Each adapter is Resolve(entry) → (ref, digest).
package source

import "github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"

// Adapter resolves a catalog entry to a concrete, deployable package reference
// and (where the source is content-addressable) its sha256 digest. A directory
// source has no digest and returns "".
type Adapter interface {
	Resolve(e appcatalog.Entry) (ref string, digest string, err error)
}
