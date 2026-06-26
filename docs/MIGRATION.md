# Migration — splitting the substrate out of cosmos-v2

Bootstrapped 2026-06-26. Tracks what moves here, what stays in `cosmos-v2`, and the
order. The live reference deployment already runs (RKE2 + full UDS Core, 12 vCPU/62 GiB).

## Moving here (`sre-v2`)
- [x] Substrate UDS bundle → `bundle/uds-bundle.yaml` (Core layers; operators TODO)
- [ ] **Platform runbook** — from `cosmos-v2/docs/deploy/uds-rke2-setup.md`: T0 bring-up,
      UDS Core (SP2), the **gotcha catalog #1–#12**, the **Troubleshooting playbook**, and
      the **Deployment-targets matrix** → `docs/`. (Cosmos-app deploy specifics stay there.)
- [ ] **PGO + MinIO operator** Zarf packages → `packages/` (the round-2 data-services)
- [ ] **The TUI installer** (`srectl`) → `installer/`
- [ ] **Platform CI** — build + cosign-sign the SRE bundle (keyless+KMS+SLSA+verify-gate,
      mirroring cosmos-v2's `release.yml`) → `.github/workflows/`
- [ ] **SP7 install-wizard spec** (currently `pontis/docs/specs/`) → `docs/specs/`

## Staying in `cosmos-v2` (the app)
- The cosmos Helm chart — its Deployment, its `PostgresCluster` CR + buckets, its UDS
  `Package` — the app image, the app `release.yml`, `deploy/airgap/zarf.yaml` (cosmos app pkg).
- **Refactor:** drop the chart's bundled standalone MinIO → use SRE's MinIO operator with
  cosmos-scoped buckets. (PGO is already an external prereq → becomes an SRE operator.)

## Build order (post-split)
1. **Substrate installer** (`installer/`) — round-1 platform bring-up.
2. **App-catalog / deploy layer** — round-2 mission-app deploy (one backend, two surfaces:
   `srectl` + the SP8 web console); sources local → OCI/GitHub → store.
3. **cosmos as the first app** — carries the **Keycloak SSO** + **PGO** wiring as the
   reference for how any app plugs in; `bootstrap-org` neutral seed mints its first org/owner.

## Coupling
`sre-v2` and `cosmos-v2` couple **only by OCI reference** (the app-catalog pulls the signed
cosmos package onto a running SRE), never by repo dependency.
