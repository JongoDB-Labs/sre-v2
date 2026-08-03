# SRE-v2 — Consolidated Handoff

> **Single entry point for resuming `sre-v2` development.** Written 2026-08-03 to collapse
> three parallel sessions into one and hand off to a fresh machine/account. Everything you
> need is in THIS repo — the old machine's `~/.claude` memory and the local-only `pontis`
> hub did **not** travel (see §9). Read this, then the repo docs in §8.

## 1. What this is — the two-layer model

`sre-v2` is the **Secure Runtime Environment (SRE) substrate** — a UDS-native, DoD-Big-Bang-lineage
secure runtime that is the **deployment destination** for mission apps. Mission apps
(`cosmos-v2` was the first, and is the worked example throughout) deploy **into** it as signed
UDS Packages and **inherit** its ingress (Istio), identity/SSO (Keycloak), data (PGO/MinIO),
and security controls (Falco, mesh default-deny, audit hash-chain). Pontis / ĒSO / defcon are
**skins/orgs of cosmos**, not the substrate.

**Why you're back here:** to **flesh out the substrate + DSOP** so a **second mission app**
(not cosmos) can be onboarded generically.

## 2. Current state — built + merged (PRs #1–#25, all on `main`)

### Substrate (live on the lab)
- **Cluster:** RKE2 v1.35.5 single-node lab VM `cosmos-k8s` (12 vCPU / 62 GiB, Proxmox), serving `cosmos.uds.dev`.
- **UDS Core 1.7.0** bundle: init + core-base (**Istio ambient** + Pepr uds-operator) + core-identity
  (**Keycloak 26** + Authservice) + core-runtime-security (**Falco** — 1.7 replaced NeuVector; do **not**
  assume NeuVector) + core-monitoring (**Prometheus/Grafana**). `core-metrics-server` skipped (RKE2 ships its own).
- **Operators:** CrunchyData **PGO 6.0.2** (Postgres 16 + pgvector + pgBackRest) · **MinIO 7.1.1**
  (lab-only, EOL-flagged; prod = external S3).
- **Secrets:** SOPS (age); private key lives on the lab VM only.
- **Deferred layers:** `core-logging` (Loki) + `core-backup-restore` (Velero) — need an S3/MinIO backend.
  **Flux GitOps NOT deployed** (SOPS done, Flux pending — north-star SP4).

### srectl (one Go binary — installer + Day-2 console)
- **Installer:** tview whiptail-style wizard (preflight → posture [Baseline/DoD] → sizing → domain →
  core-services → SSO → secrets → review → deploy). **Round-1 deploy orchestration is STUBBED** — it
  renders `uds-config.yaml` + a values overlay + dry-run; wiring host-prep → RKE2 → bundle is unbuilt.
- **App-catalog:** `srectl app {list,install,remove,status}` — resolve (local/OCI) → **fail-closed cosign
  verify** → advisory cohesion preflight → `uds deploy` (rollback-by-name) → install-record ConfigMap
  (`sre-appcatalog-installs`). `cosmos` is catalog entry #1 (`requires:[pgo]`).
- **Day-2 / ConMon monitor console** (k9s-style, read-first): OVERVIEW dashboard (stat tiles, CPU/MEM
  gauges + 30-min sparklines, disk/load, pod-phase, health rollup, deduped alerts) · resource browser
  (nodes/pods/workloads/services/packages/apps, `:` command bar, Enter-drill describe/yaml/logs) ·
  security views (Prometheus alerts + Falco events) · **Day-2 actions** (restart/rollout/cordon/scale +
  typed-name-gated delete — all confirm-gated + **dual-sink audited** [host JSONL + in-cluster ConfigMap])
  · **backups** (pgBackRest view + trigger + restore-to-new-cluster + in-place restore) · **compliance/ConMon**
  view + **RMF posture export** (`srectl.conmon.posture/v1` JSON) · **image-signing posture** (cosign verify,
  config-driven) · read-only **config** view.

### DSOP posture (what exists today)
- **Supply chain:** cosign keyless+KMS signing + SLSA provenance + verify-after-sign in the release
  pipeline; srectl surfaces per-digest signature posture.
- **ConMon:** compliance view + exportable RMF posture artifact (audit-chain integrity + firing alerts
  + Falco + signature posture).
- **Architecture-as-code:** the D2 diagram suite in `docs/architecture/` (system-of-systems poster +
  substrate internals + app-on-substrate + **devsecops supply chain** + **gitops/delivery** + environments).

## 3. Forward roadmap — substrate + DSOP for mission app #2

| Item | State | Note |
|---|---|---|
| **Onboard mission app #2** | the driving goal | Follow `docs/app-onboarding.md`: define the app's `Package` CR (ingress/SSO/network), its data instances (PGO cluster + buckets), its Keycloak client; deploy via `srectl app install`. Surface + fix any gaps that turn out cosmos-specific vs truly generic. |
| **P6 update orchestration** | **design-complete, unbuilt** | `docs/specs/update-orchestration-design.md` (signed `UpdateApproval` CRD → out-of-app controller → Zarf/Flux actuator, backup-gated, floor-enforced). **DESIGN-GATED: brainstorm before building; NEVER a direct mutation.** Biggest remaining subsystem. |
| **SP4 Flux GitOps** | SOPS done, Flux pending | Needed for the `FluxActuator` + connected delivery. |
| **srectl round-1 deploy** | stubbed | Wire host-prep → RKE2 → SRE-bundle orchestration. |
| **SP8 web console** | deferred | App-hosted org-admin GUI over the app-catalog backend; srectl is the terminal surface today. |
| **Loki / Velero** | deferred | Need an S3/MinIO backend first. |
| **SP9 Nango SSO** | deferred | Gov-blocked, amd64-only. |

