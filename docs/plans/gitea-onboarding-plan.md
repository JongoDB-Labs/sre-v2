# Gitea Onboarding (Mission App #2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Onboard Gitea as the second mission app through `srectl app install`, per the approved `docs/specs/gitea-onboarding-design.md` — proving the substrate + DSOP recipe is generic (second signer identity, declarative `Package.sso`, PGO pguser path, first real MinIO Tenant, first real bundle publish).

**Architecture:** Two repos. A new public `JongoDB-Labs/sre-apps` monorepo holds the Gitea packaging (thin wrapper chart with the UDS `Package`/`PostgresCluster`/`Tenant` CRs + upstream chart values + zarf package + UDS bundle + signed release CI). `sre-v2` gets the hardened OCI resolver, the `gitea` catalog entry, lab-gated operator Package CRs, and docs.

**Tech Stack:** Go (srectl, stdlib testing + hand-written fakes), Helm 4 / upstream gitea chart v12.7.0, zarf v0.82.0, uds-cli v0.34.3, cosign keyless (GitHub OIDC), GitHub Actions.

## Global Constraints

- Naming contract: zarf package name, bundle `metadata.name`, catalog entry `name`, and the app's `Package` CR name are all exactly **`gitea`**.
- Versions: app **1.27.0**, upstream chart **12.7.0**, release/bundle version **`1.27.0-1`** (tag `gitea-v1.27.0-1`; `-N` bumps for packaging-only changes).
- Signer identity: `^https://github.com/JongoDB-Labs/sre-apps/`, issuer `https://token.actions.githubusercontent.com`.
- **Commit rules:** commit as the configured git identity (`JongoDB <198221045+JongoDB@users.noreply.github.com>`); **NO AI attribution trailers of any kind** (already enforced via settings — do not add manually).
- sre-v2 build style: PR-per-slice, squash-merge; tests green (`cd installer && go test ./...`) before any commit claiming green.
- Destructive/mutating cluster steps are **lab-only** (`cosmos-ssh` / cosmos-k8s). Task 11 is lab-gated; skip it if the lab is unreachable and return to it later.
- sre-apps repo: public, AGPL-3.0. `.claude/`, `CLAUDE.md`, `docs/superpowers/` gitignored.
- Domain values target the lab: `uds.dev` (host `gitea` → `https://gitea.uds.dev`).

---

### Task 1: srectl OCI resolver hardening (slice S1)

**Files:**
- Modify: `installer/internal/appcatalog/source/exec.go`
- Modify: `installer/internal/appcatalog/source/oci.go`
- Modify: `installer/internal/appcatalog/source/oci_test.go`
- Modify: `installer/internal/appcatalog/source/exec_test.go`

**Interfaces:**
- Consumes: `appcatalog.Entry` (fields `.Version`, `.Source.Ref`) — unchanged.
- Produces: `source.Zarf` interface now has a single method `RegistryDigest(ref string) ([]byte, error)` (replacing `Inspect`). `source.OCI{Zarf: z}.Resolve(entry)` still returns `(pinnedRef, digest, error)` per the `source.Adapter` interface — callers (`installer/cmd/srectl/app.go`) need **no** changes (they only reference the `source.Zarf` type and `source.NewZarf()`, both of which keep their names).

Work on a branch: `git checkout -b feat/appcatalog-oci-resolver` (from up-to-date `main`).

- [ ] **Step 1: Rewrite `oci_test.go` with the new failing tests**

Replace the entire contents of `installer/internal/appcatalog/source/oci_test.go` with:

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
```

- [ ] **Step 2: Run the tests to verify they fail to compile (interface change)**

Run: `cd installer && go test ./internal/appcatalog/source/ 2>&1 | head -20`
Expected: compile errors — `fakeZarf` doesn't implement `Inspect`; `RegistryDigest` undefined.

- [ ] **Step 3: Rewrite `exec.go` — `RegistryDigest` replaces `Inspect`**

Replace the entire contents of `installer/internal/appcatalog/source/exec.go` with:

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
	// RegistryDigest returns the OCI manifest digest for a tagged ref, via
	// `zarf tools registry digest` (embedded crane). Output is one bare
	// `sha256:…` line — the exact digest cosign signs and verifies.
	RegistryDigest(ref string) ([]byte, error)
}

// execZarf is the real Zarf wrapper.
type execZarf struct{}

// NewZarf returns the production Zarf wrapper.
func NewZarf() Zarf { return execZarf{} }

// RegistryDigest runs `zarf tools registry digest <ref>` and returns its stdout.
func (execZarf) RegistryDigest(ref string) ([]byte, error) {
	out, err := commandContext("zarf", "tools", "registry", "digest", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("zarf tools registry digest %s: %w", ref, err)
	}
	return out, nil
}
```

- [ ] **Step 4: Rewrite `oci.go` — version-tagged resolution + strict digest parse**

Replace the entire contents of `installer/internal/appcatalog/source/oci.go` with:

```go
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
```

- [ ] **Step 5: Update `exec_test.go` for the new command**

Replace the entire contents of `installer/internal/appcatalog/source/exec_test.go` with:

```go
package source

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecZarf_BuildsRegistryDigestCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		// Return a harmless command so .Output() doesn't hit a real binary.
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	if _, err := (execZarf{}).RegistryDigest("ghcr.io/x/bundles/gitea:1.0.0"); err != nil {
		t.Fatalf("RegistryDigest: %v", err)
	}
	if gotName != "zarf" {
		t.Errorf("binary = %q, want zarf", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "tools registry digest") || !strings.Contains(joined, "ghcr.io/x/bundles/gitea:1.0.0") {
		t.Errorf("args = %v, want a `tools registry digest <ref>` invocation", gotArgs)
	}
}
```

- [ ] **Step 6: Run the full installer suite**

Run: `cd installer && go test ./...`
Expected: ALL PASS (nothing outside `source/` referenced `Inspect` — `grep -rn "\.Inspect(" installer/ --include="*.go"` must return nothing; if it returns hits, fix those call sites to the new method before committing).

- [ ] **Step 7: Commit, push, open the S1 PR**

