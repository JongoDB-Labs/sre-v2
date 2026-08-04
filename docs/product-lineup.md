# Product lineup — the AEGIS family and the two-lane trust model

> **Status:** living map, extending `docs/specs/gitea-onboarding-design.md` §5.4 per the
> user's post-spec two-lane decision (2026-08-03). This is **not** a design doc for any of
> the factory-side products — those live in their own (currently unaffiliated) planning
> repo — it exists so anyone reading `sre-v2` understands **where this repo sits** in the
> larger picture, and what its catalog's signature verification does and doesn't cover.

## The family

**AEGIS** is the umbrella paved-road platform: a self-hosted, all-OSS DevSecOps factory
that produces continuously-hardened, signed, attested container images and the compliance
evidence to authorize them — plus the secure runtime that consumes them. It splits into
two sides that run on **separate hosts**:

| Side | Runs on | Products |
|---|---|---|
| **Factory** | the **AEGIS VM** | **CRUCIBLE** (pipeline), **COLOSSUS** (registry), **TABULARIUM** (evidence store), **VIGIL** (maintenance loop), **LIMES** *(reserved)* |
| **Runtime** | the SRE substrate (RKE2 lab today, `cosmos-k8s`) | **`sre-v2`** (this repo) — the substrate + `srectl` + the app-catalog; **cosmos** and **gitea** as tenant mission apps |

### Factory side (the AEGIS VM)

| Product | What it is |
|---|---|
| **CRUCIBLE** | The DSOP pipeline — the reusable gauntlet of security gates (lint/render, SBOM, vuln scan, SCAP/STIG, sign, provenance, verify-after-sign) every onboarded repo's CI runs. `sre-apps`'s `release.yaml` (publish → cosign sign → verify-after-sign gate → SLSA provenance) is exactly this shape today, running standalone rather than as a CRUCIBLE-templated call — the seed CRUCIBLE will eventually generalize. |
| **COLOSSUS** | The self-hosted OCI registry (scan-on-push, cosign-verify policy, CVE gating, tag immutability) — the gov-lane counterpart to GHCR. Not used by `sre-v2`'s catalog today; both `cosmos` and `gitea` publish to GHCR. |
| **TABULARIUM** | The continuously-updated evidence store — SBOMs, provenance attestations, vuln/SCAP/VEX reports, archived and mapped into AO-ingestible formats (OSCAL, `.ckl`, SARIF, SPDX/CycloneDX) for IATT → ATO → cATO visibility. Nothing in `sre-v2` writes into it yet; the closest analog today is each app's own signed-artifact set on GHCR (SBOM/provenance attached to the published bundle) plus this SDD plan's own acceptance reports. |
| **VIGIL** | The continuous-maintenance loop — scheduled rebuild-on-CVE, base-image currency SLAs, re-scan on new-CVE disclosure, auto-PRs to consumers when a base image moves. See "the VIGIL→P6 seam" below — this is the piece that would *trigger* a substrate or app update, not the piece that performs one. |
| **LIMES** *(reserved)* | The CUI-blind egress chokepoint, extracted from an app's boundary as its own service when a real gov deployment needs one. Not in scope for any current build; named and reserved only. |

### Runtime side (this repo)

`sre-v2` is the **destination**, not the factory: it deploys already-signed artifacts onto
a running secure substrate (UDS Core + PGO + MinIO-operator), via `srectl` and the
app-catalog. It does not build, scan, or sign anything itself — it *verifies* what the
factory (or, today, plain GitHub Actions standing in for CRUCIBLE) already signed, then
deploys it. **cosmos** (mission app #1) and **gitea** (mission app #2, `docs/app-onboarding.md`
worked example #2) are the two tenant apps proven through this path so far.

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
  past a single hardcoded identity). Evidence: both entries' fail-closed check was
  exercised live — a tampered `identityRegexp` aborts before `uds deploy` ever runs
  (`.superpowers/sdd/gitea-onboarding-plan/task-12a-report.md` Step 3b).
