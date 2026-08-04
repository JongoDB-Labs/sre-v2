# Product lineup — the ARCIS family and the two-lane trust model

> **Status:** living map. Naming follows [`docs/naming.md`](naming.md) (the ARCIS
> convention — adopted 2026-08-04, superseding the interim "AEGIS family" framing).
> This is **not** a design doc for any of the factory-side products — those live in the
> factory repo — it exists so anyone reading this repo understands **where it sits** in
> the larger picture, and what its catalog's signature verification does and doesn't cover.

## The family

**ARCIS** is the platform family: a self-hosted, all-OSS DevSecOps *factory* that
produces continuously-hardened, signed, attested container images and the compliance
evidence to authorize them — plus the secure *runtime* that consumes them. The two
planes run on **separate hosts**, deliberately:

| Plane | Runs on | Members |
|---|---|---|
| **Factory** | the FABRICA VM (formerly "AEGIS VM") | **ARCIS FABRICA** (the suite/repo), housing **ARCIS PROBATIO** (pipeline), **ARCIS HORREUM** (registry), **ARCIS TABULA** (evidence store), **ARCIS VIGILES** (maintenance loop) |
| **Runtime** | the substrate cluster (RKE2 lab today, `cosmos-k8s`) | **ARCIS AEGIS** (this repo — the SRE substrate + `srectl` + the app-catalog); **cosmos** and **gitea** as tenant mission apps (out-of-family by rule) |

Legacy names appear throughout the factory repo's history and scripts
(CRUCIBLE → PROBATIO, COLOSSUS/COLOSSEUM → HORREUM, TABULARIUM → TABULA,
VIGIL → VIGILES); see `docs/naming.md` § Retired. Code paths, namespaces, and
script filenames rename lazily.

### Factory plane (ARCIS FABRICA)

| Member | What it is |
|---|---|
| **ARCIS PROBATIO** | The DSOP pipeline — the vetting examination (lint/render, SBOM, vuln scan, SCAP/STIG, sign, provenance, verify-after-sign) every onboarded repo's CI runs. `sre-apps`'s `release.yaml` (publish → cosign sign → verify-after-sign gate → SLSA provenance) is exactly this shape today, running standalone rather than as a PROBATIO-templated call — the seed PROBATIO will eventually generalize. |
| **ARCIS HORREUM** | The self-hosted OCI registry (Harbor: scan-on-push, cosign-verify policy, CVE gating, tag immutability) — the gov-lane counterpart to GHCR. Not used by this repo's catalog today; both `cosmos` and `gitea` publish to GHCR. |
| **ARCIS TABULA** | The continuously-updated evidence store — a WORM archive with a hash-chained ledger; SBOMs, provenance attestations, vuln/SCAP/VEX reports, mapped into AO-ingestible formats (OSCAL, `.ckl`, SARIF, SPDX/CycloneDX) for IATT → ATO → cATO visibility. Nothing in this repo writes into it yet; the closest analog today is each app's own signed-artifact set on GHCR plus acceptance evidence. |
| **ARCIS VIGILES** | The continuous-maintenance loop — scheduled rebuild-on-CVE, base-image currency SLAs, re-scan on new-CVE disclosure, auto-PRs to consumers when a base image moves. See "the VIGILES → P6 seam" below — this is the piece that would *trigger* a substrate or app update, not the piece that performs one. |
| **ARCIS LIMES** *(reserved)* | The CUI-blind egress chokepoint, extracted from an app's boundary as its own service when a real gov deployment needs one. Named and reserved only. |
| Hardened base images | A FABRICA deliverable, deliberately unbranded (UBI9 / distroless / Wolfi / node lines, built by PROBATIO, stored in HORREUM, kept current by VIGILES). |

### Runtime plane (ARCIS AEGIS — this repo)

ARCIS AEGIS is the **destination**, not the factory: it deploys already-signed artifacts
onto a running secure substrate (UDS Core + PGO + MinIO-operator), via `srectl` and the
app-catalog. It does not build, scan, or sign anything itself — it *verifies* what the
factory (or, today, plain GitHub Actions standing in for PROBATIO) already signed, then
deploys it. **cosmos** (mission app #1) and **gitea** (mission app #2,
`docs/app-onboarding.md` worked example #2) are the two tenant apps proven through this
path so far.

## The two-lane trust model

The catalog's signature verification (`installer/internal/appcatalog/verify.go`,
`CheckSignature`) is **lane-agnostic by construction** — it verifies a cosign signature
against a per-entry `identityRegexp`/`issuer` pair from `catalog.yaml`. What differs
between the two lanes is *how* that signature was produced and *where* the artifact lives,
not the verification code path itself.