```bash
git add installer/internal/appcatalog/source/
git commit -m "feat(appcatalog): resolve OCI digests via registry manifest digest

The oci source adapter now appends the catalog entry's version as the tag
(no more silent :latest) and resolves the digest with zarf tools registry
digest (crane) instead of grepping the first sha256 out of zarf package
inspect output. Already-@-pinned refs skip the registry. Strict bare-digest
parse fails closed."
git push -u origin feat/appcatalog-oci-resolver
gh pr create --title "feat(appcatalog): harden OCI resolver (version-tagged, manifest-digest)" \
  --body "S1 of docs/specs/gitea-onboarding-design.md §5.2. Kills the grep-first-sha256 fragility and the implicit :latest resolution; the catalog version now pins what installs."
```

---

### Task 2: sre-apps repo scaffold (slice S2a)

**Files (new repo `JongoDB-Labs/sre-apps`, working dir `~/jondev/sre-apps`):**
- Create: `README.md`, `LICENSE`, `.gitignore`

**Interfaces:**
- Produces: the public GitHub repo whose Actions OIDC identity (`^https://github.com/JongoDB-Labs/sre-apps/`) the catalog verifies (Task 10), and the directory layout Tasks 3–9 fill in.

- [ ] **Step 1: Create and clone the repo**

```bash
cd ~/jondev
gh repo create JongoDB-Labs/sre-apps --public \
  --description "Mission-app packagings for the SRE substrate — signed UDS bundles installed via srectl app. gitea is app #2 (the first OSS onboarding)." \
  --clone
cd sre-apps
```

- [ ] **Step 2: Scaffold files**

`.gitignore`:
```
.claude/
CLAUDE.md
docs/superpowers/
.superpowers/
*.tar.zst
zarf-sbom/
```

`LICENSE`: `curl -fsSL https://www.gnu.org/licenses/agpl-3.0.txt -o LICENSE`

`README.md`:
```markdown
# sre-apps

Mission-app packagings for the SRE substrate ([sre-v2](https://github.com/JongoDB-Labs/sre-v2)).
Each app directory holds a thin UDS wrapper chart (the `Package` CR + the app's data
CRs), the upstream chart values, a zarf package, and a UDS bundle. Releases are
published to `ghcr.io/jongodb-labs/bundles/<app>`, cosign-signed (keyless) by this
repo's GitHub Actions identity, and installed onto a running substrate via
`srectl app install <app>`.

| App | Version | Bundle |
|---|---|---|
| gitea | 1.27.0 (chart 12.7.0) | `ghcr.io/jongodb-labs/bundles/gitea` |

Releasing: tag `<app>-v<version>-<rev>` (e.g. `gitea-v1.27.0-1`). The release
workflow creates the zarf package + bundle, publishes, cosign-signs the published
OCI digest, verifies-after-sign (fail-closed), and attaches SLSA provenance.
```

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "chore: scaffold sre-apps (AGPL-3.0)" && git push -u origin main
```

---

### Task 3: Gitea wrapper chart — `Package` CR (slice S2b)

**Files:**
- Create: `gitea/chart/Chart.yaml`, `gitea/chart/values.yaml`, `gitea/chart/templates/uds-package.yaml`

**Interfaces:**
- Consumes: values keys defined in `values.yaml` below (Tasks 4–5 reuse them).
- Produces: UDS `Package` CR named `gitea`; SSO secret `gitea-oidc-client` (keys `key`/`secret`) in ns `gitea` — consumed by the upstream chart's `gitea.oauth[0].existingSecret` (Task 6).

- [ ] **Step 1: Write `Chart.yaml`**

```yaml
apiVersion: v2
name: gitea-uds
description: SRE substrate wiring for Gitea — the UDS Package CR (ingress/SSO/netpol/monitor) plus the app's own data instances (PGO PostgresCluster, MinIO Tenant).
type: application
version: 0.1.0
appVersion: "1.27.0"
```

- [ ] **Step 2: Write `values.yaml`**

```yaml
# Substrate wiring values. The upstream gitea chart's values live in
# ../values-gitea.yaml (consumed by the zarf component), NOT here.
domain: uds.dev
host: gitea

sso:
  clientId: gitea
  name: "Gitea"
  secretName: gitea-oidc-client

postgres:
  clusterName: gitea-pg
  storage: 5Gi
  backupStorage: 5Gi

minio:
  tenantName: gitea-minio
  bucket: gitea
  rootConfigSecret: gitea-minio-creds      # operator config (key config.env), SOPS/out-of-band
  appCredsSecret: gitea-minio-app-creds    # keys accessKey/secretKey, SOPS/out-of-band
  storage: 10Gi
  # mc image for the bootstrap Job — digest-pinned at Task 5 step 1.
  mcImage: ""
```

- [ ] **Step 3: Write `templates/uds-package.yaml`**

```yaml
apiVersion: uds.dev/v1alpha1
kind: Package
metadata:
  name: gitea
spec:
  network:
    expose:
      - service: gitea-http
        selector:
          app.kubernetes.io/name: gitea
        host: {{ .Values.host | quote }}
        gateway: tenant
        port: 3000
    allow:
      - direction: Egress
        remoteGenerated: IntraNamespace
      - direction: Ingress
        remoteGenerated: IntraNamespace
      # PGO/Patroni uses the kube API as its DCS — without this Postgres
      # crash-loops (runbook gotcha #11). The MinIO Tenant sidecar needs it too.
      - direction: Egress
        remoteGenerated: KubeAPI
      # Native-OIDC app: discovery/authorize/token all resolve via
      # https://sso.<domain>, which hairpins through the tenant gateway under
      # Istio ambient. Scoped rule — NOT remoteGenerated: Anywhere.
      - description: "SSO — OIDC via tenant gateway"
        direction: Egress
        selector:
          app.kubernetes.io/name: gitea
        remoteNamespace: istio-tenant-gateway
        remoteSelector:
          app: tenant-ingressgateway
        port: 443
      # The MinIO operator (minio-operator ns) manages the Tenant's pods in
      # THIS namespace; default-deny would sever it.
      - description: "MinIO operator -> tenant"
        direction: Ingress
        remoteNamespace: minio-operator
  monitor:
    - selector:
        app.kubernetes.io/name: gitea
      targetPort: 3000
      portName: http
      path: /metrics
      kind: ServiceMonitor
      description: "Gitea metrics"
  sso:
    - name: {{ .Values.sso.name | quote }}
      clientId: {{ .Values.sso.clientId | quote }}
      enabled: true
      standardFlowEnabled: true
      publicClient: false
      redirectUris:
        - {{ printf "https://%s.%s/user/oauth2/keycloak/callback" .Values.host .Values.domain | quote }}
      secretConfig:
        name: {{ .Values.sso.secretName | quote }}
        template:
          # The upstream gitea chart's oauth existingSecret expects EXACTLY
          # the keys "key" (client id) and "secret" (client secret).
          key: "clientField(clientId)"
          secret: "clientField(secret)"
