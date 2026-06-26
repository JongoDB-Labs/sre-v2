# App-catalog — Round-2 Deploy Layer

**Status:** Design — approved 2026-06-26
**Scope:** SRE substrate (`sre-v2`)
**Surfaces:** `srectl` CLI/TUI (MVP) · SP8 web console (later, same backend)
**Depends on:** the signed-bundle CI (`.github/workflows/release.yml`), the app-onboarding
contract (`docs/app-onboarding.md`), the round-1 installer skeleton (`installer/`).

---

## 1. Goal

Deploy **mission apps** onto a *running* SRE from **local / OCI / GitHub** sources, each
auto-wiring into the substrate's cohesion (ingress · SSO · data · netpol), **signature-
verified** and tracked for Day-2 — through two surfaces (`srectl` CLI/TUI and the SP8 web
console) over **one** Go backend, with **no deploy logic duplicated** across Go and TypeScript.

## 2. Context — round-1 vs round-2

| | Round 1 (exists) | Round 2 (this spec) |
|---|---|---|
| What | install the **substrate** (RKE2 + UDS Core + operators) | deploy **mission apps** onto it |
| Code | `installer/internal/catalog` — the *service* catalog (which Core layers/operators) | new `installer/internal/appcatalog` |
| Unit | UDS Core layers, PGO/MinIO operators | a signed **UDS Package** (Helm chart + `Package` CR) |

An "app" is exactly the **app-onboarding contract** (`docs/app-onboarding.md`): a signed
UDS Package whose `Package` CR auto-wires `expose`/`sso`/`allow` + brings its own data
instances against the shared operators. **cosmos is the reference app** and catalog entry #1.

> The round-1 `internal/catalog` and round-2 `internal/appcatalog` are deliberately separate
> packages — different domains (substrate services vs mission apps), no shared types.

## 3. Architecture — `srectl` is the shared backend

One Go implementation owns catalog resolution, signature verification, deploy orchestration
(**shells out to `uds`/`zarf` — never reimplements them**), and installed-app state. Two
surfaces from the same code:

```
 operator ── srectl app … (CLI/TUI) ──┐
                                       ├── internal/appcatalog ── uds/zarf ── UDS Operator ── cohesion
 SP8 console (cosmos/TS) ── HTTP ──────┘            (later: srectl serve)
```

- **CLI/TUI** (MVP): `srectl app …` — round-2 Day-2 on top of the round-1 install.
- **Service** (deferred): `srectl serve` exposes the same `appcatalog` over a small HTTP API;
  the SP8 console (cosmos, TypeScript) calls it. One codebase, two modes.

*Rejected alternative:* a standalone catalog **service** as the primary backend — more moving
parts (its own deploy, RBAC, lifecycle) for the same outcome. `srectl serve` gives the service
shape later without a second codebase.

## 4. The catalog model

A `catalog.yaml` lists app entries; **source adapters** resolve each entry to a deployable,
verifiable package ref.

```yaml
# catalog.yaml
apiVersion: sre/v1
apps:
  - name: cosmos
    version: "2.102.0"
    description: "COSMOS — mission app (PM/CRM/…)"
    source:
      type: oci                         # local | oci | github
      ref: ghcr.io/jongodb-labs/bundles/cosmos
    verify:                             # expected signer identity (cosign keyless)
      identityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/"
      issuer: "https://token.actions.githubusercontent.com"
    requires: [postgres]                # substrate services it needs (preflight hint)
```

**Source adapters** (`internal/appcatalog/source`):

| type | resolves | airgap |
|---|---|---|
| `local` | a dir or `*.tar.zst` on disk | ✅ |
| `oci` | a UDS bundle / Zarf package in a registry (by tag → digest) | ✅ (in-cluster/airgap registry) |
| `github` | a release asset on a repo | ❌ connected-only |

The adapter interface: `Resolve(entry) → (ref string, digest string, err)`. `oci`/`local`
are MVP; `github` is deferred (its only job is fetch-then-hand-to-`local`/`oci`).

## 5. Deploy flow — `srectl app install <name>`

1. **Resolve** — load `catalog.yaml`, find the entry, run its source adapter → `(ref, digest)`.
2. **Verify** — `cosign verify <ref>@<digest>` against `entry.verify.{identityRegexp,issuer}`.
   **Fail-closed:** no valid signature → abort, never deploy.