### Open/dev lane — live today

- **Signing:** keyless (GitHub OIDC → Fulcio short-lived cert → Rekor transparency log),
  no held private key.
- **Registry:** GHCR (`ghcr.io/jongodb-labs/bundles/...`), public.
- **Transparency:** public Rekor — anyone can independently verify the signing event
  occurred, without trusting the publisher's infrastructure.
- **In use today:** both catalog entries —
  `cosmos` (`identityRegexp: ^https://github.com/JongoDB-Labs/cosmos-v2/`) and
  `gitea` (`identityRegexp: ^https://github.com/JongoDB-Labs/sre-apps/`, a **second,
  independent signer identity** — proof the catalog's per-entry verification generalizes
  past a single hardcoded identity). Both entries' fail-closed check was exercised live —
  a tampered `identityRegexp` aborts before `uds deploy` ever runs.
- **The catalog's authority is the registry-level cosign signature, full stop.** `uds`/
  `zarf` carry their own, separate internal key-based signing mechanism for bundle
  integrity in transit; the catalog's `CheckSignature` does not touch it and `uds deploy`
  does not check the registry-level cosign signature itself — the two are independent
  layers, and only the cosign one gates a catalog install
  (`docs/specs/gitea-onboarding-design.md` §9). Don't assume double coverage.

### Gov lane — not yet wired into the catalog

- **Signing:** PROBATIO, offline key-based (a held signing key, not OIDC-keyless) —
  the shape a disconnected/CUI-bounded pipeline requires, since Fulcio/Rekor need live
  internet access to issue and log against.
- **Registry:** HORREUM — CUI-blind, self-hosted, scan-on-push + cosign-verify + CVE
  gating enforced at the registry itself, not just at deploy time.
- **In use today:** nothing in this repo. Neither `cosmos` nor `gitea` publishes to
  HORREUM, and `catalog.yaml`'s `verify` block has no key-based (non-OIDC) verification
  path implemented — `CheckSignature`'s keyless-cosign-verify call is the only mode that
  exists.
- **A HORREUM-backed catalog lane — a `source.type` resolving from HORREUM with
  key-based verification instead of keyless GHCR — is a named follow-on, not built.**
  The per-entry `verify` shape in `catalog.yaml` (identity/issuer today) would need a
  key-based sibling; the OCI resolver (`installer/internal/appcatalog/source/oci.go`)
  would need a HORREUM-aware source type. Neither exists yet. When PROBATIO's signing
  stage is extracted as the shared workflow spanning both lanes, the pool name
  **SACRAMENTUM** is its natural claim (`docs/naming.md`).

## The VIGILES → P6 seam

**ARCIS VIGILES** (factory plane) is the natural *trigger* for an update: it rebuilds on
new CVE disclosure and knows when a base image or app artifact has moved. **P6**
(`docs/specs/update-orchestration-design.md`, the `srectl` Day-2 update-orchestration
phase) is the *mechanism* that would consume such a trigger — a signed `UpdateApproval`
CRD, verified fail-closed (signature → authorization → supply-chain → floor → freshness),
actuated only by an out-of-app controller, with a mandatory backup-first/health-gate/
rollback-on-failure safety wrapper. **The two are not connected today.** P6 is
**design-gated**: fully specced, explicitly not built — "nothing here mutates installed
versions outside the catalog's install path" (`docs/specs/gitea-onboarding-design.md`
§10, restating the HANDOFF guardrail). No automation currently lets a VIGILES
rebuild-on-CVE event reach `srectl`, the catalog, or any running substrate/app version.
Wiring that seam is future work, gated on P6 actually being built.

## Named substrate gap — no admission-time verification

Every verification this repo performs today happens **at install time**, inside `srectl`
(`CheckSignature` before `uds deploy`). Nothing verifies **at admission time** — there is
no cluster-level policy (Kyverno, Sigstore `policy-controller`, or equivalent) enforcing
"only cosign-verified images may run" as an admission-webhook gate on the cluster itself.
Concretely: a pod applied by any path that bypasses `srectl app install` (a raw
`kubectl apply`, a `helm install` run by hand, a compromised or buggy future automation)
is not blocked by anything at the Kubernetes API layer — `srectl`'s fail-closed check only
guards its own code path. This is a real gap relative to the factory plane's own stated
posture (a cosign-verify admission policy shipped as portable Kyverno/Sigstore-controller
bundles a consumer enforces on *any* cluster) — the runtime has not adopted that piece.
It's recorded here so it isn't silently assumed solved by the install-time check that
does exist.