```

- [ ] **Step 4: Lint and commit**

Run: `helm lint gitea/chart` — expect `0 chart(s) failed`.

```bash
git add gitea/chart && git commit -m "feat(gitea): wrapper chart — UDS Package CR (ingress, scoped SSO egress, monitor, declarative sso client)"
```

---

### Task 4: Wrapper chart — `PostgresCluster` CR (slice S2c)

**Files:**
- Create: `gitea/chart/templates/postgrescluster.yaml`

**Interfaces:**
- Produces: PGO cluster `gitea-pg` in the release namespace; PGO auto-generates secret `gitea-pg-pguser-gitea` (key `password`) and TLS secret `gitea-pg-cluster-cert` (key `ca.crt`) — both consumed by the upstream chart values (Task 6).

- [ ] **Step 1: Write `templates/postgrescluster.yaml`**

```yaml
apiVersion: postgres-operator.crunchydata.com/v1
kind: PostgresCluster
metadata:
  name: {{ .Values.postgres.clusterName }}
spec:
  postgresVersion: 16
  instances:
    - name: instance1
      replicas: 1
      dataVolumeClaimSpec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: {{ .Values.postgres.storage }}
  users:
    # PGO usernames can't contain "_" (DNS-label rule). No SUPERUSER — the
    # declared user is just the owner of its own database (unlike cosmos).
    - name: gitea
      databases: [gitea]
  backups:
    pgbackrest:
      repos:
        - name: repo1
          volume:
            volumeClaimSpec:
              accessModes: ["ReadWriteOnce"]
              resources:
                requests:
                  storage: {{ .Values.postgres.backupStorage }}
      manual:
        repoName: repo1
```

- [ ] **Step 2: Lint and commit**

Run: `helm lint gitea/chart` — expect pass.

```bash
git add gitea/chart/templates/postgrescluster.yaml
git commit -m "feat(gitea): PostgresCluster gitea-pg — least-priv owner role, pgBackRest volume repo"
```

---

### Task 5: Wrapper chart — MinIO `Tenant` + bootstrap Job (slice S2d)

**Files:**
- Create: `gitea/chart/templates/minio-tenant.yaml`, `gitea/chart/templates/minio-bootstrap-job.yaml`
- Modify: `gitea/chart/values.yaml` (fill `minio.mcImage`)

**Interfaces:**
- Consumes: SOPS/out-of-band secrets `gitea-minio-creds` (key `config.env`: `export MINIO_ROOT_USER=…`/`export MINIO_ROOT_PASSWORD=…`) and `gitea-minio-app-creds` (keys `accessKey`, `secretKey`).
- Produces: Tenant `gitea-minio` (S3 at `gitea-minio-hl:9000`, HTTP, bucket `gitea`); a least-priv MinIO user (from `gitea-minio-app-creds`) with a bucket-scoped policy — the creds the upstream chart's storage env consumes (Task 6).

- [ ] **Step 1: Pin the mc image by digest**

Run: `zarf tools registry digest docker.io/minio/mc:latest` (with `~/bin` on PATH). Copy the `sha256:…` output and set in `gitea/chart/values.yaml`:

```yaml
  mcImage: "docker.io/minio/mc@sha256:<PASTE-THE-DIGEST>"
```

- [ ] **Step 2: Write `templates/minio-tenant.yaml`**

```yaml
apiVersion: minio.min.io/v2
kind: Tenant
metadata:
  name: {{ .Values.minio.tenantName }}
spec:
  configuration:
    name: {{ .Values.minio.rootConfigSecret }}
  # Lab posture: HTTP path-style in-namespace (matches cosmos; substrate-wide
  # MinIO TLS is a separate pending decision — spec §3.4).
  requestAutoCert: false
  pools:
    - name: pool-0
      servers: 1
      volumesPerServer: 1
      volumeClaimTemplate:
        spec:
          accessModes: ["ReadWriteOnce"]
          resources:
            requests:
              storage: {{ .Values.minio.storage }}
  buckets:
    - name: {{ .Values.minio.bucket }}
```

- [ ] **Step 3: Write `templates/minio-bootstrap-job.yaml`**

```yaml
# Least-priv IAM is imperative by design (packages/minio README: "the Tenant CR
# provisions the bucket; the app owns the retention + IAM posture").
apiVersion: batch/v1
kind: Job
metadata:
  name: gitea-minio-bootstrap
  annotations:
    "helm.sh/hook": post-install,post-upgrade
    "helm.sh/hook-delete-policy": before-hook-creation
spec:
  backoffLimit: 30
  activeDeadlineSeconds: 900
  template:
    metadata:
      labels:
        app.kubernetes.io/name: gitea-minio-bootstrap
    spec:
      restartPolicy: OnFailure
      containers:
        - name: mc
          image: {{ .Values.minio.mcImage }}
          command: ["/bin/sh", "-ec"]
          args:
            - |
              . /rootcfg/config.env
              until mc alias set t http://{{ .Values.minio.tenantName }}-hl:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"; do
                echo "waiting for tenant..."; sleep 5
              done
              cat > /tmp/policy.json <<'EOF'
              {
                "Version": "2012-10-17",
                "Statement": [
                  {
                    "Effect": "Allow",
                    "Action": ["s3:*"],
                    "Resource": [
                      "arn:aws:s3:::{{ .Values.minio.bucket }}",
                      "arn:aws:s3:::{{ .Values.minio.bucket }}/*"
                    ]
                  }
                ]
              }
              EOF
              mc admin user add t "$APP_ACCESS_KEY" "$APP_SECRET_KEY" || true
              mc admin policy create t gitea-rw /tmp/policy.json || true
              mc admin policy attach t gitea-rw --user "$APP_ACCESS_KEY" || true
              echo "bootstrap complete"
          env:
            - name: APP_ACCESS_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ .Values.minio.appCredsSecret }}
                  key: accessKey
            - name: APP_SECRET_KEY
              valueFrom:
                secretKeyRef:
                  name: {{ .Values.minio.appCredsSecret }}
                  key: secretKey
          volumeMounts:
            - name: rootcfg
              mountPath: /rootcfg
              readOnly: true
      volumes:
        - name: rootcfg
          secret:
            secretName: {{ .Values.minio.rootConfigSecret }}
            items:
              - key: config.env
                path: config.env