3. **Preflight cohesion** *(advisory)* — scan the package manifests (`zarf package inspect`)
   for a UDS `Package` CR and warn if absent (the app won't wire cohesion); warn too if a
   `requires` service is missing (e.g. needs `postgres` but PGO isn't installed). Advisory only —
   the deploy proceeds; **step 6 is the authoritative post-deploy cohesion check.**
4. **Deploy** — `uds deploy <ref> --confirm`. The UDS Operator reconciles the `Package` CR →
   ingress (shared gateway) + SSO client + netpol wired automatically.
5. **Record state** — write/extend the install record (§6).
6. **Confirm** — wait for the app's workloads Ready + the cohesion artifacts (the
   VirtualService; the `keycloak-client-secrets` entry if `sso` was declared); report.

Errors at any step abort with a clear message; a failed step-4 deploy is cleaned up with
`uds remove` (best-effort) so a half-wired app isn't left behind.

## 6. State model

Canonical install record = a ConfigMap `sre-appcatalog-installs` in the substrate system
namespace (`sre-system`, which `srectl` ensures exists), created on first install — each app a key:

```yaml
cosmos: { version: "2.102.0", source: "oci:ghcr.io/jongodb-labs/bundles/cosmos",
          digest: "sha256:…", installedAt: "2026-…Z", installedBy: "<oidc-sub>" }
```

`app list --installed` cross-checks this record against the **live UDS Packages**
(`kubectl get packages -A`) so drift (a Package present without a record, or vice-versa) is
visible — the record is convenience metadata, the cluster is the source of truth.

## 7. Day-2 (re-entrant; both surfaces)

- `app list [--installed]` · `app status <name>` (workloads + cohesion health) ·
  `app remove <name>` (`uds remove` + prune the record) ·
  `app update <name> --to <ver>` *(deferred — ties into SP8 update-orchestration: signed
  approval → actuate)*.

## 8. Security

- **Signature verification is mandatory + fail-closed** (§5.2) — gov-grade supply chain;
  pairs with the producer-side signing in `release.yml`.
- **RBAC:** app deploy/remove is a platform-admin action — gated by the substrate Keycloak
  (the `srectl serve` API authenticates against `sso.<domain>`; the CLI relies on cluster
  RBAC/kubeconfig).
- **Audit:** every install/remove/update is recorded (the install record + emitted to the
  cluster audit; via `srectl serve`, into cosmos's audit hash-chain per SP8).
- **Airgap:** `oci` + `local` adapters work fully disconnected; `github` is connected-only.

## 9. File structure

```
installer/
  cmd/srectl/app.go              # cobra `app` parent + list/install/remove/status subcommands
  internal/appcatalog/
    catalog.go                   # load + validate catalog.yaml; Entry type
    source/source.go             # Adapter interface: Resolve(Entry) → (ref,digest)
    source/local.go              # local dir/tarball adapter
    source/oci.go                # OCI registry adapter (tag → digest)
    source/github.go             # DEFERRED — release-asset adapter (→ delegates to local/oci)
    verify.go                    # cosign verify wrapper (fail-closed)
    preflight.go                 # cohesion-CR + requires check (uds inspect)
    deploy.go                    # uds deploy / uds remove orchestration
    state.go                     # the sre-appcatalog-installs ConfigMap record
  cmd/srectl/serve.go            # DEFERRED — `srectl serve` HTTP API over internal/appcatalog
catalog.yaml                     # the shipped default catalog (cosmos as entry #1)
```

Each file has one responsibility; `internal/appcatalog` is consumed identically by the CLI
commands and (later) `serve` — that's what makes it the shared backend.

## 10. Error handling

| Failure | Behavior |
|---|---|
| catalog.yaml missing/invalid | clear validation error, exit non-zero |
| source unreachable | error; `github` unreachable in airgap is expected, message says so |
| signature invalid/absent | **abort before deploy** (fail-closed), name the expected identity |
| `requires` service absent | preflight **warns** (install proceeds) — the app may degrade |
| `uds deploy` fails | surface uds/zarf output; best-effort `uds remove`; record NOT written |
| record/cluster drift | surfaced by `list --installed`, never silently reconciled |

## 11. Testing

- **Unit:** catalog load/validate; `local`+`oci` adapters (oci against a local registry
  fixture); `verify` (cosign stubbed for pass + fail paths); `state` ConfigMap read/write;
  preflight (manifest with/without a `Package` CR).
- **Acceptance (dogfood):** re-deploy **cosmos through the catalog** onto the running SRE —
  `srectl app install cosmos` → verify passes → `uds deploy` → the VirtualService +
  (if declared) the SSO client appear → the install record is written → `app status cosmos`
  green. This is the round-2 analog of the SSO end-to-end proof, and the bar for "MVP done."

## 12. MVP scope

**Build now:** `srectl app {list,install,remove,status}` · `catalog.yaml` · `local`+`oci`
adapters · cosign-verify · cohesion preflight · `uds deploy`/`remove` · the install-record
ConfigMap · cosmos as catalog entry #1 + the dogfood acceptance.

**Defer:** `github` adapter · `srectl serve` + the SP8 console surface (lands with the SP8
console build) · `app update` (lands with SP8 update-orchestration).

## 13. Future

- `srectl serve` + the SP8 console GUI over the same `internal/appcatalog`.
- Multi-app bundles / dependency ordering (app A needs app B).
- A hosted/online app store as a `source` adapter.
- Per-org app entitlements (which orgs may install which apps) — ties to cosmos entitlements.