## 4. Open PR decision

- **[#10](https://github.com/JongoDB-Labs/sre-v2/pull/10) — self-hosted runners** — OPEN, intent unclear; decide keep/close on the new machine.

## 5. Guardrails (portable copy — these lived in `~/.claude` memory)

- **P6 updates = design-gated.** Brainstorm/confirm before building. NEVER a direct mutation — always
  signed-approval → controller → Flux/Zarf.
- **Destructive/mutating tests are lab-only** (`cosmos-k8s`), NEVER prod.
- **SP10 (defcon/pontis prod cutover) = EXPLICIT STOP** (out of `sre-v2` scope, but know it): prod is
  still **Docker Compose**; per-step auth, backup-first, dry-run on a restored copy.
- **Honesty rule:** dev/lab = `cosmos.uds.dev` (only live k8s/UDS); prod (`pontis`/`defcon`) still
  Compose; no staging.
- **Build style:** subagent-driven TDD (superpowers) — spec → plan → fresh implementer per task +
  per-task spec+quality review + a final whole-branch review; PR-per-slice; squash-merge.

## 6. ⚠️ Access to RE-ESTABLISH on the new Mac (none of this travels)

- **`gh` auth** for `JongoDB-Labs` (GitHub CLI + git remote).
- **Lab cluster:** `ssh cosmos@cosmos-ssh.fightingsmartcyber.com` via **cloudflared** ProxyCommand + the
  **`id_rsa`** key (copy it, or authorize a new key). `kubectl get nodes` → `cosmos-k8s Ready`.
- **Browser access to lab UIs:** `/etc/hosts` entries for `cosmos.uds.dev` / `sso.uds.dev` → the MetalLB
  gateway IPs (admin `192.168.86.240` / tenant `192.168.86.241`); for canonical-`:443` SSO over the
  tunnel, the `sudo socat TCP-LISTEN:443 → TCP:127.0.0.1:<port>` bridge trick.
- **TUI test VM:** `192.168.86.59` (`cosmos-tui-test`) — reachable via the **bastion hop**
  (`ssh cosmos-ssh 'ssh 192.168.86.59 …'`; the VM trusts the bastion's key, not the Mac's).
- **Tooling on PATH:** `uds`, `zarf`, `cosign`, `kubectl`, `helm`, Go (srectl builds), `d2`
  (`brew install d2`, for the diagram render).
- **SOPS age private key** — lives on the lab VM only; not in git.
- ⚠️ The lab + VMs sit behind the **home-network cloudflared tunnel** (192.168.86.x). If the new Mac
  can't reach that tunnel, lab access needs re-setup first.

## 7. Cold-start resume prompt (paste into the first fresh session)

```
Resume SRE-v2 substrate development. cwd is a fresh clone of
git@github.com:JongoDB-Labs/sre-v2.git. Read docs/HANDOFF.md FIRST — it's the
consolidated handoff (substrate + DSOP state, roadmap, access prereqs, guardrails).
Goal: flesh out the substrate + DSOP to onboard a SECOND mission app (not cosmos).
First moves: (1) re-establish + confirm lab access (ssh cosmos-ssh; kubectl get nodes
→ cosmos-k8s Ready); (2) read docs/app-onboarding.md (the app-plug-in recipe) and
docs/specs/update-orchestration-design.md.
GUARDRAILS: P6 update orchestration is DESIGN-GATED — brainstorm before building, and it
is NEVER a direct mutation (signed-approval → controller → Flux/Zarf). Destructive/mutating
tests are lab-only (cosmos-k8s), never prod (defcon/pontis are still Docker Compose = SP10,
an explicit per-step-auth stop). Build style: subagent-driven TDD (superpowers) with
per-task spec+quality review + a final whole-branch review, PR-per-slice, squash-merge.
```

## 8. Repo reading list (all durable, in this repo)

- `docs/app-onboarding.md` — **the recipe for onboarding app #2** (ingress/SSO/data/network via the
  `Package` CR; cosmos as worked example).
- `docs/platform-runbook.md` — substrate bring-up runbook (gotchas #1–#12, troubleshooting playbook,
  deployment-target matrix).
- `docs/MIGRATION.md` — migrate runbook.
- `docs/architecture/` — the D2 diagram suite (`render.sh` → `rendered/*.svg`; needs `d2`) + `README.md`.
- `docs/specs/` — `srectl-day2-console-design.md`, `srectl-tui-redesign-design.md`,
  `app-catalog-round2-design.md`, `architecture-diagram-suite-design.md`, and
  **`update-orchestration-design.md`** (the ported P6 design).
- `docs/plans/` — the TDD implementation plans.
- `installer/` — `srectl` (`cmd/srectl` + `internal/{preflight,config,render,catalog,appcatalog,tui}`).

## 9. ⚠️ What did NOT travel (left on the old Mac)

- **`pontis`** (command-center hub — `WORKSTREAMS.md`, the SP7/SP8/north-star specs, design PNGs) —
  **local-only, no git remote.** The forward-relevant **P6 design is ported into this repo** (§8). The
  SP7/SP8 specs are historical — **already implemented** in srectl. If you want the whole hub, give
  `pontis` a private remote on the old machine and push it, then clone.
- **`~/.claude` memory** — old machine/account only; the portable facts are folded into this doc.
- **`cosmos-v2`** — fine, it's on GitHub and self-sustaining (mission app #1, now well ahead of 2.103.0).

---

*Consolidated 2026-08-03. Sessions collapsed: "SRE-v2 architecture and deployment diagram" (#24, merged),
"Update-orchestration design reconciliation" (P6 ported here), "srectl TUI redesign build" (#14, done).
Substrate + srectl = PRs #1–#25 on `main`.*