```

- [ ] **Step 4: Lint and commit**

Run: `helm lint gitea/chart` — expect pass.

```bash
git add gitea/chart
git commit -m "feat(gitea): MinIO Tenant gitea-minio + mc bootstrap Job (bucket-scoped least-priv user)"
```

---

### Task 6: Upstream chart values + golden/render tests (slice S2e)

**Files:**
- Create: `gitea/values-gitea.yaml`, `gitea/tests/render-test.sh`, `gitea/tests/golden/wrapper.yaml`, `Makefile`

**Interfaces:**
- Consumes: secrets/services produced by Tasks 3–5 (`gitea-oidc-client`, `gitea-pg-pguser-gitea`, `gitea-pg-cluster-cert`, `gitea-pg-primary:5432`, `gitea-minio-hl:9000`, `gitea-minio-app-creds`).
- Produces: `gitea/values-gitea.yaml` — referenced by the zarf component (Task 7); `make test` — run by CI (Task 8).

- [ ] **Step 1: Write `gitea/values-gitea.yaml`**

```yaml
# Values for the UPSTREAM gitea chart v12.7.0 (dl.gitea.com), applied by the
# zarf component. Everything external: Postgres = PGO cluster gitea-pg,
# object storage = MinIO Tenant gitea-minio, ingress = the substrate's
# tenant gateway (via the wrapper chart's Package CR), SSO = the substrate's
# Keycloak (client from Package.sso).

replicaCount: 1
# Git repos live on a RWO PVC (repos cannot go to object storage) — Recreate
# avoids the RollingUpdate maxSurge-100% volume-attach deadlock.
strategy:
  type: Recreate

# All four bundled subcharts OFF — postgresql-ha and valkey-cluster default ON.
postgresql:
  enabled: false
postgresql-ha:
  enabled: false
valkey-cluster:
  enabled: false
valkey:
  enabled: false

ingress:
  enabled: false

persistence:
  enabled: true
  size: 10Gi

deployment:
  env:
    # lib/pq honors PGSSLROOTCERT; reaches init + main containers, so the
    # configure_gitea init container's `gitea migrate` also verifies TLS.
    - name: PGSSLROOTCERT
      value: /etc/pg-ca/ca.crt

extraVolumes:
  - name: pg-ca
    secret:
      secretName: gitea-pg-cluster-cert
      items:
        - key: ca.crt
          path: ca.crt
extraContainerVolumeMounts:
  - name: pg-ca
    mountPath: /etc/pg-ca
    readOnly: true
extraInitVolumeMounts:
  - name: pg-ca
    mountPath: /etc/pg-ca
    readOnly: true

gitea:
  admin:
    existingSecret: gitea-admin-creds
    passwordMode: initialOnlyNoReset
  additionalConfigFromEnvs:
    - name: GITEA__DATABASE__PASSWD
      valueFrom:
        secretKeyRef:
          name: gitea-pg-pguser-gitea
          key: password
    - name: GITEA__STORAGE__MINIO_ACCESS_KEY_ID
      valueFrom:
        secretKeyRef:
          name: gitea-minio-app-creds
          key: accessKey
    - name: GITEA__STORAGE__MINIO_SECRET_ACCESS_KEY
      valueFrom:
        secretKeyRef:
          name: gitea-minio-app-creds
          key: secretKey
  oauth:
    - name: keycloak
      provider: openidConnect
      existingSecret: gitea-oidc-client
      autoDiscoverUrl: https://sso.uds.dev/realms/uds/.well-known/openid-configuration
      scopes: "openid profile email"
  config:
    server:
      DOMAIN: gitea.uds.dev
      ROOT_URL: https://gitea.uds.dev/
      SSH_DOMAIN: gitea.uds.dev
    database:
      DB_TYPE: postgres
      HOST: gitea-pg-primary:5432
      NAME: gitea
      USER: gitea
      SSL_MODE: verify-full
    session:
      PROVIDER: memory
    cache:
      ADAPTER: memory
    queue:
      TYPE: level
    metrics:
      ENABLED: true
    service:
      # NOT DISABLE_REGISTRATION: true — that blocks OIDC auto-registration
      # too (documented lockout trap, spec §3.2).
      ALLOW_ONLY_EXTERNAL_REGISTRATION: true
      SHOW_REGISTRATION_BUTTON: false
    oauth2_client:
      ENABLE_AUTO_REGISTRATION: true
      ACCOUNT_LINKING: auto
      USERNAME: preferred_username
    storage:
      STORAGE_TYPE: minio
      MINIO_ENDPOINT: gitea-minio-hl:9000
      MINIO_BUCKET: gitea
      MINIO_USE_SSL: false
      MINIO_BUCKET_LOOKUP: path
```

- [ ] **Step 2: Write `gitea/tests/render-test.sh`**

```bash
#!/usr/bin/env bash
# Render tests: (1) wrapper chart golden diff; (2) upstream chart assertions
# with our values (subcharts off, env wiring present). UPDATE=1 regenerates
# the golden.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "== wrapper chart golden"
RENDERED=$(mktemp)
helm template gitea chart --namespace gitea > "$RENDERED"
if [[ "${UPDATE:-}" == "1" ]]; then
  cp "$RENDERED" tests/golden/wrapper.yaml
  echo "golden updated"
fi
diff -u tests/golden/wrapper.yaml "$RENDERED"

echo "== upstream chart assertions (v12.7.0 with values-gitea.yaml)"
helm repo add gitea-charts https://dl.gitea.com/charts/ --force-update >/dev/null
UP=$(mktemp)
helm template gitea gitea-charts/gitea --version 12.7.0 --namespace gitea \
  --values values-gitea.yaml > "$UP"

fail() { echo "ASSERTION FAILED: $1"; exit 1; }
grep -q "kind: Deployment" "$UP" || fail "no gitea Deployment rendered"
grep -q "PGSSLROOTCERT" "$UP" || fail "PGSSLROOTCERT env missing"
grep -q "GITEA__DATABASE__PASSWD" "$UP" || fail "DB password env wiring missing"
grep -q "gitea-oidc-client" "$UP" || fail "oauth existingSecret wiring missing"
grep -q "type: Recreate" "$UP" || fail "strategy Recreate missing"
! grep -qE "postgresql|valkey" <(grep -E "^kind: (StatefulSet|Deployment)" -A2 "$UP") \
  || true # kinds alone can't name subcharts; check by resource name instead:
! grep -q "name: gitea-postgresql" "$UP" || fail "bundled postgresql rendered — subchart not disabled"
! grep -q "name: gitea-valkey" "$UP" || fail "bundled valkey rendered — subchart not disabled"
echo "PASS"
```

Run: `chmod +x gitea/tests/render-test.sh`

`Makefile` (repo root):
```makefile
.PHONY: test
test:
	helm lint gitea/chart
	bash gitea/tests/render-test.sh
```

- [ ] **Step 3: Generate the golden, then run the test**

Run: `mkdir -p gitea/tests/golden && UPDATE=1 bash gitea/tests/render-test.sh && make test`
Expected: `PASS`. **Inspect `tests/golden/wrapper.yaml` by hand once**: it must contain the Package CR (with sso + 5 allow rules + monitor), the PostgresCluster, the Tenant, and the Job.

- [ ] **Step 4: Commit**

```bash
git add gitea/values-gitea.yaml gitea/tests Makefile
git commit -m "feat(gitea): upstream chart values (external PG/MinIO/SSO) + golden render tests"
git push
```

---

### Task 7: zarf package + UDS bundle (slice S3a)

**Files:**
- Create: `gitea/zarf.yaml`, `gitea/bundle/uds-bundle.yaml`

**Interfaces:**
- Consumes: `gitea/chart` (Tasks 3–5), `gitea/values-gitea.yaml` (Task 6), the pinned `minio.mcImage` digest (Task 5 step 1 — reuse the same digest below).
- Produces: zarf package `gitea` and UDSBundle `gitea` version `1.27.0-1` — published by Task 9; the bundle name/`gitea` naming contract the catalog depends on (Task 10).

- [ ] **Step 1: Write `gitea/zarf.yaml`**

```yaml
kind: ZarfPackageConfig
metadata:
  name: gitea
  version: "1.27.0-1"
  description: "Gitea 1.27.0 (chart 12.7.0) onto the SRE substrate — external PGO Postgres, MinIO Tenant storage, substrate SSO/ingress via the gitea-uds wrapper chart."
components:
  - name: gitea
    required: true
    charts:
      # Wrapper first: Package CR (netpols/SSO client) + data CRs exist
      # before the app chart lands.
      - name: gitea-uds
        namespace: gitea
        localPath: chart
        version: 0.1.0
      - name: gitea
        namespace: gitea
        url: https://dl.gitea.com/charts/
        version: 12.7.0
        valuesFiles:
          - values-gitea.yaml
    images:
      # Substrate carries the crunchy-postgres + minio tenant-server images
      # (packages/pgo, packages/minio) — NOT bundled here.
      - docker.gitea.com/gitea:1.27.0-rootless
      - docker.io/minio/mc@sha256:<SAME-DIGEST-AS-TASK-5-STEP-1>
```

- [ ] **Step 2: Write `gitea/bundle/uds-bundle.yaml`**

```yaml
kind: UDSBundle
metadata:
  name: gitea
  version: "1.27.0-1"
  description: "Gitea mission-app bundle for the SRE substrate (app-only; deploys onto a running SRE)."
  architecture: amd64
packages:
  - name: gitea
    path: ../
    ref: "1.27.0-1"
```

- [ ] **Step 3: Validate both files build locally**

```bash
export PATH="$HOME/bin:$PATH"
zarf dev lint gitea 2>&1 | tail -5          # schema-valid (warnings ok, errors not)
zarf package create gitea --confirm -o gitea
uds create gitea/bundle --confirm -o .
ls uds-bundle-gitea-*.tar.zst
```
Expected: a bundle tarball exists. (This pulls the upstream chart + images — needs network; several minutes.)

- [ ] **Step 4: Commit**

```bash
git add gitea/zarf.yaml gitea/bundle
git commit -m "feat(gitea): zarf package + app-only UDS bundle (1.27.0-1)"
git push
```

---

### Task 8: PR CI workflow (slice S3b)

**Files:**
- Create: `.github/workflows/ci.yaml`

- [ ] **Step 1: Write `.github/workflows/ci.yaml`**

```yaml
name: ci
on:
  pull_request:
  push:
    branches: [main]
jobs:
  lint-and-render:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - uses: azure/setup-helm@v4
      - name: install zarf
        run: |
          curl -fsSL -o /usr/local/bin/zarf \
            https://github.com/zarf-dev/zarf/releases/download/v0.82.0/zarf_v0.82.0_Linux_amd64
          chmod +x /usr/local/bin/zarf
      - name: helm lint + render tests
        run: make test
      - name: zarf schema lint
        run: zarf dev lint gitea
```

- [ ] **Step 2: Commit, push, verify the run**

```bash
git add .github/workflows/ci.yaml
git commit -m "ci: helm lint + golden render + zarf schema lint on PRs"
git push
gh run watch --exit-status $(gh run list --workflow ci --limit 1 --json databaseId --jq '.[0].databaseId')
```
Expected: the run completes green. If it fails, read the log (`gh run view --log-failed`), fix, and re-push before proceeding.

---

### Task 9: Release workflow — publish → sign → verify → attest (slice S3c)

**Files:**
- Create: `.github/workflows/release.yaml`

**Interfaces:**
- Produces: `ghcr.io/jongodb-labs/bundles/gitea:1.27.0-1`, cosign-signed (keyless, identity `^https://github.com/JongoDB-Labs/sre-apps/`) + SLSA provenance — the artifact Task 10's catalog entry resolves and verifies.

- [ ] **Step 1: Write `.github/workflows/release.yaml`**

