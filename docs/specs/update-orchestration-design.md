<!-- Ported 2026-08-03 from pontis/docs/specs/2026-06-24-update-orchestration-design.md.
     pontis is local-only (no git remote); this design is rescued into the repo for
     portability. Cross-refs to SP8 / north-star are historical pontis design docs, now
     superseded by the merged srectl implementation — this design stands on its own. The
     srectl-day2-console-design.md and app-catalog-round2-design.md refs ARE in this repo
     (docs/specs/). -->

# Update Orchestration — Signed-Approval Control Plane

> **Status:** design (standalone). Extracted from **SP8 §3** and reconciled with the substrate reality on **2026-07-01** (two-layer SRE/cosmos split, the merged app-catalog, srectl).
> **Elaborates:** north-star §5 ("Updates"), `2026-06-24-k8s-migration-north-star-design.md`.
> **Sibling specs:** `2026-06-26-SP8-day2-conmon-console.md` (app-hosted org-admin console — emits `app` approvals); sre-v2 `docs/specs/srectl-day2-console-design.md` (operator terminal — emits `substrate` approvals).
> **Implements into:** cosmos-v2 (policy plane + courier) and sre-v2 (controller + actuators).

## 0. Provenance & why this doc exists

The update-orchestration was brainstormed alongside the north-star (2026-06-24), then **absorbed into SP8 §3** rather than written up on its own. Since then the ground shifted:

- **Two-layer split** — the platform is now a **secure runtime substrate** (`JongoDB-Labs/sre-v2`) that is the *deployment destination*, plus **mission apps** (`JongoDB-Labs/cosmos-v2` first) that deploy **into** it and **inherit** its ingress (Istio), auth (Keycloak), storage (PGO/MinIO), and security controls (Falco/mesh/audit). The "in-app vs out-of-app" line SP8 drew is now a real **cosmos↔sre repo boundary**.
- **The app-catalog merged** (sre-v2 PR #12) with the exact primitives the actuator must **reuse, not reinvent**: fail-closed cosign verification (`CheckSignature`), `uds deploy` with rollback-by-name, and an install-record ConfigMap.
- **srectl exists** — the operator-facing Day-2 console (observability, actions, backup/restore, config, ConMon export) is built and merged. Only its **P6 (updates)** phase is unbuilt; this document is its design.

This doc is the single reconciled design for the update seam. SP8 §3 and the srectl P6 phase both **defer to it**.

## 1. Scope — one seam, two targets, two policy owners

The design governs updates to **two target kinds** through **one identical trust mechanism** (sign → verify → cosign-gate → actuate). Only the policy owner and the actuation payload differ.

| Target kind | What is updated | Policy owner (signer) | Blast radius |
|---|---|---|---|
| `substrate` | the sre-v2 secure runtime — UDS Core layers, operators, the bundle | operator, via **srectl** | platform-wide; re-bases the controls every tenant app inherits |
| `app` | one mission app (cosmos, …) to a new signed digest | per-org admin, via the **cosmos console** | that tenant app only |

Because apps deploy **into** the substrate and inherit its controls, a `substrate` update is platform-wide while an `app` update is tenant-scoped. This asymmetry is *the* reason the two policy owners are distinct — and why key scoping (§3.2) enforces that distinction cryptographically.

**Non-goals.** This is not a general CD system. It orchestrates *approved, signed, verified* version changes to an already-installed platform. Initial provisioning is the installer's job (SP7/srectl); routine app *deployment* is the app-catalog's job. This seam governs **updates** to both, and reuses the app-catalog's deploy path as one of its actuators.

## 2. Components & the repo split

```
┌─ cosmos-v2 (identity · RBAC · audit-chain) ─┐        ┌─ sre-v2 (the secure runtime = destination) ─────────┐
│  cosmos /admin/platform console (SP8)        │        │  srectl operator console (P6)                        │
│   └ app-update POLICY PLANE (per-org)        │        │   └ substrate-update POLICY PLANE (operator)          │
│   └ COURIER: signs UpdateApproval(app)  ──────┼──CRD──►│  update CONTROLLER  (the only privileged mutator)     │
│      key A  ·  emits, never actuates          │        │   verify → cosign-gate → floor → backup → apply →     │
└───────────────────────────────────────────────┘        │            health-gate → rollback? → audit            │
                                                          │   Actuator iface → ZarfUdsActuator | FluxActuator     │
   srectl COURIER: signs UpdateApproval(substrate) ──────►│   reuses app-catalog: CheckSignature · deploy.go ·    │
      key B  ·  emits, never actuates                     │            install-record ConfigMap                    │
                                                          └───────────────────────────────────────────────────────┘
```

- **cosmos-v2** owns identity, RBAC, and the audit hash-chain, so the **per-org app-update policy plane** and the **courier** for `app` targets live there (SP8 §3). It signs, it never actuates.
- **sre-v2** is the destination and holds all privilege:
  - **srectl** — the operator console already built; its P6 phase is the **substrate-update policy plane + courier** (signs `substrate` targets).
  - **update controller** — the *only* component that mutates the cluster/host. A least-privilege Kubernetes controller with a narrow RBAC; a signed-binary systemd unit is the fallback for non-k8s installs.
  - **Actuator interface** with `ZarfUdsActuator` and `FluxActuator` (§4).
  - **Reuses the merged app-catalog** — `CheckSignature` (fail-closed cosign), `deploy.go` (uds deploy + rollback-by-name), the install-record ConfigMap. The update path does not re-implement verification or deploy.

**Separation of duty is load-bearing:** the app (untrusted relative to the cluster) can *request* an update by signing an approval, but only the out-of-app controller — which holds no policy authority of its own — can act. A stolen app key cannot actuate; a compromised controller cannot act without a valid, floored, signed approval.

## 3. The trust seam — the `UpdateApproval` CRD

### 3.1 Transport & shape
Either signer writes a signed `UpdateApproval` **custom resource** into the cluster; the controller watches, verifies, actuates, and writes status back. A k8s-native CR (not a network endpoint) means both signers use one identical path, there is no new listening surface to secure, it works airgapped with zero network assumptions, and **the CR itself is the audit record**.

```yaml
apiVersion: updates.sre.dev/v1alpha1
kind: UpdateApproval
spec:
  targetKind: substrate | app          # selects policy owner, key scope, actuator payload
  targetRef:  <app name | "substrate"> # what to update
  targetDigest: sha256:…               # the exact artifact (image/chart/bundle) to move to
  policySnapshot: {...}                # the policy in force at approval time (channel, preset, floor ref)
  approverIdentity: <who>              # cosmos user or operator principal
  nonce: <uuid>                        # single-use, replay guard
  issuedAt: <ts>
  expiry: <ts>
  signature: <detached sig over canonical(spec-minus-signature)>
status:
  phase: Pending | Verified | Actuating | Healthy | RolledBack | Rejected
  verifiedAt / actuatedAt: <ts>
  health: <readiness · db:up · Package Ready summary>
  rollbackOf: <digest, if this reverted a prior update>
  message: <human-readable reason on reject/rollback>
```

### 3.2 Signing & the type-scoped trust root (decision Q3)
- **Per-signer keypairs.** The cosmos app holds key **A**; the operator/srectl holds key **B**. They are distinct.
- **Type-scoped trust config** — a SOPS-managed `Secret`/`ConfigMap` the controller reads, mapping *public key → allowed `targetKind`(s)*: key A → `app` only, key B → `substrate` only.
- **Consequence:** the cosmos app **physically cannot** approve a substrate update — the controller rejects the (key, targetKind) pair. Separation of duty falls out of key scoping, not procedure. No external CA is required, so airgap is unaffected.

### 3.3 Controller verification — fail-closed, all must pass
On each `UpdateApproval`, the controller checks, in order, and **rejects + audits** on any failure:

1. **Signature** valid against a key in the trust config.
2. **Authorization** — that key is permitted for `spec.targetKind` (§3.2).
3. **Supply chain** — `targetDigest` is **cosign-verifiable** (reuse `CheckSignature`; `--offline` in airgap) and carries SLSA provenance.
4. **Floor** — `targetDigest` is at or above the security floor (§3.4).
5. **Freshness** — `nonce` is unseen (recorded consumed-nonce set) and `now < expiry`.

Only an approval passing all five reaches the actuator. Verification is identical in connected and airgap environments — the actuator is the *only* thing that varies.

### 3.4 The security floor (⚠️ design call 1 — new, concretizes SP8's "strict floor")
SP8 named a "strict security floor … can never select below the current signed/CVE-clean baseline" but never defined it. This design makes it concrete and tamper-evident:

- The floor is a **signed, monotonic baseline** the controller holds: a minimum version **and** a CVE-clean digest allowlist.
- No policy, preset, or approval can actuate below the floor — the controller rejects it at check ④, regardless of who signed.
- **Lowering the floor is itself a floor-update** — a separate, **operator-only**, audited action (its own signed record), never a side effect of an app or routine update. You cannot quietly downgrade past a known-vulnerable line.

The floor's own tamper-evidence is what makes every approval above it trustworthy; without it, "verified signature" only proves *who* asked, not that the target is *safe*.

## 4. Actuator interface + the safety wrapper

```
interface Actuator {
  Apply(target, digest)   // move target to digest
  Rollback(target)        // return target to last known-good
  Health(target)          // readiness · db:up · UDS Package Ready
}
```

- **`ZarfUdsActuator`** (airgap staging + airgap prod, and single-host devtest). `substrate` → `uds`/`zarf` bundle-deploy; `app` → the app-catalog `deploy.go` path (already cosign-gated). `Rollback` = redeploy the last known-good bundle / install-record. **Buildable and testable today.**
- **`FluxActuator`** (connected staging + connected prod). After the controller has verified the approval, it **pins the verified digest** into the Flux source (`OCIRepository`/`HelmRelease`); Flux reconciles the rollout. `Rollback` = revert the pin. **Flux never sees an unverified digest — it is the deploy *mechanism*, never the approval *authority*.** The trust seam stays single and identical across environments.

The environment's install profile selects exactly one actuator. The controller, CRD, and verification (§3) are shared.

### 4.1 The safety wrapper (⚠️ design call 2 & 3 — controller-enforced, identical for both actuators)
Every actuation runs inside one wrapper, in this order:

1. **Backup-first, gated** (⚠️ call 2) — trigger a PGO/pgBackRest backup and **gate on its success** before touching the target (reuse srectl's `TriggerBackup`, incl. the `manual.repoName`+annotation fix). Honors the hard "backup-first / no data loss" constraint at the mechanism level, not as advice.
2. **`Apply`** the verified digest.
3. **Health-gate** (⚠️ call 3) — "healthy" = pod **readiness** *and* **`db:up`** *and* UDS `Package` **Ready**, within a timeout.
4. **Rollback on failure** — any health miss → automatic `Rollback` + `phase=RolledBack` + reason.
5. **Audit** — the approval token *is* the evidence; the whole episode is recorded (§5).

## 5. End-to-end flow & tamper-evidence

Two representative paths (the seam is identical; environment + target differ):

- **App update, connected.** Org-admin approves in cosmos → cosmos signs `UpdateApproval(app)` → CR applied → controller verifies (§3.3) → `FluxActuator` pins the digest → Flux rolls out → health-gate → audit.
- **Substrate update, airgap.** Operator imports the signed bundle and approves in srectl → srectl signs `UpdateApproval(substrate)` → controller verifies (offline cosign) → `ZarfUdsActuator` `uds`-deploys → health-gate → rollback-on-fail → audit.

**Audit is dual-sink** (both already exist): the substrate `ConfigMap`/Events sink (srectl's `multiAuditor`) **and** the cosmos audit hash-chain + WORM. The `UpdateApproval` CR — approver, digest, policy snapshot, signature, verdict, health outcome — *is* the tamper-evident record; nothing extra needs to be fabricated. Rejections and rollbacks are audited as first-class outcomes, not just successes.

## 6. Testing

- **Token unit tests** — sign/verify/floor/nonce/expiry, including **forged signature, expired, below-floor, and wrong-`targetKind`-for-key → reject** (fail-closed; mirror the app-catalog's fail-open regression pins).
- **Actuator fakes** — `Apply`/`Rollback`/`Health` behind fake exec-wrappers (the app-catalog pattern), fully unit-testable with **no cluster**.
- **Integration on the lab only** — update happy-path + **rollback-on-failed-health**, on `cosmos-k8s`. **Never defcon/prod** (SP10 is the explicit per-step-auth stop).
- **Authorization negatives** — key A cannot sign `substrate`; key B cannot sign `app`.
- **Ordering** — backup-first: no `Apply` is reachable before a gated successful backup.

## 7. Build sequencing (⚠️ design call 4 — sequencing, not scope)

Both targets are **designed in full**. The **substrate path is buildable first** — srectl exists and the app-catalog primitives are merged — so the MVP is: `UpdateApproval` CRD + controller + verification + floor + `ZarfUdsActuator` + safety wrapper, driven by srectl's P6 policy plane/courier. Then: the cosmos app-update courier + per-org policy plane (SP8 §3's console), and the `FluxActuator` (stood up with the connected staging target). Design-complete for all; honest about order.

## 8. Open items & seams

- **Flux not yet deployed** — `FluxActuator` is designed; standing Flux up is part of the connected-staging bring-up (north-star SP4 GitOps is SOPS-done, Flux-pending).
- **Floor storage & signing key** — the floor baseline is SOPS-managed like the trust config; its update key is the operator key (key B) or a dedicated floor key — decide at implementation.
- **Consumed-nonce store** — a small controller-owned CR/ConfigMap; bound its growth (TTL past `expiry`).
- **Non-k8s installs** — the systemd-unit controller variant shares all verification logic; only the watch/transport differs (file-drop instead of CR). Deferred until a non-k8s target is real.

## 9. References

- SP8 (app-hosted console; app-approval surface) — `2026-06-26-SP8-day2-conmon-console.md` §3
- North-star §5 (Updates) — `2026-06-24-k8s-migration-north-star-design.md`
- srectl operator console (substrate-approval surface, P6) — sre-v2 `docs/specs/srectl-day2-console-design.md`
- App-catalog (reused verify/deploy/rollback) — sre-v2 `docs/specs/app-catalog-round2-design.md`, `installer/internal/appcatalog/{verify,deploy}.go`
- External: Defense Unicorns UDS (Core, CLI, Bundles), Zarf, Flux, cosign/SLSA, CrunchyData PGO/pgBackRest