- **The catalog's authority is the registry-level cosign signature, full stop.** `uds`/
  `zarf` carry their own, separate internal key-based signing mechanism for bundle
  integrity in transit; the catalog's `CheckSignature` does not touch it and `uds deploy`
  does not check the registry-level cosign signature itself — the two are independent
  layers, and only the cosign one is what gates a catalog install
  (`docs/specs/gitea-onboarding-design.md` §9). Don't assume double coverage.

### Gov lane — not yet wired into the catalog

- **Signing:** CRUCIBLE, offline key-based (a held signing key, not OIDC-keyless) —
  the shape a disconnected/CUI-bounded pipeline requires, since Fulcio/Rekor need live
  internet access to issue and log against.
- **Registry:** COLOSSUS — CUI-blind, self-hosted, scan-on-push + cosign-verify + CVE
  gating enforced at the registry itself, not just at deploy time.
- **In use today:** nothing in `sre-v2`. Neither `cosmos` nor `gitea` publishes to
  COLOSSUS, and `catalog.yaml`'s `verify` block has no key-based (non-OIDC) verification
  path implemented — `CheckSignature`'s keyless-cosign-verify call is the only mode that
  exists.
- **A COLOSSUS-backed catalog lane — a `source.type` resolving from COLOSSUS with
  key-based verification instead of keyless GHCR — is a named follow-on, not built.**
  The per-entry `verify` shape in `catalog.yaml` (identity/issuer today) would need a
  key-based sibling; the OCI resolver (`installer/internal/appcatalog/source/oci.go`)
  would need a COLOSSUS-aware source type. Neither exists yet.

## The VIGIL → P6 seam

**VIGIL** (factory side) is the natural *trigger* for an update: it rebuilds on new CVE
disclosure and knows when a base image or app artifact has moved. **P6**
(`docs/specs/update-orchestration-design.md`, the `srectl` Day-2 update-orchestration
phase) is the *mechanism* that would consume such a trigger — a signed `UpdateApproval`
CRD, verified fail-closed (signature → authorization → supply-chain → floor → freshness),
actuated only by an out-of-app controller, with a mandatory backup-first/health-gate/
rollback-on-failure safety wrapper. **The two are not connected today.** P6 is
**design-gated**: fully specced (`update-orchestration-design.md` §§1–7), but explicitly
not built — "nothing here mutates installed versions outside the catalog's install path"
(`docs/specs/gitea-onboarding-design.md` §10, restating the HANDOFF guardrail). No
automation currently lets a VIGIL rebuild-on-CVE event reach `srectl`, the catalog, or any
running substrate/app version. Wiring that seam is future work, gated on P6 actually being
built (§7 of its design: the substrate path — `ZarfUdsActuator` — is buildable first,
since `srectl` and the app-catalog primitives it reuses already exist).

## Named substrate gap — no admission-time verification

Every verification `sre-v2` performs today happens **at install time**, inside `srectl`
(`CheckSignature` before `uds deploy`). Nothing verifies **at admission time** — there is
no cluster-level policy (Kyverno, Sigstore `policy-controller`, or equivalent) enforcing
"only cosign-verified images may run" as an admission-webhook gate on the cluster itself.
Concretely: a pod applied by any path that bypasses `srectl app install` (a raw
`kubectl apply`, a `helm install` run by hand, a compromised or buggy future automation)
is not blocked by anything at the Kubernetes API layer — `srectl`'s fail-closed check only
guards its own code path. This is a real gap relative to the factory side's own stated
posture (a cosign-verify admission policy shipped as portable Kyverno/Sigstore-controller
bundles "a consumer enforces on *any* cluster") — `sre-v2` has not adopted that piece.
Closing it is not scoped to any task in the Gitea onboarding plan; it's recorded here so
it isn't silently assumed solved by the install-time check that does exist.