```yaml
name: release
on:
  push:
    tags: ["gitea-v*"]
permissions:
  contents: read
  packages: write
  id-token: write      # cosign keyless (Fulcio)
  attestations: write  # SLSA provenance
env:
  BUNDLE: ghcr.io/jongodb-labs/bundles/gitea
jobs:
  release:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v4
      - name: derive + verify version
        run: |
          VERSION="${GITHUB_REF_NAME#gitea-v}"
          echo "VERSION=$VERSION" >> "$GITHUB_ENV"
          grep -q "version: \"$VERSION\"" gitea/zarf.yaml || { echo "zarf.yaml version != tag $VERSION"; exit 1; }
          grep -q "version: \"$VERSION\"" gitea/bundle/uds-bundle.yaml || { echo "uds-bundle.yaml version != tag $VERSION"; exit 1; }
      - name: install zarf + uds + cosign
        run: |
          curl -fsSL -o /usr/local/bin/zarf \
            https://github.com/zarf-dev/zarf/releases/download/v0.82.0/zarf_v0.82.0_Linux_amd64
          curl -fsSL -o /usr/local/bin/uds \
            https://github.com/defenseunicorns/uds-cli/releases/download/v0.34.3/uds-cli_v0.34.3_Linux_amd64
          chmod +x /usr/local/bin/zarf /usr/local/bin/uds
      - uses: sigstore/cosign-installer@v3
      - uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: create zarf package + bundle
        run: |
          zarf package create gitea --confirm -o gitea
          uds create gitea/bundle --confirm -o .
      - name: publish bundle
        run: uds publish uds-bundle-gitea-*.tar.zst "oci://ghcr.io/jongodb-labs/bundles"
      - name: resolve published digest
        run: |
          DIGEST=$(zarf tools registry digest "$BUNDLE:$VERSION")
          echo "$DIGEST" | grep -Eq '^sha256:[a-f0-9]{64}$'
          echo "DIGEST=$DIGEST" >> "$GITHUB_ENV"
      - name: cosign sign (keyless)
        run: cosign sign --yes "$BUNDLE@$DIGEST"
      - name: verify-after-sign (fail-closed, self-healing)
        run: |
          for i in 1 2 3; do
            if cosign verify \
                 --certificate-identity-regexp '^https://github.com/JongoDB-Labs/sre-apps/' \
                 --certificate-oidc-issuer https://token.actions.githubusercontent.com \
                 "$BUNDLE@$DIGEST" >/dev/null 2>&1; then
              echo "verified on attempt $i"; exit 0
            fi
            echo "verify miss (attempt $i) — re-signing"
            cosign sign --yes "$BUNDLE@$DIGEST" || true
            sleep 10
          done
          echo "RELEASE UNVERIFIABLE — failing"; exit 1
      - name: SLSA provenance
        uses: actions/attest-build-provenance@v3
        with:
          subject-name: ghcr.io/jongodb-labs/bundles/gitea
          subject-digest: ${{ env.DIGEST }}
          push-to-registry: true
```

- [ ] **Step 2: Commit and push the workflow**

```bash
git add .github/workflows/release.yaml
git commit -m "ci: release — create/publish bundle, cosign keyless sign, verify-after-sign gate, SLSA provenance"
git push
```

- [ ] **Step 3: Tag the first release and watch it**

```bash
git tag gitea-v1.27.0-1
git push origin gitea-v1.27.0-1
gh run watch --exit-status $(gh run list --workflow release --limit 1 --json databaseId --jq '.[0].databaseId')
```
Expected: green through the verify gate. If `uds publish` 403s: GHCR first-publish permissions — re-check the `packages: write` permission block and that the org allows Actions to create packages.

- [ ] **Step 4: Manual follow-ups (record in the PR/issue)**

1. Flip GHCR package `jongodb-labs/bundles/gitea` to **public** (GitHub UI → package settings — no API exists).
2. From any machine: verify unauthenticated:
```bash
cosign verify \
  --certificate-identity-regexp '^https://github.com/JongoDB-Labs/sre-apps/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/jongodb-labs/bundles/gitea:1.27.0-1
```
Expected: verification succeeds, identity shows the sre-apps release workflow.

---

### Task 10: sre-v2 catalog entry + shipped-catalog test (slice S4)

**Files:**
- Modify: `catalog.yaml` (repo root)
- Modify: `installer/internal/appcatalog/shipped_catalog_test.go`

