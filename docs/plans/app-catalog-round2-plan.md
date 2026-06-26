# App-catalog Round-2 (MVP) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `srectl app {list,install,remove,status}` so a platform admin can deploy the signed cosmos UDS bundle onto a running SRE from a `catalog.yaml` — resolved by a local/OCI source adapter, cosign-verified fail-closed, cohesion-preflighted, deployed via `uds`, and tracked in a `sre-appcatalog-installs` ConfigMap.

**Architecture:** One Go package `installer/internal/appcatalog` owns the whole flow (catalog model → source adapters → verify → preflight → deploy → state); `installer/cmd/srectl/app.go` is a thin cobra surface over it, mirroring the round-1 `internal/catalog` + `cmd/srectl/install.go` split. Every external tool (`uds`/`zarf`/`cosign`/`kubectl`) is reached through a small exec-wrapper interface with a real `os/exec` impl and a test fake, so verify/preflight/deploy/state are fully unit-testable without a cluster. The round-1 `internal/catalog` (substrate services) and this round-2 `internal/appcatalog` (mission apps) stay deliberately separate packages with no shared types.

**Tech Stack:** Go (module floor `go 1.25.0`), `github.com/spf13/cobra` (CLI), `gopkg.in/yaml.v3` (catalog/state marshalling), `os/exec` (tool orchestration), Go stdlib `testing` (no test framework dependency — matches round-1). External binaries orchestrated, never reimplemented: `uds`, `zarf`, `cosign`, `kubectl`.

## Global Constraints

These are project-wide rules copied verbatim from the approved spec (`docs/specs/app-catalog-round2-design.md`). Every task below implicitly includes this section.

- **Go module path:** `github.com/JongoDB-Labs/sre-v2/installer` — all imports of the new package are `github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog` (and `.../source`). (from `installer/go.mod`)
- **Go version:** module floor is `go 1.25.0` (from `installer/go.mod`; the local toolchain is newer — do not bump the `go` directive).
- **Orchestrate, never reimplement:** deploy/verify/preflight orchestrate `uds`/`zarf`/`cosign` (and `kubectl` for state) by shelling out through small exec-wrapper **interfaces** — never reimplement them, and **stub them in tests** (spec §3, §5, §11). "One Go implementation owns catalog resolution, signature verification, deploy orchestration (**shells out to `uds`/`zarf` — never reimplements them**), and installed-app state."
- **Signature verification is mandatory + fail-closed** (spec §5.2, §8, §10): "no valid signature → abort, never deploy." A verify error, an unsigned ref, or a signer-identity mismatch all abort **before** any `uds deploy`. Name the expected identity in the error.
- **Airgap = `oci` + `local` only** (spec §4, §8, §12): the `local` and `oci` adapters must work fully disconnected; the `github` adapter is connected-only and is **deferred** out of this MVP.
- **Source-of-truth precedence** (spec §6): the install-record ConfigMap is convenience metadata; the live cluster (`kubectl get packages -A`) is authoritative. Drift is surfaced by `list --installed`, never silently reconciled.
- **Package separation** (spec §2): `internal/appcatalog` shares no types with the round-1 `internal/catalog`. Do not import one from the other.

**MVP scope (spec §12) — build exactly this, defer the rest:**
- Build now: `srectl app {list,install,remove,status}` · `catalog.yaml` · `local`+`oci` adapters · cosign-verify · cohesion preflight · `uds deploy`/`remove` · the install-record ConfigMap · cosmos as catalog entry #1 + the dogfood acceptance.
- Defer (do NOT build): `github` adapter · `srectl serve` + SP8 console · `app update`.

## Resolved design decisions (spec-ambiguity review)

Five spec ambiguities were resolved during planning — implement these as written:

1. **Authoritative cohesion check is integration, not unit code** (spec §5.6). The post-deploy
   confirmation (VirtualService present + `keycloak-client-secrets` entry if `sso` was declared)
   needs live cluster artifacts → it lives in the **Task 10 acceptance**, not a Go function. The
   unit layer covers resolve → verify → preflight → deploy → record.
2. **`local` tarball is verified; `local` directory is dev-only.** A `*.tar.zst` yields a real
   `sha256:` digest and **is** cosign-verified (fail-closed). A directory has no content digest and
   is **rejected unless `--allow-unsigned` is passed** (explicit dev opt-out) — fail-closed stays
   the default; no source silently bypasses verify.
3. **Rollback removes by name.** `UDS.Remove` is name-based; a failed-deploy rollback passes the
   app **name** (`entry.Name`), not the bundle ref.
4. **`installedBy` = the CLI actor.** Record the kubeconfig context / OS user; the OIDC subject
   lands with the deferred `srectl serve`.
5. **`requires` matches a substrate service id, not a literal service.** Check `requires` tokens
   against the installed substrate services (round-1 names: `pgo`, `minio`, …). The shipped cosmos
   entry uses **`requires: [pgo]`** (the Postgres operator), not `postgres`.

---

## Task Overview

| # | Task | Deliverable |
|---|------|-------------|
| 1 | Catalog model + load/validate | `appcatalog/catalog.go` — `Entry`/`Catalog` types, `Load`/`Validate` |
| 2 | Source `Adapter` interface + `local` adapter | `appcatalog/source/source.go`, `source/local.go` |
| 3 | `oci` adapter (tag → digest) | `appcatalog/source/oci.go` + the `zarf` exec wrapper |
| 4 | `verify` (cosign wrapper, fail-closed) | `appcatalog/verify.go` + the `cosign` exec wrapper |
| 5 | `preflight` (advisory cohesion/requires) | `appcatalog/preflight.go` (reuses the `zarf` wrapper) |
| 6 | `deploy` (uds deploy/remove orchestration) | `appcatalog/deploy.go` + the `uds` exec wrapper |
| 7 | `state` (install-record ConfigMap r/w) | `appcatalog/state.go` + the `kubectl` exec wrapper |
| 8 | `cmd/srectl/app.go` commands | `app {list,install,remove,status}` wiring 1–7 |
| 9 | The shipped `catalog.yaml` | repo-root `catalog.yaml`, cosmos = entry #1 |
| 10 | cosmos dogfood acceptance | `docs/app-onboarding.md` acceptance section (manual/integration) |

Dependency order: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8, then 9, then 10. Tasks 4/5/6/7 each introduce one exec wrapper; task 8 composes everything; tasks 9–10 are content + acceptance.

**Exec-wrapper testing strategy (read before Task 3).** Each external binary is wrapped in a tiny interface defined in the file that first needs it:

- `Zarf` interface — `Inspect(ref string) ([]byte, error)` (Task 3, reused Task 5).
- `Cosign` interface — `Verify(ref, identityRegexp, issuer string) error` (Task 4).
- `UDS` interface — `Deploy(ref string) error`, `Remove(name string) error` (Task 6).
- `Kube` interface — `GetConfigMap(ns, name string) ([]byte, error)`, `ApplyConfigMap(ns, name string, data map[string]string) error`, `ListPackages() ([]byte, error)`, `EnsureNamespace(ns string) error` (Task 7).

Every interface ships with: (a) a **real** impl (`exec…` struct) that builds an `*exec.Cmd` — its command-assembly is unit-tested by injecting a fake `commandContext` so no real binary runs; and (b) a hand-written **fake** in `*_test.go` that returns canned output/errors. Verify/preflight/deploy/state consume the interface, so their happy- and failure-path logic is tested against the fake with no cluster, no network, no binaries. This is the spec's "stub them in tests" (§11) made concrete.

---

### Task 1: Catalog model + load/validate `catalog.yaml`

Establishes the `appcatalog.Entry`/`Catalog` types and a validating loader. Unlike round-1's embed-only `catalog.Load`, this loads from a **path** (the shipped `catalog.yaml` lives at repo root, outside the binary) and **validates** every entry, because a malformed catalog is an operator error, not a build error (spec §10).

**Files:**
- Create: `installer/internal/appcatalog/catalog.go`
- Test: `installer/internal/appcatalog/catalog_test.go`

**Interfaces:**
- Consumes: nothing (leaf task).
- Produces:
  - `type SourceType string` with consts `SourceLocal SourceType = "local"`, `SourceOCI SourceType = "oci"`, `SourceGitHub SourceType = "github"`.
  - `type Source struct { Type SourceType; Ref string }` (yaml: `type`, `ref`).
  - `type Verify struct { IdentityRegexp string; Issuer string }` (yaml: `identityRegexp`, `issuer`).
  - `type Entry struct { Name, Version, Description string; Source Source; Verify Verify; Requires []string }` (yaml: `name`,`version`,`description`,`source`,`verify`,`requires`).
  - `type Catalog struct { APIVersion string; Apps []Entry }` (yaml: `apiVersion`, `apps`).
  - `func Load(path string) (*Catalog, error)` — reads + parses + validates; returns a clear error on missing/invalid.
  - `func (c *Catalog) Find(name string) (Entry, bool)`.
  - `func (c *Catalog) Validate() error` — combined error naming every problem.

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"path/filepath"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const validCatalog = `apiVersion: sre/v1
apps:
  - name: cosmos
    version: "2.102.0"
    description: "COSMOS — mission app"
    source:
      type: oci
      ref: ghcr.io/jongodb-labs/bundles/cosmos
    verify:
      identityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/"
      issuer: "https://token.actions.githubusercontent.com"
    requires: [postgres]
`

