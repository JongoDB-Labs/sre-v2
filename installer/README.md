# SRE installer — `srectl` (Security-Onion-style TUI/CLI)

The single tool that stands up the **substrate** (round 1) and, re-entrantly,
reconfigures it (Day-2). One Go binary: a `bubbletea` TUI for humans + full `cobra`
CLI parity for headless/airgap. Ships inside the SRE UDS bundle.

> Scope decisions (locked 2026-06-26): **full host→platform** install · **re-entrant**
> for Day-2 · creates a **platform system-admin** (`INTERNAL_ADMINS`). It does **not**
> touch any app's settings — mission apps (cosmos first) are deployed in round 2 by
> the app-catalog layer, which creates *their* first org/owner.

## Round 1 — substrate install (app-agnostic)

```
preflight host  →  RKE2 (or accept EKS/AKS)  →  SRE UDS bundle (Core + operators)  →  platform admin
```

Flow: **preflight** (arch, vCPU/RAM/disk, kernel ≥5.8, swap-off, `/dev/kmsg`; connected
vs airgap) → **posture** (Baseline | DoD-hardened) → **sizing** (small | medium | large) →
**core services** (which UDS Core layers + operators) → **SSO** (Keycloak | external OIDC) →
**secrets** (SOPS age key) → **review** (renders `uds-config.yaml` + values overlay) →
**deploy** (orchestrates the tools — never reimplements them).

Renders two files (re-runnable, git-committable): `uds-config.yaml` (bundle variables)
\+ `values.overlay.yaml` (sizing + posture). `--from answers.yaml` replays headless.

## Round 2 — apps deploy onto the running SRE

Handled by the **app-catalog layer** (shared backend; surfaces = this CLI + the SP8
web console). Sources: local tarball → OCI/GitHub → hosted store. Each app is a signed
UDS Package that auto-wires into ingress/SSO/monitor via its CR. See `docs/specs/`.

## Build order
1. **skeleton** — TUI+CLI, preflight, config model (catalog + sizing + posture), render + `--dry-run`
2. **orchestration** — wire deploy (host-prep → RKE2 → SRE bundle) + the Day-2 state-read
3. **app-catalog** — round-2 deploy layer (cosmos as the first, reference app)

Full design: `docs/specs/SP7-install-wizard.md` (migrating from the planning repo).