**Interfaces:**
- Consumes: the published, signed bundle (Task 9) and the hardened resolver (Task 1 — the entry's `version` becomes the resolved tag).

Branch: `git checkout -b feat/catalog-gitea` (from up-to-date `main`, after the S1 PR merges).

- [ ] **Step 1: Add the failing test**

Append to `installer/internal/appcatalog/shipped_catalog_test.go`:

```go
func TestShippedCatalog_GiteaIsEntryTwo(t *testing.T) {
	c, err := Load(shippedCatalogPath())
	if err != nil {
		t.Fatalf("the shipped catalog.yaml must load + validate: %v", err)
	}
	if len(c.Apps) < 2 || c.Apps[1].Name != "gitea" {
		t.Fatalf("gitea must be catalog entry #2 (cosmos stays #1), got %+v", c.Apps)
	}
	gitea := c.Apps[1]
	if gitea.Version == "" {
		t.Error("gitea must pin a version — the resolver uses it as the OCI tag")
	}
	if gitea.Source.Type != SourceOCI || gitea.Source.Ref != "ghcr.io/jongodb-labs/bundles/gitea" {
		t.Errorf("gitea source = %+v, want oci ghcr.io/jongodb-labs/bundles/gitea", gitea.Source)
	}
	// The SECOND signer identity — proves multi-identity verify is generic.
	if gitea.Verify.IdentityRegexp != "^https://github.com/JongoDB-Labs/sre-apps/" ||
		gitea.Verify.Issuer != "https://token.actions.githubusercontent.com" {
		t.Errorf("gitea verify = %+v, want the sre-apps keyless identity", gitea.Verify)
	}
	if len(gitea.Requires) != 2 || gitea.Requires[0] != "pgo" || gitea.Requires[1] != "minio" {
		t.Errorf("gitea must require [pgo minio], got %v", gitea.Requires)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd installer && go test ./internal/appcatalog/ -run TestShippedCatalog -v`
Expected: `TestShippedCatalog_GiteaIsEntryTwo` FAILS ("gitea must be catalog entry #2").

- [ ] **Step 3: Add the catalog entry**

Append to `catalog.yaml` (after the cosmos entry, same indentation):

```yaml
  - name: gitea
    version: "1.27.0-1"
    description: "Gitea — self-hosted git forge (mission app #2, first OSS onboarding)."
    source:
      type: oci
      ref: ghcr.io/jongodb-labs/bundles/gitea
    verify:
      identityRegexp: "^https://github.com/JongoDB-Labs/sre-apps/"
      issuer: "https://token.actions.githubusercontent.com"
    requires: [pgo, minio]
```

- [ ] **Step 4: Run the full suite**

Run: `cd installer && go test ./...`
Expected: ALL PASS (including the cosmos entry-#1 test, untouched).

- [ ] **Step 5: Commit, push, open the S4 PR**

```bash
git add catalog.yaml installer/internal/appcatalog/shipped_catalog_test.go
git commit -m "feat(catalog): gitea entry #2 — second keyless signer identity (sre-apps)"
git push -u origin feat/catalog-gitea
gh pr create --title "feat(catalog): gitea as entry #2 (sre-apps signer identity)" \
  --body "S4 of docs/specs/gitea-onboarding-design.md §5.1. Points at the published ghcr.io/jongodb-labs/bundles/gitea:1.27.0-1 (signed + verified in sre-apps CI)."
```

---

### Task 11: Operator Package CRs — LAB-GATED (slice S5)

**Files:**
- Create: `packages/pgo/uds-package.yaml`, `packages/minio/uds-package.yaml`
- Modify: `packages/pgo/zarf.yaml`, `packages/minio/zarf.yaml` (add `manifests:` entries)

**Interfaces:**
- Produces: live UDS `Package` CRs named `pgo` (ns `postgres-operator`) and `minio` (ns `minio-operator`) — the names the `requires` preflight matches, making `requires: [pgo, minio]` truthful.

**⚠️ Precondition: the lab is reachable and healthy** (`ssh cosmos-ssh 'kubectl get nodes'` → Ready). This slice changes live network posture for running operators — **lab-first, bake-second**. If anything degrades, `kubectl delete package <name> -n <ns>` is the rollback.

- [ ] **Step 1: Write the two CR manifests**

`packages/pgo/uds-package.yaml`:
```yaml
# Makes `requires: [pgo]` truthful (the app-catalog preflight matches live
# Package CR names) AND brings the operator under default-deny.
apiVersion: uds.dev/v1alpha1
kind: Package
metadata:
  name: pgo
  namespace: postgres-operator
spec:
  network:
    allow:
      - direction: Egress
        remoteGenerated: KubeAPI
        description: "operator watch/reconcile + Patroni DCS management"
      - direction: Egress
        remoteGenerated: IntraNamespace
      - direction: Ingress
        remoteGenerated: IntraNamespace
```

`packages/minio/uds-package.yaml`:
```yaml
apiVersion: uds.dev/v1alpha1
kind: Package
metadata:
  name: minio
  namespace: minio-operator
spec:
  network:
    allow:
      - direction: Egress
        remoteGenerated: KubeAPI
        description: "operator watch/reconcile of Tenants"
      - direction: Egress
        remoteGenerated: IntraNamespace
      - direction: Ingress
        remoteGenerated: IntraNamespace
      # The operator reaches tenant pods in app namespaces (sidecar/config
      # sync); app-side ingress is declared by each app's Package. Egress
      # from HERE to those namespaces:
      - direction: Egress
        remoteNamespace: "*"
        description: "operator -> tenant pods in app namespaces (verify + tighten on the lab)"
```

- [ ] **Step 2: LAB — record the before-state**

```bash
ssh cosmos-ssh 'kubectl get netpol -n postgres-operator; kubectl get netpol -n minio-operator; kubectl -n postgres-operator get pods; kubectl -n minio-operator get pods'
```
Save the output into the PR description later.

- [ ] **Step 3: LAB — apply live and observe**

```bash
scp packages/pgo/uds-package.yaml cosmos-ssh:/tmp/pgo-pkg.yaml
scp packages/minio/uds-package.yaml cosmos-ssh:/tmp/minio-pkg.yaml
ssh cosmos-ssh 'kubectl apply -f /tmp/pgo-pkg.yaml -f /tmp/minio-pkg.yaml'
# wait 60s, then:
ssh cosmos-ssh '
kubectl get packages -A
kubectl get netpol -n postgres-operator
kubectl -n postgres-operator logs deploy/pgo --since=2m | tail -20
kubectl -n minio-operator get pods
kubectl -n cosmos get pods    # the running cosmos PG must stay healthy
'
```
Expected: Package CRs Ready; operator pods stay Running; **no new errors** in the PGO log. Then exercise Patroni: `ssh cosmos-ssh 'kubectl -n cosmos delete pod -l postgres-operator.crunchydata.com/role=master'` and confirm the primary comes back healthy (`kubectl -n cosmos get pods -w`, ~2 min). If anything breaks: `kubectl delete package pgo -n postgres-operator` (and/or `minio`), diagnose, adjust the allow rules, re-apply.

- [ ] **Step 4: Verify the preflight warning disappears**

```bash
cd installer && go build -o /tmp/srectl ./cmd/srectl
ssh cosmos-ssh 'kubectl get packages -A -o json' >/dev/null  # sanity: CRs visible
# From a kubeconfig-bearing context (or on the lab VM with srectl copied over):
/tmp/srectl app status cosmos   # must NOT print the missing-require warning path
```

- [ ] **Step 5: Bake into the zarf packages**

In `packages/pgo/zarf.yaml`, add to the single component (after its `charts:` block, same indentation level):
```yaml
    manifests:
      - name: pgo-uds-package
        namespace: postgres-operator
        files:
          - uds-package.yaml
```
In `packages/minio/zarf.yaml`, equivalently:
```yaml
    manifests:
      - name: minio-uds-package
        namespace: minio-operator
        files:
          - uds-package.yaml
```
(Read each zarf.yaml first and match its exact component indentation.)

- [ ] **Step 6: Commit, push, PR (with the lab before/after evidence)**

```bash
git checkout -b feat/operator-package-crs
git add packages/pgo packages/minio
git commit -m "feat(packages): UDS Package CRs for pgo + minio operators

Makes the app-catalog requires preflight truthful (it matches live Package
CR names) and brings both operators under default-deny. Lab-verified:
netpols observed, PGO reconcile + Patroni failover exercised post-apply."
git push -u origin feat/operator-package-crs
gh pr create --title "feat(packages): operator Package CRs (requires truthfulness + default-deny)" \
  --body "S5 of docs/specs/gitea-onboarding-design.md §5.3. Lab before/after evidence: <paste step 2/3 output>"
```

---

### Task 12: Lab acceptance + docs (slice S6)

**Files:**
- Modify: `docs/app-onboarding.md` (second worked example)
- Modify: `docs/platform-runbook.md` (new gotchas found during acceptance)
- Modify: `installer/internal/catalog/catalog.yaml` (clear the `minio: status pending` drift)
- Create: `docs/product-lineup.md`

**Preconditions:** lab healthy; S1–S5 merged; bundle published + public.

- [ ] **Step 1: Pre-create namespace + out-of-band secrets on the lab**

```bash
ssh cosmos-ssh '
kubectl create ns gitea 2>/dev/null || true
kubectl -n gitea create secret generic gitea-admin-creds \
  --from-literal=username=gitea_admin \
  --from-literal=password="$(openssl rand -base64 18)"
ROOTPW=$(openssl rand -base64 24)
printf "export MINIO_ROOT_USER=gitea-minio-root\nexport MINIO_ROOT_PASSWORD=%s\n" "$ROOTPW" > /tmp/config.env
kubectl -n gitea create secret generic gitea-minio-creds --from-file=config.env=/tmp/config.env
rm /tmp/config.env
kubectl -n gitea create secret generic gitea-minio-app-creds \
  --from-literal=accessKey=gitea-app \
  --from-literal=secretKey="$(openssl rand -base64 24)"
'
```

- [ ] **Step 2: Run the acceptance sequence (spec §7)**

On a host with kubeconfig + `uds`/`zarf`/`cosign` (the lab VM):
1. `srectl app list` → gitea appears as entry #2.
2. `srectl app install gitea` → expect in order: `resolved gitea → ghcr.io/jongodb-labs/bundles/gitea@sha256:…`, `signature verified`, no advisory warnings, `deployed gitea`, `recorded install of gitea 1.27.0-1`.
3. Cohesion checks:
```bash
kubectl get virtualservice -n gitea                          # exists, host gitea.uds.dev
kubectl -n gitea get secret gitea-oidc-client -o jsonpath='{.data}' | grep -q key   # keys key/secret
kubectl -n gitea get postgrescluster gitea-pg                # reconciled
kubectl -n gitea get tenant gitea-minio                      # green
kubectl -n gitea get servicemonitor                          # exists
kubectl -n gitea get pods                                    # gitea Running (may take minutes: PG init + migrate)
```
4. Functional: browse `https://gitea.uds.dev` → "Sign in with keycloak" → log in as the Keycloak test user (seed one per `docs/app-onboarding.md` §2 if needed) → auto-registered. Create a repo, push over HTTPS, upload an attachment; then verify the object landed:
```bash
kubectl -n gitea exec deploy/gitea -c gitea -- ls /data || true   # repos on the PVC
# attachment in the bucket:
kubectl -n gitea run mc --rm -it --image=$(grep mcImage -A0 … ) — simpler: port-forward gitea-minio-hl 9000 and use mc from the VM
```
5. `srectl app status gitea` → installed, `live UDS Package: true`, no drift.
6. Record every deviation/gotcha encountered — each becomes a runbook entry in Step 4.

- [ ] **Step 3: Fail-closed check**

Temporarily edit the lab's catalog copy to `identityRegexp: "^https://github.com/WRONG/"` and re-run `srectl app install gitea` → MUST abort with the expected-identity message before `uds deploy`. Restore the catalog.

- [ ] **Step 4: Write the docs**

1. `docs/app-onboarding.md`: add a `## Worked example #2 — Gitea (OSS app)` section after the round-2 acceptance section, documenting exactly what was DIFFERENT from cosmos: declarative `Package.sso` + `secretConfig.template` (quote the working YAML from `sre-apps/gitea/chart/templates/uds-package.yaml`), the pguser-secret Postgres pattern + `PGSSLROOTCERT`, the Tenant-CR + bootstrap-Job object-storage pattern, the scoped SSO egress rule, the `monitor` seam, and the two-lane supply chain note (see 3.). Cite `JongoDB-Labs/sre-apps` as the reference implementation.
2. `docs/platform-runbook.md`: append the acceptance-run gotchas (numbered continuing the existing catalog; expected candidates: operator↔tenant netpols, oauth-secret race at first install, PG init ordering).
3. `docs/product-lineup.md` (new): the family map — AEGIS umbrella; factory side (CRUCIBLE pipeline, COLOSSUS registry, TABULARIUM evidence store, VIGIL maintenance, LIMES reserved) on the AEGIS VM; runtime side (SRE substrate + srectl + app-catalog, cosmos + gitea as tenants); the **two-lane trust model** (open/dev lane: keyless + GHCR + public Rekor — cosmos/gitea today; gov lane: CRUCIBLE offline key-based + COLOSSUS — CUI-blind), noting the catalog's authority is the registry-level cosign signature and that a COLOSSUS/key-based catalog lane is a follow-on; the VIGIL→P6 seam (P6 remains design-gated); admission-time verification as a named substrate gap.
4. `installer/internal/catalog/catalog.yaml`: change the minio entry's `status: pending` line to reflect that packages/minio ships in the bundle (read the file first; follow its existing status vocabulary).

- [ ] **Step 5: Commit, push, PR**

```bash
git checkout -b docs/gitea-acceptance
git add docs/ installer/internal/catalog/catalog.yaml
git commit -m "docs: Gitea worked example #2, acceptance gotchas, product lineup (two-lane supply chain)"
git push -u origin docs/gitea-acceptance
gh pr create --title "docs: Gitea onboarding acceptance + product lineup" \
  --body "S6 of docs/specs/gitea-onboarding-design.md §5.4/§7. Acceptance evidence: <paste key outputs>"
```

---

## Self-Review Notes

- Spec coverage: §3 → Tasks 3–7; §4 → Tasks 8–9; §5.1 → Task 10; §5.2 → Task 1; §5.3 → Task 11; §5.4 → Task 12; §6 testing → embedded per task; §7 acceptance → Task 12. The product-lineup doc extends §5.4 per the user's post-spec two-lane decision (2026-08-03).
- Type consistency: `source.Zarf.RegistryDigest([]byte, error)` is used identically in Task 1's exec.go, oci.go, and both test files; secret names (`gitea-oidc-client`, `gitea-pg-pguser-gitea`, `gitea-pg-cluster-cert`, `gitea-minio-creds`, `gitea-minio-app-creds`, `gitea-admin-creds`) match across Tasks 3–6 and 12; version `1.27.0-1` matches across Tasks 7, 9, 10.
- Known deliberate deferals (not placeholders): the mc image digest is resolved by command at Task 5 step 1 (procedure, not TBD); Task 11's minio egress rule is explicitly marked verify-and-tighten on the lab (that is the slice's purpose); Task 12 step 2.4's object-check offers the port-forward route because in-cluster mc invocation depends on the tenant service shape observed live.