func TestLoad_ValidCatalog(t *testing.T) {
	c, err := Load(writeCatalog(t, validCatalog))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIVersion != "sre/v1" {
		t.Errorf("apiVersion = %q, want sre/v1", c.APIVersion)
	}
	got, ok := c.Find("cosmos")
	if !ok {
		t.Fatal("cosmos should be in the catalog")
	}
	if got.Source.Type != SourceOCI || got.Source.Ref == "" {
		t.Errorf("cosmos source = %+v, want oci with a ref", got.Source)
	}
	if got.Verify.IdentityRegexp == "" || got.Verify.Issuer == "" {
		t.Errorf("cosmos verify should be populated, got %+v", got.Verify)
	}
	if _, ok := c.Find("nope"); ok {
		t.Error("Find should miss unknown names")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("Load should error on a missing file")
	}
}

func TestValidate_RejectsBadEntries(t *testing.T) {
	const bad = `apiVersion: sre/v1
apps:
  - name: ""
    source:
      type: smoke-signals
  - name: cosmos
    version: "2.102.0"
    source:
      type: oci
      ref: ""
`
	if _, err := Load(writeCatalog(t, bad)); err == nil {
		t.Error("Load should reject empty name, unknown source type, and empty oci ref")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run TestLoad -v`
Expected: FAIL — `undefined: Load` / `undefined: writeFile` (package doesn't compile yet).

- [ ] **Step 3: Write minimal implementation**

```go
// Package appcatalog deploys mission apps onto a running SRE substrate from a
// catalog.yaml: it resolves an app's source to a verifiable package ref, checks
// the cosign signature (fail-closed), advisory-preflights cohesion, deploys via
// uds, and records the install in a ConfigMap. It is the shared backend behind
// the `srectl app` commands (and, later, `srectl serve`). It deliberately shares
// no types with the round-1 internal/catalog (substrate services vs mission apps).
package appcatalog

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// SourceType selects which source adapter resolves an app entry to a package ref.
type SourceType string

const (
	// SourceLocal resolves a directory or *.tar.zst on disk (airgap-ok).
	SourceLocal SourceType = "local"
	// SourceOCI resolves a bundle/package in an OCI registry by tag (airgap-ok).
	SourceOCI SourceType = "oci"
	// SourceGitHub resolves a release asset on a repo (connected-only; DEFERRED).
	SourceGitHub SourceType = "github"
)

// Source is where an app's package comes from.
type Source struct {
	// Type selects the adapter (local | oci | github).
	Type SourceType `yaml:"type"`
	// Ref is the adapter-specific locator: a path (local) or a registry ref (oci).
	Ref string `yaml:"ref"`
}

// Verify is the expected cosign keyless signer identity for an app's package.
type Verify struct {
	// IdentityRegexp matches the signing workflow's certificate identity.
	IdentityRegexp string `yaml:"identityRegexp"`
	// Issuer is the expected OIDC issuer (e.g. GitHub Actions).
	Issuer string `yaml:"issuer"`
}

// Entry is one mission app in the catalog.
type Entry struct {
	// Name is the unique app key, e.g. "cosmos".
	Name string `yaml:"name"`
	// Version is the app version the entry pins, e.g. "2.102.0".
	Version string `yaml:"version"`
	// Description is a one-line human summary.
	Description string `yaml:"description"`
	// Source is where the package is resolved from.
	Source Source `yaml:"source"`
	// Verify is the expected signer identity (cosign keyless).
	Verify Verify `yaml:"verify"`
	// Requires lists substrate services the app needs (preflight hints).
	Requires []string `yaml:"requires"`
}

// Catalog is the full set of deployable mission apps.
type Catalog struct {
	// APIVersion is the catalog schema version, e.g. "sre/v1".
	APIVersion string `yaml:"apiVersion"`
	// Apps lists the mission-app entries.
	Apps []Entry `yaml:"apps"`
}

// Load reads, parses, and validates catalog.yaml at path. A missing or invalid
// catalog is an operator error (clear message, non-zero exit), not a build error.
func Load(path string) (*Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("appcatalog: read %s: %w", path, err)
	}
	var c Catalog
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("appcatalog: parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Find returns the entry with the given name and whether it was found.
func (c *Catalog) Find(name string) (Entry, bool) {
	for _, e := range c.Apps {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}

// Validate checks the catalog for structural problems, returning a combined error
// describing every issue found (or nil when the catalog is coherent).
func (c *Catalog) Validate() error {
	var errs []string
	if c.APIVersion == "" {
		errs = append(errs, "apiVersion must be set")
	}
	if len(c.Apps) == 0 {
		errs = append(errs, "catalog has no apps")
	}
	seen := map[string]bool{}
	for i, e := range c.Apps {
		where := fmt.Sprintf("apps[%d]", i)
		if e.Name == "" {
			errs = append(errs, where+": name must not be empty")
		} else if seen[e.Name] {
			errs = append(errs, fmt.Sprintf("%s: duplicate app name %q", where, e.Name))
		}
		seen[e.Name] = true
		switch e.Source.Type {
		case SourceLocal, SourceOCI, SourceGitHub:
		default:
			errs = append(errs, fmt.Sprintf("%s: source.type %q is not one of local|oci|github", where, e.Source.Type))
		}
		if e.Source.Ref == "" {
			errs = append(errs, where+": source.ref must not be empty")
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid catalog: %v", errs)
	}
	return nil
}

// writeFile writes s to path with 0o644 permissions. Shared by tests and the
// state writer; kept here so the package has one small file helper.
func writeFile(path, s string) error {
	return os.WriteFile(path, []byte(s), 0o644)
}

// reValid reports whether expr compiles as a regexp (used by verify input checks).
func reValid(expr string) bool {
	_, err := regexp.Compile(expr)
	return err == nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -run 'TestLoad|TestValidate' -v`
Expected: PASS (3 tests). `go vet ./internal/appcatalog/` clean. (`reValid` is referenced by Task 4; if `go vet` flags it unused in this commit, inline it into Task 4 instead — but it is exercised by Task 4's tests in the same package, so keep it.)

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/catalog.go installer/internal/appcatalog/catalog_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): catalog model + validating loader"
```

---

### Task 2: Source `Adapter` interface + `local` adapter

The `Adapter` resolves a catalog `Entry` to a deployable, verifiable `(ref, digest)`. The `local` adapter handles a directory or a `*.tar.zst` on disk — pure filesystem work, no cluster, airgap-safe (spec §4). For a directory it returns the path as the ref with an empty digest (a dir is not content-addressable); for a tarball it returns the path plus a `sha256:…` digest of the file bytes.

**Files:**
- Create: `installer/internal/appcatalog/source/source.go`
- Create: `installer/internal/appcatalog/source/local.go`
- Test: `installer/internal/appcatalog/source/local_test.go`

**Interfaces:**
- Consumes: `appcatalog.Entry`, `appcatalog.Source` (Task 1).
- Produces:
  - `type Adapter interface { Resolve(e appcatalog.Entry) (ref string, digest string, err error) }`.
  - `type Local struct{}` implementing `Adapter` for `Source.Type == SourceLocal`.
  - `func (Local) Resolve(e appcatalog.Entry) (string, string, error)`.
  - `func sha256File(path string) (string, error)` — returns `"sha256:"+hex` (reused by tests).

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/source/ -v`
Expected: FAIL — `undefined: Local` (package doesn't compile).

- [ ] **Step 3: Write minimal implementation**

`source.go`:

```go
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
```

`local.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/source/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/source/source.go installer/internal/appcatalog/source/local.go installer/internal/appcatalog/source/local_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): source Adapter interface + local adapter"
```

---

### Task 3: `oci` adapter (tag → digest) + the `zarf` exec wrapper

The `oci` adapter resolves a registry ref (a tag) to a pinned `ref@sha256:…` digest by shelling out to `zarf package inspect` (which prints the package's OCI manifest/metadata). This task introduces the **first exec wrapper** and its dual-testing pattern: a `Zarf` interface, a real impl whose command assembly is unit-tested via an injected `commandContext`, and a fake used by the adapter test. No real `zarf` binary runs in tests.

**Files:**
- Create: `installer/internal/appcatalog/source/exec.go` (the `Zarf` interface + real impl + injectable `commandContext`)
- Create: `installer/internal/appcatalog/source/oci.go`
- Test: `installer/internal/appcatalog/source/oci_test.go`
- Test: `installer/internal/appcatalog/source/exec_test.go`

**Interfaces:**
- Consumes: `appcatalog.Entry`, `Adapter` (Tasks 1–2).
- Produces:
  - `type Zarf interface { Inspect(ref string) ([]byte, error) }`.
  - `type execZarf struct{}` (real impl) + `func NewZarf() Zarf`.
  - `var commandContext = exec.Command` (package var, swapped in tests).
  - `type OCI struct{ Zarf Zarf }` implementing `Adapter`.
  - `func (o OCI) Resolve(e appcatalog.Entry) (string, string, error)` — returns `(e.Source.Ref+"@"+digest, digest, nil)`.
  - `func parseDigest(inspectOutput []byte) (string, error)` — extracts `sha256:…` (pure, testable).

- [ ] **Step 1: Write the failing test**

`oci_test.go`:

```go
package source

import (
	"errors"
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// fakeZarf is a hand-written test double for the Zarf wrapper.
type fakeZarf struct {
	out      []byte
	err      error
	gotRef   string
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
```

`exec_test.go` (proves the real impl assembles the right argv without running `zarf`):

```go
package source

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecZarf_BuildsInspectCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		// Return a harmless command so .Output() doesn't hit a real binary.
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	if _, err := (execZarf{}).Inspect("ghcr.io/x/cosmos"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if gotName != "zarf" {
		t.Errorf("binary = %q, want zarf", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "package inspect") || !strings.Contains(joined, "ghcr.io/x/cosmos") {
		t.Errorf("args = %v, want a `package inspect <ref>` invocation", gotArgs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/source/ -run 'OCI|ParseDigest|ExecZarf' -v`
Expected: FAIL — `undefined: OCI`, `undefined: execZarf`, `undefined: commandContext`, `undefined: parseDigest`.

- [ ] **Step 3: Write minimal implementation**

`exec.go`:

```go
package source

import (
	"fmt"
	"os/exec"
)

// commandContext builds external commands. It is a package var so tests can swap
// it for a fake that records argv and returns a harmless command — letting us
// unit-test command assembly without running the real binary.
var commandContext = exec.Command

// Zarf is the slice of the `zarf` CLI this package orchestrates. We shell out to
// zarf — we never reimplement it (spec §3). Tests use a fake Zarf.
type Zarf interface {
	// Inspect returns `zarf package inspect <ref>` output (manifest + metadata).
	Inspect(ref string) ([]byte, error)
}

// execZarf is the real Zarf wrapper.
type execZarf struct{}

// NewZarf returns the production Zarf wrapper.
func NewZarf() Zarf { return execZarf{} }

// Inspect runs `zarf package inspect <ref>` and returns its stdout.
func (execZarf) Inspect(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "package", "inspect", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf package inspect %s: %w", ref, err)
	}
	return out, nil
}
```

`oci.go`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/source/ -v`
Expected: PASS (all source tests: local + oci + exec).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/source/exec.go installer/internal/appcatalog/source/oci.go installer/internal/appcatalog/source/oci_test.go installer/internal/appcatalog/source/exec_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): oci adapter (tag->digest) + zarf exec wrapper"
```

---

### Task 4: `verify` — cosign wrapper, fail-closed

`Verify` checks the resolved package's signature against the entry's expected signer identity (`cosign verify <ref> --certificate-identity-regexp … --certificate-oidc-issuer …`). **Fail-closed** (spec §5.2, §8, §10): any non-nil result from cosign, plus an empty/invalid `identityRegexp` or empty `issuer`, aborts — naming the expected identity. Introduces the `Cosign` exec wrapper (same dual-testing pattern as Task 3).

**Files:**
- Create: `installer/internal/appcatalog/verify.go` (the `Cosign` interface + real impl + `Verify` func)
- Test: `installer/internal/appcatalog/verify_test.go`

**Interfaces:**
- Consumes: `appcatalog.Entry`, `appcatalog.Verify` (Task 1); `reValid` (Task 1).
- Produces:
  - `type Cosign interface { Verify(ref, identityRegexp, issuer string) error }`.
  - `type execCosign struct{}` + `func NewCosign() Cosign`.
  - `var commandContext = exec.Command` (package-local var in `appcatalog`, distinct from the `source` package's; used by this + later tasks' real impls).
  - `func Verify(c Cosign, e appcatalog.Entry, ref string) error` — fail-closed gate.

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeCosign is a hand-written test double for the Cosign wrapper.
type fakeCosign struct {
	err       error
	gotRef    string
	gotIDRe   string
	gotIssuer string
}

func (f *fakeCosign) Verify(ref, idRe, issuer string) error {
	f.gotRef, f.gotIDRe, f.gotIssuer = ref, idRe, issuer
	return f.err
}

func entryWithVerify() Entry {
	return Entry{
		Name: "cosmos",
		Verify: Verify{
			IdentityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/",
			Issuer:         "https://token.actions.githubusercontent.com",
		},
	}
}

func TestVerify_PassesThroughToCosign(t *testing.T) {
	fc := &fakeCosign{}
	if err := Verify(fc, entryWithVerify(), "ghcr.io/x/cosmos@sha256:abc"); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if fc.gotRef != "ghcr.io/x/cosmos@sha256:abc" {
		t.Errorf("ref = %q, want the pinned ref", fc.gotRef)
	}
	if fc.gotIDRe != entryWithVerify().Verify.IdentityRegexp || fc.gotIssuer != entryWithVerify().Verify.Issuer {
		t.Errorf("identity/issuer not forwarded: %+v", fc)
	}
}

func TestVerify_FailClosedOnBadSignature(t *testing.T) {
	err := Verify(&fakeCosign{err: errors.New("no matching signatures")}, entryWithVerify(), "ref@sha256:abc")
	if err == nil {
		t.Fatal("Verify must fail closed when cosign errors")
	}
	if !strings.Contains(err.Error(), entryWithVerify().Verify.IdentityRegexp) {
		t.Errorf("error should name the expected identity, got: %v", err)
	}
}

func TestVerify_FailClosedOnMissingPolicy(t *testing.T) {
	e := entryWithVerify()
	e.Verify.IdentityRegexp = "" // no expected identity configured
	if err := Verify(&fakeCosign{}, e, "ref@sha256:abc"); err == nil {
		t.Error("Verify must refuse to run with no expected signer identity (fail-closed)")
	}
	e2 := entryWithVerify()
	e2.Verify.IdentityRegexp = "([" // invalid regexp
	if err := Verify(&fakeCosign{}, e2, "ref@sha256:abc"); err == nil {
		t.Error("Verify must refuse an invalid identity regexp (fail-closed)")
	}
}

func TestExecCosign_BuildsVerifyCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	_ = (execCosign{}).Verify("ref@sha256:abc", "^https://github.com/JongoDB-Labs/", "https://token.actions.githubusercontent.com")
	if gotName != "cosign" {
		t.Errorf("binary = %q, want cosign", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"verify", "ref@sha256:abc", "--certificate-identity-regexp", "--certificate-oidc-issuer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, gotArgs)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run 'Verify|ExecCosign' -v`
Expected: FAIL — `undefined: Verify`, `undefined: execCosign`, `undefined: commandContext` (the `appcatalog` package's exec var doesn't exist yet).

- [ ] **Step 3: Write minimal implementation**

`verify.go`:

```go
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

// Verify is the fail-closed signature gate (spec §5.2, §8). It refuses to proceed
// unless the entry declares a valid expected signer identity, then delegates to
// cosign. ANY error — bad policy or a failed/absent signature — aborts the deploy,
// naming the expected identity so the operator knows what was required.
func Verify(c Cosign, e Entry, ref string) error {
	if e.Verify.IdentityRegexp == "" || e.Verify.Issuer == "" {
		return fmt.Errorf("verify: %s has no expected signer identity/issuer configured; refusing to deploy unverified (fail-closed)", e.Name)
	}
	if !reValid(e.Verify.IdentityRegexp) {
		return fmt.Errorf("verify: %s identityRegexp %q is not a valid regexp; refusing to deploy (fail-closed)", e.Name, e.Verify.IdentityRegexp)
	}
	if err := c.Verify(ref, e.Verify.IdentityRegexp, e.Verify.Issuer); err != nil {
		return fmt.Errorf("verify: signature check failed for %s (expected signer identity %q, issuer %q): %w", e.Name, e.Verify.IdentityRegexp, e.Verify.Issuer, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -v`
Expected: PASS (Task 1 + Task 4 tests). `reValid` is now exercised — `go vet ./internal/appcatalog/` clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/verify.go installer/internal/appcatalog/verify_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): fail-closed cosign verify gate + cosign wrapper"
```

---

### Task 5: `preflight` — advisory cohesion / requires scan

`Preflight` is **advisory** (spec §5.3, §10): it inspects the resolved package (`zarf package inspect`, reusing the `source.Zarf` wrapper) and returns warnings — never an error that blocks deploy. It warns when no UDS `Package` CR is present (the app won't auto-wire cohesion) and when a `requires` service is absent from the live cluster. The authoritative cohesion check is the post-deploy confirm (Task 8 wiring).

**Files:**
- Create: `installer/internal/appcatalog/preflight.go`
- Test: `installer/internal/appcatalog/preflight_test.go`

**Interfaces:**
- Consumes: `appcatalog.Entry` (Task 1); `source.Zarf` interface (Task 3, the `Inspect` method).
- Produces:
  - `type Warning struct { Code string; Message string }`.
  - `type Inspector interface { Inspect(ref string) ([]byte, error) }` (matches `source.Zarf`, declared locally so `appcatalog` need not import `source`).
  - `func Preflight(z Inspector, e Entry, ref string, installedRequires map[string]bool) ([]Warning, error)` — `error` only for an inspect I/O failure; cohesion/requires gaps are `Warning`s.
  - `func hasPackageCR(inspectOutput []byte) bool` (pure, testable).

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"errors"
	"testing"
)

// fakeInspector is a hand-written double for the inspect dependency.
type fakeInspector struct {
	out []byte
	err error
}

func (f *fakeInspector) Inspect(string) ([]byte, error) { return f.out, f.err }

const manifestWithCR = `kind: ZarfPackageConfig
components:
  - name: cosmos
    manifests:
      - name: cosmos-uds-package
        # ...
kind: Package
apiVersion: uds.dev/v1alpha1
`

const manifestNoCR = `kind: ZarfPackageConfig
components:
  - name: cosmos
    charts:
      - name: cosmos
`

func TestPreflight_WarnsOnMissingPackageCR(t *testing.T) {
	warns, err := Preflight(&fakeInspector{out: []byte(manifestNoCR)},
		Entry{Name: "cosmos"}, "ref", map[string]bool{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !hasWarning(warns, "no-package-cr") {
		t.Errorf("expected a no-package-cr warning, got %+v", warns)
	}
}

func TestPreflight_WarnsOnMissingRequire(t *testing.T) {
	warns, err := Preflight(&fakeInspector{out: []byte(manifestWithCR)},
		Entry{Name: "cosmos", Requires: []string{"postgres"}}, "ref",
		map[string]bool{}) // postgres NOT installed
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !hasWarning(warns, "missing-require") {
		t.Errorf("expected a missing-require warning, got %+v", warns)
	}
}

func TestPreflight_CleanWhenCohesionPresent(t *testing.T) {
	warns, err := Preflight(&fakeInspector{out: []byte(manifestWithCR)},
		Entry{Name: "cosmos", Requires: []string{"postgres"}}, "ref",
		map[string]bool{"postgres": true})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %+v", warns)
	}
}

func TestPreflight_InspectErrorIsAdvisoryButReturned(t *testing.T) {
	_, err := Preflight(&fakeInspector{err: errors.New("cannot read package")},
		Entry{Name: "cosmos"}, "ref", map[string]bool{})
	if err == nil {
		t.Error("an inspect I/O failure should surface as an error to the caller")
	}
}

// hasWarning is a small test helper.
func hasWarning(ws []Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run TestPreflight -v`
Expected: FAIL — `undefined: Preflight`, `undefined: Warning`.

- [ ] **Step 3: Write minimal implementation**

`preflight.go`:

```go
package appcatalog

import (
	"fmt"
	"regexp"
)

// packageCRRe detects a UDS Package CR in zarf inspect output. Its presence means
// the UDS Operator will auto-wire cohesion (expose/sso/allow) on deploy; absence
// means the app will deploy but not self-wire — worth a warning, not a block.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -run TestPreflight -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/preflight.go installer/internal/appcatalog/preflight_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): advisory cohesion/requires preflight"
```

---

### Task 6: `deploy` — `uds deploy` / `uds remove` orchestration

`Deploy` shells out to `uds deploy <ref> --confirm`; on failure it best-effort `uds remove`s so a half-wired app isn't left behind (spec §5, §10). `Remove` wraps `uds remove <name> --confirm`. Introduces the `UDS` exec wrapper.

**Files:**
- Create: `installer/internal/appcatalog/deploy.go`
- Test: `installer/internal/appcatalog/deploy_test.go`

**Interfaces:**
- Consumes: `commandContext` (Task 4, same package var).
- Produces:
  - `type UDS interface { Deploy(ref string) error; Remove(name string) error }`.
  - `type execUDS struct{}` + `func NewUDS() UDS`.
  - `func Deploy(u UDS, ref string) error` — on deploy failure, attempts `Remove` (best-effort) and returns the original error.
  - `func Remove(u UDS, name string) error`.

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeUDS is a hand-written double for the UDS wrapper.
type fakeUDS struct {
	deployErr  error
	removeErr  error
	deployed   string
	removed    string
	removeCount int
}

func (f *fakeUDS) Deploy(ref string) error  { f.deployed = ref; return f.deployErr }
func (f *fakeUDS) Remove(name string) error { f.removed = name; f.removeCount++; return f.removeErr }

func TestDeploy_HappyPath(t *testing.T) {
	fu := &fakeUDS{}
	if err := Deploy(fu, "ghcr.io/x/cosmos@sha256:abc"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if fu.deployed != "ghcr.io/x/cosmos@sha256:abc" {
		t.Errorf("deployed ref = %q", fu.deployed)
	}
	if fu.removeCount != 0 {
		t.Error("a successful deploy must not trigger a rollback remove")
	}
}

func TestDeploy_RollsBackOnFailure(t *testing.T) {
	fu := &fakeUDS{deployErr: errors.New("reconcile timeout")}
	err := Deploy(fu, "ghcr.io/x/cosmos@sha256:abc")
	if err == nil {
		t.Fatal("Deploy should return the deploy error")
	}
	if fu.removeCount != 1 {
		t.Errorf("a failed deploy should best-effort remove once, got %d", fu.removeCount)
	}
	if !strings.Contains(err.Error(), "reconcile timeout") {
		t.Errorf("error should wrap the deploy failure, got %v", err)
	}
}

func TestRemove_Wraps(t *testing.T) {
	fu := &fakeUDS{}
	if err := Remove(fu, "cosmos"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if fu.removed != "cosmos" {
		t.Errorf("removed = %q, want cosmos", fu.removed)
	}
}

func TestExecUDS_BuildsCommands(t *testing.T) {
	var calls [][]string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	_ = (execUDS{}).Deploy("ref@sha256:abc")
	_ = (execUDS{}).Remove("cosmos")
	if len(calls) != 2 {
		t.Fatalf("want 2 commands, got %d", len(calls))
	}
	if strings.Join(calls[0], " ") != "uds deploy ref@sha256:abc --confirm" {
		t.Errorf("deploy argv = %v", calls[0])
	}
	if strings.Join(calls[1], " ") != "uds remove cosmos --confirm" {
		t.Errorf("remove argv = %v", calls[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run 'Deploy|Remove|ExecUDS' -v`
Expected: FAIL — `undefined: Deploy`, `undefined: Remove`, `undefined: execUDS`.

- [ ] **Step 3: Write minimal implementation**

`deploy.go`:

```go
package appcatalog

import (
	"fmt"
	"os/exec"
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

// ensure the package always imports os/exec via commandContext's type even if a
// future refactor drops other uses (keeps the var's type obvious to readers).
var _ = exec.Command
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -run 'Deploy|Remove|ExecUDS' -v`
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/deploy.go installer/internal/appcatalog/deploy_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): uds deploy/remove with best-effort rollback"
```

---

### Task 7: `state` — the `sre-appcatalog-installs` ConfigMap record

`state` reads and writes the canonical install record: a ConfigMap `sre-appcatalog-installs` in `sre-system` (which `srectl` ensures exists), each app a key whose value is a small YAML blob (spec §6). It also lists the live UDS Packages so `list --installed` can show drift. Introduces the `Kube` exec wrapper.

**Files:**
- Create: `installer/internal/appcatalog/state.go`
- Test: `installer/internal/appcatalog/state_test.go`

**Interfaces:**
- Consumes: `commandContext` (Task 4); `writeFile` (Task 1, for the apply-via-temp-file path).
- Produces:
  - `const SystemNamespace = "sre-system"`, `const InstallsConfigMap = "sre-appcatalog-installs"`.
  - `type Record struct { Version, Source, Digest, InstalledAt, InstalledBy string }` (yaml tags `version`,`source`,`digest`,`installedAt`,`installedBy`).
  - `type Kube interface { EnsureNamespace(ns string) error; GetConfigMap(ns, name string) ([]byte, error); ApplyConfigMap(ns, name string, data map[string]string) error; ListPackages() ([]byte, error) }`.
  - `type execKube struct{}` + `func NewKube() Kube`.
  - `type State struct { Kube Kube }`.
  - `func (s State) Load() (map[string]Record, error)` — empty map when the ConfigMap is absent.
  - `func (s State) Put(name string, r Record) error` — read-modify-write the ConfigMap (ensures ns first).
  - `func (s State) Delete(name string) error` — prune a key.
  - `func (s State) InstalledPackages() (map[string]bool, error)` — names from `kubectl get packages -A`.
  - `func marshalRecords(map[string]Record) (map[string]string, error)` / `func unmarshalRecords([]byte) (map[string]Record, error)` (pure, testable).

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"errors"
	"testing"
)

// fakeKube is a hand-written, in-memory double for the Kube wrapper.
type fakeKube struct {
	cm           map[string]string // current ConfigMap data, nil ⇒ not found
	cmMissing    bool
	nsEnsured    string
	applied      map[string]string
	packagesJSON []byte
	listErr      error
}

func (f *fakeKube) EnsureNamespace(ns string) error { f.nsEnsured = ns; return nil }

func (f *fakeKube) GetConfigMap(ns, name string) ([]byte, error) {
	if f.cmMissing {
		return nil, errMissingConfigMap
	}
	return marshalConfigMapForTest(f.cm), nil
}

func (f *fakeKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	f.applied = data
	f.cm = data
	f.cmMissing = false
	return nil
}

func (f *fakeKube) ListPackages() ([]byte, error) { return f.packagesJSON, f.listErr }

func TestState_PutThenLoad(t *testing.T) {
	fk := &fakeKube{cmMissing: true}
	s := State{Kube: fk}
	rec := Record{Version: "2.102.0", Source: "oci:ghcr.io/x/cosmos", Digest: "sha256:abc", InstalledAt: "2026-06-26T00:00:00Z", InstalledBy: "maggie"}
	if err := s.Put("cosmos", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if fk.nsEnsured != SystemNamespace {
		t.Errorf("Put should ensure %q, ensured %q", SystemNamespace, fk.nsEnsured)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["cosmos"].Version != "2.102.0" || got["cosmos"].Digest != "sha256:abc" {
		t.Errorf("round-trip mismatch: %+v", got["cosmos"])
	}
}

func TestState_LoadEmptyWhenAbsent(t *testing.T) {
	got, err := State{Kube: &fakeKube{cmMissing: true}}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("absent ConfigMap should load as empty, got %+v", got)
	}
}

func TestState_Delete(t *testing.T) {
	fk := &fakeKube{}
	s := State{Kube: fk}
	_ = s.Put("cosmos", Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"})
	_ = s.Put("other", Record{Version: "1.0.0", Source: "oci:y", Digest: "sha256:def"})
	if err := s.Delete("cosmos"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Load()
	if _, ok := got["cosmos"]; ok {
		t.Error("cosmos should be gone after Delete")
	}
	if _, ok := got["other"]; !ok {
		t.Error("Delete must not drop other records")
	}
}

func TestState_InstalledPackages(t *testing.T) {
	fk := &fakeKube{packagesJSON: []byte(`{"items":[{"metadata":{"name":"cosmos"}},{"metadata":{"name":"keycloak"}}]}`)}
	got, err := State{Kube: fk}.InstalledPackages()
	if err != nil {
		t.Fatalf("InstalledPackages: %v", err)
	}
	if !got["cosmos"] || !got["keycloak"] {
		t.Errorf("expected cosmos+keycloak, got %+v", got)
	}
}

func TestState_InstalledPackagesError(t *testing.T) {
	_, err := State{Kube: &fakeKube{listErr: errors.New("no cluster")}}.InstalledPackages()
	if err == nil {
		t.Error("InstalledPackages should surface a list error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run TestState -v`
Expected: FAIL — `undefined: State`, `undefined: Record`, `undefined: errMissingConfigMap`, `undefined: marshalConfigMapForTest`.

- [ ] **Step 3: Write minimal implementation**

`state.go`:

```go
package appcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// SystemNamespace is the substrate system namespace srectl ensures exists.
	SystemNamespace = "sre-system"
	// InstallsConfigMap is the canonical install-record ConfigMap name (spec §6).
	InstallsConfigMap = "sre-appcatalog-installs"
)

// errMissingConfigMap signals the install-record ConfigMap does not exist yet
// (first install). Load treats it as "no records", not an error.
var errMissingConfigMap = errors.New("install-record ConfigMap not found")

// Record is one app's install metadata. The ConfigMap is convenience metadata;
// the live cluster is the source of truth (spec §6).
type Record struct {
	// Version is the installed app version.
	Version string `yaml:"version"`
	// Source is the resolved source, e.g. "oci:ghcr.io/jongodb-labs/bundles/cosmos".
	Source string `yaml:"source"`
	// Digest is the deployed package's sha256 digest.
	Digest string `yaml:"digest"`
	// InstalledAt is the RFC3339 install timestamp.
	InstalledAt string `yaml:"installedAt"`
	// InstalledBy is the actor (OIDC sub, or kubeconfig user for the CLI).
	InstalledBy string `yaml:"installedBy"`
}

// Kube is the slice of `kubectl` this package orchestrates for state. We shell
// out to kubectl — never reimplement a Kubernetes client (keeps the binary slim
// and matches the orchestrate-don't-reimplement rule).
type Kube interface {
	EnsureNamespace(ns string) error
	GetConfigMap(ns, name string) ([]byte, error)
	ApplyConfigMap(ns, name string, data map[string]string) error
	ListPackages() ([]byte, error)
}

// State reads/writes the install-record ConfigMap and lists live UDS Packages.
type State struct {
	// Kube orchestrates kubectl; tests inject a fake.
	Kube Kube
}

// Load returns the install records, or an empty map when the ConfigMap is absent.
func (s State) Load() (map[string]Record, error) {
	raw, err := s.Kube.GetConfigMap(SystemNamespace, InstallsConfigMap)
	if errors.Is(err, errMissingConfigMap) {
		return map[string]Record{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("state: read %s/%s: %w", SystemNamespace, InstallsConfigMap, err)
	}
	return unmarshalRecords(raw)
}

// Put records (or replaces) an app's install metadata, ensuring the namespace and
// merging into any existing records (read-modify-write).
func (s State) Put(name string, r Record) error {
	if err := s.Kube.EnsureNamespace(SystemNamespace); err != nil {
		return fmt.Errorf("state: ensure namespace: %w", err)
	}
	recs, err := s.Load()
	if err != nil {
		return err
	}
	recs[name] = r
	return s.apply(recs)
}

// Delete prunes an app's record (no-op if absent).
func (s State) Delete(name string) error {
	recs, err := s.Load()
	if err != nil {
		return err
	}
	delete(recs, name)
	return s.apply(recs)
}

// apply marshals the records and writes them back as the ConfigMap data.
func (s State) apply(recs map[string]Record) error {
	data, err := marshalRecords(recs)
	if err != nil {
		return err
	}
	if err := s.Kube.ApplyConfigMap(SystemNamespace, InstallsConfigMap, data); err != nil {
		return fmt.Errorf("state: apply %s: %w", InstallsConfigMap, err)
	}
	return nil
}

// InstalledPackages returns the set of live UDS Package names (cluster truth),
// parsed from `kubectl get packages -A -o json`.
func (s State) InstalledPackages() (map[string]bool, error) {
	raw, err := s.Kube.ListPackages()
	if err != nil {
		return nil, fmt.Errorf("state: list packages: %w", err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("state: parse packages json: %w", err)
	}
	out := make(map[string]bool, len(list.Items))
	for _, it := range list.Items {
		out[it.Metadata.Name] = true
	}
	return out, nil
}

// marshalRecords renders each record to a YAML string keyed by app name (the
// ConfigMap data shape: map[appName]yaml-blob).
func marshalRecords(recs map[string]Record) (map[string]string, error) {
	data := make(map[string]string, len(recs))
	for name, r := range recs {
		b, err := yaml.Marshal(r)
		if err != nil {
			return nil, fmt.Errorf("state: marshal record %q: %w", name, err)
		}
		data[name] = string(b)
	}
	return data, nil
}

// unmarshalRecords parses ConfigMap data (map[appName]yaml-blob) back to records.
func unmarshalRecords(cmData []byte) (map[string]Record, error) {
	// cmData is the ConfigMap's `.data` as YAML: a map of app name → record blob.
	var data map[string]string
	if err := yaml.Unmarshal(cmData, &data); err != nil {
		return nil, fmt.Errorf("state: parse configmap data: %w", err)
	}
	out := make(map[string]Record, len(data))
	for name, blob := range data {
		var r Record
		if err := yaml.Unmarshal([]byte(blob), &r); err != nil {
			return nil, fmt.Errorf("state: parse record %q: %w", name, err)
		}
		out[name] = r
	}
	return out, nil
}

// execKube is the real Kube wrapper.
type execKube struct{}

// NewKube returns the production Kube wrapper.
func NewKube() Kube { return execKube{} }

// EnsureNamespace creates the namespace if absent (idempotent via apply).
func (execKube) EnsureNamespace(ns string) error {
	manifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", ns)
	return kubectlApplyStdin(manifest)
}

// GetConfigMap returns the ConfigMap's `.data` as YAML, or errMissingConfigMap.
func (execKube) GetConfigMap(ns, name string) ([]byte, error) {
	out, err := commandContext("kubectl", "get", "configmap", name, "-n", ns, "-o", "jsonpath={.data}").Output()
	if err != nil {
		// kubectl exits non-zero when the object is absent; treat as missing.
		return nil, errMissingConfigMap
	}
	return out, nil
}

// ApplyConfigMap server-side-applies the ConfigMap with the given data via a
// temp manifest file (kubectl apply -f), so the record is upserted idempotently.
func (execKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n", name, ns)
	for k, v := range data {
		// Indent each record blob as a YAML block scalar under its key.
		fmt.Fprintf(&b, "  %s: |\n", k)
		for _, line := range strings.Split(strings.TrimRight(v, "\n"), "\n") {
			fmt.Fprintf(&b, "    %s\n", line)
		}
	}
	tmp := filepath.Join(os.TempDir(), "sre-appcatalog-installs.yaml")
	if err := writeFile(tmp, b.String()); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := commandContext("kubectl", "apply", "-f", tmp).CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply configmap: %w: %s", err, out)
	}
	return nil
}

// ListPackages returns `kubectl get packages -A -o json`.
func (execKube) ListPackages() ([]byte, error) {
	out, err := commandContext("kubectl", "get", "packages", "-A", "-o", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get packages: %w", err)
	}
	return out, nil
}

// kubectlApplyStdin pipes a manifest to `kubectl apply -f -`.
func kubectlApplyStdin(manifest string) error {
	cmd := commandContext("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply -f -: %w: %s", err, out)
	}
	return nil
}
```

Add the test-only helper to `state_test.go` (it mirrors how the fake serializes the ConfigMap data the same way `unmarshalRecords` expects):

```go
// marshalConfigMapForTest renders the fake's in-memory ConfigMap data (map of
// app name → record YAML blob) into the YAML the production GetConfigMap returns.
func marshalConfigMapForTest(data map[string]string) []byte {
	if data == nil {
		return []byte("{}")
	}
	b, err := yaml.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}
```

…and add `import "gopkg.in/yaml.v3"` to `state_test.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -run TestState -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/internal/appcatalog/state.go installer/internal/appcatalog/state_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): install-record ConfigMap state + kubectl wrapper"
```

---

### Task 8: `cmd/srectl/app.go` — `app {list,install,remove,status}`

The cobra surface. A parent `app` command with four subcommands wiring Tasks 1–7: `list [--installed]`, `install <name>`, `remove <name>`, `status <name>`. It mirrors `cmd/srectl/install.go`: a thin command builder + a `run…` function taking an `io.Writer`, registered in `main.go`. Deploy logic stays in `internal/appcatalog`; this file only orchestrates and prints.

To keep the command testable without a cluster, `runInstall`/etc. take the already-constructed dependencies (`source.Adapter` chosen by source type, `Cosign`, `UDS`, `Inspector`, `State`) — a small `deps` struct built by a `newDeps()` helper that uses the real wrappers in production. Tests call the `run…` funcs with fakes.

**Files:**
- Create: `installer/cmd/srectl/app.go`
- Modify: `installer/cmd/srectl/main.go:30-34` (add `newAppCmd()` to `root.AddCommand(...)`)
- Test: `installer/cmd/srectl/app_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–7 — `appcatalog.{Load,Catalog,Entry,Verify,Deploy,Remove,Preflight,State,Record,Cosign,UDS,Inspector,NewCosign,NewUDS,NewKube,SystemNamespace}`, `source.{Adapter,Local,OCI,NewZarf}`.
- Produces (within `package main`):
  - `func newAppCmd() *cobra.Command`.
  - `type appDeps struct { Cat *appcatalog.Catalog; Cosign appcatalog.Cosign; UDS appcatalog.UDS; Inspect appcatalog.Inspector; State appcatalog.State; Zarf source.Zarf; Now func() string; Actor string }`.
  - `func adapterFor(e appcatalog.Entry, z source.Zarf) (source.Adapter, error)` — `Local{}` / `OCI{Zarf:z}`; `github` → "deferred" error.
  - `func runAppList(out io.Writer, d appDeps, installedOnly bool) error`.
  - `func runAppInstall(out io.Writer, d appDeps, name string) error`.
  - `func runAppRemove(out io.Writer, d appDeps, name string) error`.
  - `func runAppStatus(out io.Writer, d appDeps, name string) error`.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

// --- fakes (mirror the package-level interfaces) ---

type fakeCosign struct{ err error }

func (f fakeCosign) Verify(ref, idRe, issuer string) error { return f.err }

type fakeUDS struct {
	deployErr error
	deployed  string
	removed   string
}

func (f *fakeUDS) Deploy(ref string) error  { f.deployed = ref; return f.deployErr }
func (f *fakeUDS) Remove(name string) error { f.removed = name; return nil }

type fakeInspect struct{ out []byte }

func (f fakeInspect) Inspect(string) ([]byte, error) { return f.out, nil }

type fakeKube struct {
	cm           map[string]string
	packagesJSON []byte
}

func (f *fakeKube) EnsureNamespace(string) error { return nil }
func (f *fakeKube) GetConfigMap(ns, name string) ([]byte, error) {
	if f.cm == nil {
		// emulate "absent" via the sentinel the State.Load path expects
		return nil, errCMMissingForTest
	}
	return marshalCMForTest(f.cm), nil
}
func (f *fakeKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	f.cm = data
	return nil
}
func (f *fakeKube) ListPackages() ([]byte, error) { return f.packagesJSON, nil }

func testCatalog() *appcatalog.Catalog {
	return &appcatalog.Catalog{
		APIVersion: "sre/v1",
		Apps: []appcatalog.Entry{{
			Name:    "cosmos",
			Version: "2.102.0",
			Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/cosmos"},
			Verify:  appcatalog.Verify{IdentityRegexp: "^https://github.com/JongoDB-Labs/", Issuer: "https://token.actions.githubusercontent.com"},
		}},
	}
}

const inspectWithCRAndDigest = "kind: Package\nmanifestDigest: sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

func testDeps(kube *fakeKube, uds *fakeUDS) appDeps {
	z := fakeInspect{out: []byte(inspectWithCRAndDigest)}
	return appDeps{
		Cat:     testCatalog(),
		Cosign:  fakeCosign{},
		UDS:     uds,
		Inspect: z,
		State:   appcatalog.State{Kube: kube},
		Zarf:    z,
		Now:     func() string { return "2026-06-26T00:00:00Z" },
		Actor:   "tester",
	}
}

func TestRunAppList_ShowsCatalog(t *testing.T) {
	var out bytes.Buffer
	if err := runAppList(&out, testDeps(&fakeKube{}, &fakeUDS{}), false); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "cosmos") || !strings.Contains(out.String(), "2.102.0") {
		t.Errorf("list output missing cosmos/version:\n%s", out.String())
	}
}

func TestRunAppInstall_HappyPathDeploysAndRecords(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	var out bytes.Buffer
	if err := runAppInstall(&out, testDeps(kube, uds), "cosmos"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(uds.deployed, "ghcr.io/x/cosmos@sha256:") {
		t.Errorf("expected a digest-pinned deploy, got %q", uds.deployed)
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if recs["cosmos"].Version != "2.102.0" {
		t.Errorf("install should write the record, got %+v", recs["cosmos"])
	}
}

func TestRunAppInstall_FailClosedDoesNotDeploy(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	d := testDeps(kube, uds)
	d.Cosign = fakeCosign{err: errSigForTest} // signature fails
	var out bytes.Buffer
	if err := runAppInstall(&out, d, "cosmos"); err == nil {
		t.Fatal("install must abort when verify fails (fail-closed)")
	}
	if uds.deployed != "" {
		t.Error("a fail-closed verify must NOT reach uds deploy")
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if _, ok := recs["cosmos"]; ok {
		t.Error("no record should be written on a failed install")
	}
}

func TestRunAppInstall_UnknownApp(t *testing.T) {
	if err := runAppInstall(&bytes.Buffer{}, testDeps(&fakeKube{}, &fakeUDS{}), "ghost"); err == nil {
		t.Error("install of an unknown app should error")
	}
}

func TestRunAppRemove_RemovesAndPrunes(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	_ = (appcatalog.State{Kube: kube}).Put("cosmos", appcatalog.Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"})
	var out bytes.Buffer
	if err := runAppRemove(&out, testDeps(kube, uds), "cosmos"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if uds.removed != "cosmos" {
		t.Errorf("remove should uds-remove cosmos, got %q", uds.removed)
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if _, ok := recs["cosmos"]; ok {
		t.Error("remove should prune the record")
	}
}

func TestRunAppStatus_ReportsRecordAndLive(t *testing.T) {
	kube := &fakeKube{packagesJSON: []byte(`{"items":[{"metadata":{"name":"cosmos"}}]}`)}
	_ = (appcatalog.State{Kube: kube}).Put("cosmos", appcatalog.Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"})
	var out bytes.Buffer
	if err := runAppStatus(&out, testDeps(kube, &fakeUDS{}), "cosmos"); err != nil {
		t.Fatalf("status: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "cosmos") || !strings.Contains(s, "installed") {
		t.Errorf("status output missing cosmos/installed:\n%s", s)
	}
}

func TestAdapterFor_GitHubDeferred(t *testing.T) {
	_, err := adapterFor(appcatalog.Entry{Source: appcatalog.Source{Type: appcatalog.SourceGitHub}}, fakeInspect{})
	if err == nil {
		t.Error("github adapter is deferred and should error")
	}
}
```

This test references two sentinels and a helper that live in the test file — declare them at the top of `app_test.go`:

```go
import "errors"

var (
	errSigForTest       = errors.New("no matching signatures")
	errCMMissingForTest = errors.New("not found")
)
```

…but the production `State.Load` only treats its **own** `errMissingConfigMap` sentinel as "absent". Since the fake is in package `main` (not `appcatalog`), it cannot return that unexported sentinel. **Resolution:** the fake's `GetConfigMap` returns `(nil, nil)` for an absent map and `State.Load`/`unmarshalRecords` already handle empty input as zero records — so drop `errCMMissingForTest` and have the fake return `return []byte("{}"), nil` when `f.cm == nil`. Update the fake accordingly (and `marshalCMForTest` below). The final `app_test.go` fake is:

```go
func (f *fakeKube) GetConfigMap(ns, name string) ([]byte, error) {
	if f.cm == nil {
		return []byte("{}"), nil
	}
	return marshalCMForTest(f.cm), nil
}

func marshalCMForTest(data map[string]string) []byte {
	b, err := yaml.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}
```

(add `import "gopkg.in/yaml.v3"`, and remove the unused `errCMMissingForTest`.)

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./cmd/srectl/ -run 'RunApp|AdapterFor' -v`
Expected: FAIL — `undefined: runAppList`, `undefined: appDeps`, `undefined: adapterFor`, etc.

- [ ] **Step 3: Write minimal implementation**

`app.go`:

```go
package main

import (
	"fmt"
	"io"
	"os/user"
	"text/tabwriter"
	"time"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog/source"
	"github.com/spf13/cobra"
)

// defaultCatalogPath is the shipped catalog location (repo-root catalog.yaml).
const defaultCatalogPath = "catalog.yaml"

// appDeps bundles the appcatalog collaborators a command run needs. Production
// builds it from the real exec wrappers (newAppDeps); tests inject fakes so the
// commands are exercised without a cluster, registry, or external binaries.
type appDeps struct {
	// Cat is the loaded catalog.
	Cat *appcatalog.Catalog
	// Cosign verifies signatures (fail-closed).
	Cosign appcatalog.Cosign
	// UDS deploys/removes packages.
	UDS appcatalog.UDS
	// Inspect supplies zarf inspect output to preflight.
	Inspect appcatalog.Inspector
	// State reads/writes the install record + lists live packages.
	State appcatalog.State
	// Zarf resolves OCI refs to digests (the source.OCI adapter's dependency).
	Zarf source.Zarf
	// Now returns the install timestamp (RFC3339); injectable for tests.
	Now func() string
	// Actor is who performed the action (kubeconfig user for the CLI).
	Actor string
}

// newAppDeps builds production dependencies from the real wrappers.
func newAppDeps(catalogPath string) (appDeps, error) {
	cat, err := appcatalog.Load(catalogPath)
	if err != nil {
		return appDeps{}, err
	}
	z := source.NewZarf()
	return appDeps{
		Cat:     cat,
		Cosign:  appcatalog.NewCosign(),
		UDS:     appcatalog.NewUDS(),
		Inspect: z,
		State:   appcatalog.State{Kube: appcatalog.NewKube()},
		Zarf:    z,
		Now:     func() string { return time.Now().UTC().Format(time.RFC3339) },
		Actor:   currentActor(),
	}, nil
}

// currentActor returns the local username as a best-effort actor label for the
// CLI (the serve API will substitute the OIDC subject — deferred).
func currentActor() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

// adapterFor selects the source adapter for an entry. local and oci are MVP;
// github is deferred (spec §12) and returns a clear error.
func adapterFor(e appcatalog.Entry, z source.Zarf) (source.Adapter, error) {
	switch e.Source.Type {
	case appcatalog.SourceLocal:
		return source.Local{}, nil
	case appcatalog.SourceOCI:
		return source.OCI{Zarf: z}, nil
	case appcatalog.SourceGitHub:
		return nil, fmt.Errorf("source type %q is deferred (connected-only github adapter not in MVP)", e.Source.Type)
	default:
		return nil, fmt.Errorf("unknown source type %q", e.Source.Type)
	}
}

// newAppCmd builds the `srectl app` parent command and its subcommands.
func newAppCmd() *cobra.Command {
	var catalogPath string
	var installedOnly bool

	cmd := &cobra.Command{
		Use:   "app",
		Short: "Deploy and manage mission apps on the running substrate",
		Long: "app deploys signed mission-app bundles from the catalog onto a running SRE\n" +
			"(verify → preflight → uds deploy → record) and manages them Day-2.",
	}
	cmd.PersistentFlags().StringVar(&catalogPath, "catalog", defaultCatalogPath, "path to catalog.yaml")

	list := &cobra.Command{
		Use:   "list",
		Short: "List catalog apps (and, with --installed, what is deployed)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppList(c.OutOrStdout(), d, installedOnly)
		},
	}
	list.Flags().BoolVar(&installedOnly, "installed", false, "cross-check the install record against live UDS Packages")

	install := &cobra.Command{
		Use:   "install <name>",
		Short: "Verify and deploy an app from the catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppInstall(c.OutOrStdout(), d, args[0])
		},
	}

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a deployed app and prune its record",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppRemove(c.OutOrStdout(), d, args[0])
		},
	}

	status := &cobra.Command{
		Use:   "status <name>",
		Short: "Show an app's install record and live presence",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppStatus(c.OutOrStdout(), d, args[0])
		},
	}

	cmd.AddCommand(list, install, remove, status)
	return cmd
}

// runAppList prints the catalog; with installedOnly it cross-checks the record
// against live UDS Packages and flags drift (spec §6).
func runAppList(out io.Writer, d appDeps, installedOnly bool) error {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if !installedOnly {
		fmt.Fprintln(tw, "NAME\tVERSION\tSOURCE\tDESCRIPTION")
		for _, e := range d.Cat.Apps {
			fmt.Fprintf(tw, "%s\t%s\t%s:%s\t%s\n", e.Name, e.Version, e.Source.Type, e.Source.Ref, e.Description)
		}
		return tw.Flush()
	}

	recs, err := d.State.Load()
	if err != nil {
		return err
	}
	live, err := d.State.InstalledPackages()
	if err != nil {
		return err
	}
	fmt.Fprintln(tw, "NAME\tRECORD\tLIVE\tNOTE")
	seen := map[string]bool{}
	for name, r := range recs {
		seen[name] = true
		note := ""
		if !live[name] {
			note = "drift: recorded but no live Package"
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", name, r.Version, live[name], note)
	}
	for name := range live {
		if !seen[name] {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", name, "-", true, "drift: live Package without a record")
		}
	}
	return tw.Flush()
}

// runAppInstall executes the deploy flow (spec §5): resolve → verify (fail-closed)
// → advisory preflight → deploy (best-effort rollback) → record.
func runAppInstall(out io.Writer, d appDeps, name string) error {
	e, ok := d.Cat.Find(name)
	if !ok {
		return fmt.Errorf("app %q is not in the catalog", name)
	}

	adapter, err := adapterFor(e, d.Zarf)
	if err != nil {
		return err
	}
	ref, digest, err := adapter.Resolve(e)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "resolved %s → %s\n", e.Name, ref)

	if err := appcatalog.Verify(d.Cosign, e, ref); err != nil {
		return err // fail-closed: aborts before deploy
	}
	fmt.Fprintf(out, "signature verified (identity %q)\n", e.Verify.IdentityRegexp)

	live, err := d.State.InstalledPackages()
	if err != nil {
		// Preflight requires-check is advisory; an unreachable cluster here is a
		// real problem for the deploy that follows, so surface it.
		return err
	}
	warns, err := appcatalog.Preflight(d.Inspect, e, ref, live)
	if err != nil {
		return err
	}
	for _, w := range warns {
		fmt.Fprintf(out, "warning [%s]: %s\n", w.Code, w.Message)
	}

	if err := appcatalog.Deploy(d.UDS, ref); err != nil {
		return err
	}
	fmt.Fprintf(out, "deployed %s\n", e.Name)

	rec := appcatalog.Record{
		Version:     e.Version,
		Source:      string(e.Source.Type) + ":" + e.Source.Ref,
		Digest:      digest,
		InstalledAt: d.Now(),
		InstalledBy: d.Actor,
	}
	if err := d.State.Put(e.Name, rec); err != nil {
		return err
	}
	fmt.Fprintf(out, "recorded install of %s %s\n", e.Name, e.Version)
	return nil
}

// runAppRemove removes the app and prunes its record (spec §7).
func runAppRemove(out io.Writer, d appDeps, name string) error {
	if err := appcatalog.Remove(d.UDS, name); err != nil {
		return err
	}
	if err := d.State.Delete(name); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s and pruned its record\n", name)
	return nil
}

// runAppStatus reports an app's recorded metadata and whether a live Package
// exists (spec §6 — record is convenience, cluster is truth).
func runAppStatus(out io.Writer, d appDeps, name string) error {
	recs, err := d.State.Load()
	if err != nil {
		return err
	}
	live, err := d.State.InstalledPackages()
	if err != nil {
		return err
	}
	rec, recorded := recs[name]
	state := "not installed"
	if recorded {
		state = "installed"
	}
	fmt.Fprintf(out, "%s: %s\n", name, state)
	if recorded {
		fmt.Fprintf(out, "  version:     %s\n", rec.Version)
		fmt.Fprintf(out, "  source:      %s\n", rec.Source)
		fmt.Fprintf(out, "  digest:      %s\n", rec.Digest)
		fmt.Fprintf(out, "  installedAt: %s\n", rec.InstalledAt)
		fmt.Fprintf(out, "  installedBy: %s\n", rec.InstalledBy)
	}
	fmt.Fprintf(out, "  live UDS Package: %t\n", live[name])
	if recorded && !live[name] {
		fmt.Fprintln(out, "  note: drift — recorded but no live Package")
	}
	if !recorded && live[name] {
		fmt.Fprintln(out, "  note: drift — live Package without a record")
	}
	return nil
}
```

Then wire it into `main.go` (Modify `installer/cmd/srectl/main.go:30-34`):

```go
	root.AddCommand(
		newPreflightCmd(),
		newInstallCmd(),
		newAppCmd(),
		newVersionCmd(),
	)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./... -v`
Expected: PASS — the new `cmd/srectl` app tests plus all existing installer tests. `go build ./...` and `go vet ./...` clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add installer/cmd/srectl/app.go installer/cmd/srectl/app_test.go installer/cmd/srectl/main.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(srectl): app list/install/remove/status commands"
```

---

### Task 9: The shipped `catalog.yaml` (cosmos = entry #1)

The default catalog the binary ships against, at repo root (spec §9). cosmos is the reference app and entry #1, declaring an `oci` source against its signed bundle and the keyless signer identity matching `release.yml`/the SP4 chart-signing. A test in the `appcatalog` package loads this real file (via a relative path from the test) to guard it against drift — the round-2 analog of round-1's `TestRequiredMatchesBundle`.

**Files:**
- Create: `catalog.yaml` (repo root)
- Test: `installer/internal/appcatalog/shipped_catalog_test.go`

**Interfaces:**
- Consumes: `appcatalog.Load`, `appcatalog.Catalog.Find`, `appcatalog.SourceOCI` (Task 1).
- Produces: no new Go API — content + a guard test.

- [ ] **Step 1: Write the failing test**

```go
package appcatalog

import (
	"path/filepath"
	"testing"
)

// shippedCatalogPath locates the repo-root catalog.yaml relative to this test
// file (installer/internal/appcatalog/ → ../../../catalog.yaml).
func shippedCatalogPath() string {
	return filepath.Join("..", "..", "..", "catalog.yaml")
}

func TestShippedCatalog_CosmosIsEntryOne(t *testing.T) {
	c, err := Load(shippedCatalogPath())
	if err != nil {
		t.Fatalf("the shipped catalog.yaml must load + validate: %v", err)
	}
	if len(c.Apps) == 0 || c.Apps[0].Name != "cosmos" {
		t.Fatalf("cosmos must be catalog entry #1, got %+v", c.Apps)
	}
	cosmos := c.Apps[0]
	if cosmos.Source.Type != SourceOCI || cosmos.Source.Ref == "" {
		t.Errorf("cosmos must declare an oci source with a ref, got %+v", cosmos.Source)
	}
	if cosmos.Verify.IdentityRegexp == "" || cosmos.Verify.Issuer == "" {
		t.Errorf("cosmos must declare an expected signer identity (fail-closed), got %+v", cosmos.Verify)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run TestShippedCatalog -v`
Expected: FAIL — `appcatalog: read ../../../catalog.yaml: open …: no such file or directory`.

- [ ] **Step 3: Write minimal implementation**

Create `catalog.yaml` at the repo root:

```yaml
# SRE mission-app catalog — the apps `srectl app install` can deploy onto a
# running substrate. Each entry is a signed UDS bundle resolved by its source
# adapter, verified against the declared keyless signer identity (fail-closed),
# then `uds deploy`ed so the UDS Operator auto-wires cohesion.
#
# cosmos is the reference app and entry #1 (the app-onboarding contract incarnate).
apiVersion: sre/v1
apps:
  - name: cosmos
    version: "2.102.0"
    description: "COSMOS — mission app (PM/CRM/Lead Tracker over the shared substrate)."
    source:
      type: oci
      ref: ghcr.io/jongodb-labs/bundles/cosmos
    verify:
      identityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/"
      issuer: "https://token.actions.githubusercontent.com"
    requires: [postgres]
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd installer && go test ./internal/appcatalog/ -run TestShippedCatalog -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add catalog.yaml installer/internal/appcatalog/shipped_catalog_test.go
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "feat(appcatalog): ship default catalog.yaml with cosmos as entry #1"
```

---

### Task 10: cosmos dogfood acceptance (manual/integration)

The MVP's "done" bar (spec §11 Acceptance): re-deploy cosmos **through the catalog** onto the running SRE and observe cohesion wire up. This needs the live RKE2 cluster, a reachable registry, and real `uds`/`zarf`/`cosign`/`kubectl`, so it is documented as a **manual/integration acceptance** appended to `docs/app-onboarding.md` (not a Go unit test — the unit tests in Tasks 1–9 cover the logic with fakes). The commit is doc-only.

**Files:**
- Modify: `docs/app-onboarding.md` (append a "Round-2 acceptance — deploy cosmos via the catalog" section)

**Interfaces:**
- Consumes: the built `srectl` binary from Tasks 1–9; the live substrate.
- Produces: a runnable, copy-pasteable acceptance checklist (no Go API).

- [ ] **Step 1: Append the acceptance section to `docs/app-onboarding.md`**

Append this section verbatim:

```markdown
## Round-2 acceptance — deploy cosmos via the catalog

This is the round-2 "MVP done" bar (app-catalog spec §11): cosmos, re-deployed
**through `srectl app`**, wiring substrate cohesion automatically. It runs against
the live SRE (RKE2 + UDS Core) with a reachable bundle registry and the `uds`,
`zarf`, `cosign`, `kubectl` binaries on PATH. It is a manual/integration check —
the unit suite (`go test ./...`) already covers the logic with fakes.

Preconditions:
- The substrate is up and `kubectl get nodes` is Ready.
- PGO is installed (cosmos `requires: [postgres]`); else expect the advisory
  `missing-require` warning and a degraded cosmos.
- `catalog.yaml` (repo root) lists cosmos as entry #1.

Steps:
1. Build: `cd installer && go build -o /tmp/srectl ./cmd/srectl`
2. List the catalog — cosmos appears:
   `/tmp/srectl app list`
   Expect a row: `cosmos  2.102.0  oci:ghcr.io/jongodb-labs/bundles/cosmos  …`
3. Install through the catalog:
   `/tmp/srectl app install cosmos`
   Expect, in order: `resolved cosmos → …@sha256:…`, `signature verified …`,
   any advisory warnings, `deployed cosmos`, `recorded install of cosmos 2.102.0`.
   (If the signature does not verify, the command MUST abort here with the
   expected-identity message and never reach `uds deploy` — fail-closed.)
4. Cohesion wired (the authoritative post-deploy check):
   - `kubectl get virtualservice -A | grep cosmos` → a VirtualService exists.
   - If cosmos declares `sso`: `kubectl -n keycloak get secret keycloak-client-secrets -o yaml | grep cosmos` → a client entry exists.
5. Record written:
   `kubectl -n sre-system get configmap sre-appcatalog-installs -o yaml`
   → a `cosmos:` key with version/source/digest/installedAt/installedBy.
6. Status is green and drift-free:
   `/tmp/srectl app status cosmos`
   → `cosmos: installed`, the record fields, and `live UDS Package: true` with no
   drift note.
7. Drift visibility (optional): `/tmp/srectl app list --installed` shows cosmos
   with `LIVE true` and no drift note.

Teardown (optional): `/tmp/srectl app remove cosmos` → `removed cosmos and pruned
its record`; re-running `app status cosmos` reports `not installed`.

Pass criterion: steps 2–6 succeed as described; the VirtualService (and SSO client
if declared) appear; the record is written; `app status` is green. That is MVP done.
```

- [ ] **Step 2: Verify the doc renders and the binary builds**

Run: `cd installer && go build -o /tmp/srectl ./cmd/srectl && /tmp/srectl app list --help`
Expected: builds clean; help text for `app list` prints (proves the command tree is wired). The cluster steps (3–7) are executed by the operator against the live SRE during acceptance, not in CI.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2 && git add docs/app-onboarding.md
git -c user.email="198221045+JongoDB@users.noreply.github.com" commit -m "docs(app-onboarding): round-2 cosmos-via-catalog acceptance"
```

---

## Self-Review

**1. Spec coverage** (each §, mapped to a task):

- §4 catalog model + source-adapter table → Task 1 (model), Tasks 2/3 (`local`/`oci`), Task 8 `adapterFor` defers `github` (§12). ✅
- §5 deploy flow steps 1–6 → resolve (Tasks 2/3 + Task 8), verify fail-closed (Task 4), advisory preflight (Task 5), deploy + best-effort rollback (Task 6), record (Task 7), confirm/cohesion (Task 10 acceptance — the authoritative post-deploy check, since it needs the live VirtualService/SSO artifacts). ✅
- §6 state model — ConfigMap `sre-appcatalog-installs` in `sre-system`, per-app keys, drift vs live Packages → Task 7 (`State`, `Record`, `InstalledPackages`) + Task 8 (`list --installed`, `status` drift notes). ✅
- §7 Day-2 — `list [--installed]`, `status`, `remove`; `update` deferred → Task 8 (built) ; `update` correctly **not** built (§12). ✅
- §8 security — fail-closed verify (Task 4), audit via the record (Task 7 `installedBy`/`installedAt`), airgap `oci`+`local` (Tasks 2/3). RBAC for the CLI is "relies on cluster RBAC/kubeconfig" — no code needed beyond using kubectl; the serve-API auth is deferred. ✅
- §9 file structure → Tasks map 1:1 to the listed files; deferred files (`source/github.go`, `cmd/srectl/serve.go`) intentionally **not** created. ✅
- §10 error handling table → catalog invalid (Task 1 `Validate`), source unreachable (Tasks 2/3 wrap errors; `github` deferred message in Task 8), signature invalid/absent abort (Task 4), `requires` warns (Task 5), `uds deploy` fails → best-effort remove + record-not-written (Task 6 ordering: `Deploy` before `State.Put` in Task 8 `runAppInstall`), drift surfaced never reconciled (Task 8). ✅
- §11 testing — unit: catalog (T1), local+oci (T2/T3), verify stubbed pass+fail (T4), state r/w (T7), preflight with/without CR (T5); acceptance dogfood (T10). ✅
- §12 MVP scope — **build:** list/install/remove/status (T8), catalog.yaml (T9), local+oci (T2/T3), cosign-verify (T4), preflight (T5), uds deploy/remove (T6), record ConfigMap (T7), cosmos entry #1 + dogfood (T9/T10). **defer:** `github` adapter (not created; T8 errors), `srectl serve`+SP8 console (not created), `app update` (not created). ✅
- §13 Future — explicitly out of scope; no tasks. ✅ (correct.)

No gaps found.

**2. Placeholder scan:** No "TBD"/"TODO"/"implement later"/"add error handling"/"similar to Task N" — every code step has real Go. Every `run …` command has an expected result. The one forward-reference resolved inline: `reValid` (defined Task 1) is unused until Task 4 — flagged in Task 1 Step 4 with the rationale that Task 4's tests exercise it in the same package, so `go vet` stays clean across commits. The Task 8 test's ConfigMap-absent sentinel mismatch (a cross-package unexported-sentinel trap) is caught and resolved inline (the fake returns `[]byte("{}")`, which `unmarshalRecords` already treats as zero records).

**3. Type consistency** (names used in later tasks match earlier definitions):

- `appcatalog.Entry`/`Source`/`Verify`/`Catalog` (T1) — used identically in T2/T3/T4/T5/T8/T9. ✅
- `SourceType` consts `SourceLocal`/`SourceOCI`/`SourceGitHub` (T1) — switched on in T2/T3/T8 with the same names. ✅
- `Adapter.Resolve(e Entry) (ref, digest string, err error)` (T2) — `Local` (T2) and `OCI` (T3) implement exactly this signature; `adapterFor` returns `source.Adapter` (T8). ✅
- `Zarf.Inspect(ref) ([]byte,error)` (T3) and `appcatalog.Inspector.Inspect(ref) ([]byte,error)` (T5) — structurally identical; T8 passes the same `source.NewZarf()` value as both `d.Zarf` and `d.Inspect`. ✅
- `Cosign.Verify(ref,identityRegexp,issuer string) error` (T4) — `execCosign` impl + `Verify(c Cosign, e Entry, ref string)` free func; T8 calls `appcatalog.Verify(d.Cosign, e, ref)`. ✅
- `UDS.Deploy(ref)`/`Remove(name)` + free funcs `Deploy(u UDS, ref)`/`Remove(u UDS, name)` (T6) — T8 calls `appcatalog.Deploy(d.UDS, ref)` / `appcatalog.Remove(d.UDS, name)`. ✅
- `commandContext` — two **separate** package-level vars (one in `source` T3, one in `appcatalog` T4), each swapped only within its own package's tests. Intentional, not a collision. ✅
- `Kube` interface methods + `State.{Load,Put,Delete,InstalledPackages}` + `Record` (T7) — T8 uses `appcatalog.State{Kube:…}`, `.Load()`, `.Put(name,Record{…})`, `.Delete(name)`, `.InstalledPackages()`, and constructs `appcatalog.Record{Version,Source,Digest,InstalledAt,InstalledBy}` with the exact field names. ✅
- `SystemNamespace = "sre-system"`, `InstallsConfigMap = "sre-appcatalog-installs"` (T7) — T8/T10 reference `sre-system` + `sre-appcatalog-installs` consistently. ✅
- `appDeps` fields (T8) — defined once, used by all four `run…` funcs with matching names. ✅

No naming drift found. Plan is internally consistent and ready to execute.
