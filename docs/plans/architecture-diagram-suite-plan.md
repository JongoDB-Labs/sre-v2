# Architecture & Delivery Diagram Suite — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Author a maintainable D2 diagram-as-code suite — one overview poster + five drill-downs — that renders to committed SVG/PNG and replaces the flat north-star poster.

**Architecture:** Six `.d2` sources in `sre-v2/docs/architecture/`, each importing a shared `_theme.d2` that defines the color/box/boundary/gate classes and the legend. A `render.sh` renders every source to `rendered/*.svg` and `*.png` via the `d2` CLI (which fails closed on syntax errors — that is the per-task test). Rendered artifacts are committed so GitHub and ATO/onboarding PDFs need no toolchain. Every diagram box traces to a real repo file; visual grammar mirrors UDS LikeC4 + the DoD DevSecOps Reference Design.

**Tech Stack:** D2 (`d2lang.com`, ELK layout engine), `d2` CLI, bash. No app code.

## Global Constraints

- **Language = D2 only.** Every `.d2` imports `_theme.d2`; never inline colors/styles — use theme classes so one edit propagates. (Copied from spec §4.)
- **Diagram home** = `sre-v2/docs/architecture/`; spec at `sre-v2/docs/specs/architecture-diagram-suite-design.md`. Do not scatter across repos.
- **Every view carries a title + a legend** (C4 rule) and, where reality diverges from north-star, a **current-vs-target** marker. (spec §2.3)
- **Honesty constraint (view ⑥):** prod (`pontis.fightingsmartcyber.com`, `defcon.fightingsmartcyber.com`) is **still on Docker Compose**; dev/lab is the live UDS substrate at `cosmos.uds.dev`; **no staging exists.** Never draw prod as already-on-k8s. (spec §3⑥)
- **Grounding:** every box maps to a real path (`bundle/uds-bundle.yaml`, `packages/{pgo,minio}/zarf.yaml`, `catalog.yaml`, `installer/…`, `cosmos-v2/charts/cosmos/templates/…`, `cosmos-v2/.github/workflows/{security,release}.yml`). (spec §4)
- **Commit identity:** `git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB"` (GH007 email-privacy fix). Branch: `docs/architecture-diagram-suite` (already created).
- **Color roles (verbatim, spec §2.2):** `source` grey · `ci` blue · `artifact` purple · `gitops` amber · `substrate` teal · `app` dark-red · `env` green · `external` muted.

---

## Task 1: Toolchain + shared theme + render harness

**Files:**
- Create: `sre-v2/docs/architecture/_theme.d2`
- Create: `sre-v2/docs/architecture/render.sh`
- Create: `sre-v2/docs/architecture/rendered/.gitkeep`
- Create (temp, delete after): `sre-v2/docs/architecture/_smoke.d2`

**Interfaces:**
- Produces (consumed by every later task): the class names `source`, `ci`, `artifact`, `gitops`, `substrate`, `app`, `env`, `external`, `boundary`, `gate`, `note`; the edge label conventions (`observability` style via `class: obs`); and the `legend` container pattern. Later tasks apply these with `x.class: substrate` etc. — they must not redefine styles.

- [ ] **Step 1: Install the `d2` CLI**

Run: `command -v d2 || brew install d2` (fallback: `curl -fsSL https://d2lang.com/install.sh | sh -s --`)
Expected: `d2 --version` prints a version (≥ 0.6).

- [ ] **Step 2: Write `_theme.d2`** (the shared contract — full content)

```d2
# _theme.d2 — shared legend, colors, and element classes for the SRE-v2 suite.
# Every view imports this:  ...@_theme
# Color roles per spec §2.2. Nesting = trust boundary; ◆ = control gate.
vars: {
  d2-config: {
    layout-engine: elk
    pad: 40
  }
}

classes: {
  # --- color roles (fills) ---
  source:    { style: { fill: "#3f3f46"; stroke: "#18181b"; font-color: "#fafafa"; border-radius: 6 } }
  ci:        { style: { fill: "#1e40af"; stroke: "#1e3a8a"; font-color: "#eff6ff"; border-radius: 6 } }
  artifact:  { style: { fill: "#5b21b6"; stroke: "#4c1d95"; font-color: "#f5f3ff"; border-radius: 6 } }
  gitops:    { style: { fill: "#92400e"; stroke: "#78350f"; font-color: "#fffbeb"; border-radius: 6 } }
  substrate: { style: { fill: "#0f5132"; stroke: "#052e16"; font-color: "#ecfdf5"; border-radius: 6 } }
  app:       { style: { fill: "#7f1d1d"; stroke: "#450a0a"; font-color: "#fef2f2"; border-radius: 6 } }
  env:       { style: { fill: "#166534"; stroke: "#14532d"; font-color: "#f0fdf4"; border-radius: 6 } }
  external:  { style: { fill: "#52525b"; stroke: "#3f3f46"; font-color: "#e4e4e7"; border-radius: 6; stroke-dash: 2 } }

  # --- structural ---
  boundary:  { style: { fill: transparent; stroke: "#94a3b8"; stroke-dash: 4; font-color: "#cbd5e1"; border-radius: 8 } }
  gate:      { shape: diamond; style: { fill: "#b45309"; stroke: "#78350f"; font-color: "#fffbeb" } }
  note:      { shape: page; style: { fill: "#1f2937"; stroke: "#374151"; font-color: "#e5e7eb"; font-size: 11; italic: true } }

  # --- edge classes (applied to connections) ---
  obs:       { style: { stroke: "#38bdf8"; stroke-dash: 3; font-color: "#7dd3fc" } }   # observability flows
  alt:       { style: { stroke: "#a1a1aa"; stroke-dash: 5 } }                          # alternative/airgap path
  gap:       { style: { stroke: "#dc2626"; stroke-dash: 6; font-color: "#fca5a5"; stroke-width: 2 } }  # current→target gap
}

# Reusable legend block — each view instantiates `legend` and fills rows it uses.
# Views place it bottom-left. Keep entries to the classes actually used in the view.
```

- [ ] **Step 3: Write `render.sh`** (full content)

```bash
#!/usr/bin/env bash
# Render every view *.d2 -> rendered/<name>.svg and .png
set -euo pipefail
cd "$(dirname "$0")"
mkdir -p rendered
shopt -s nullglob
for f in [0-9][0-9]-*.d2; do
  name="${f%.d2}"
  echo "rendering $f"
  d2 --theme 200 --dark-theme 200 "$f" "rendered/${name}.svg"
  d2 --theme 200 "$f" "rendered/${name}.png"
done
echo "done -> rendered/"
```

- [ ] **Step 4: Smoke-test the toolchain + theme import**

Create `_smoke.d2`:

```d2
...@_theme
title: SMOKE { near: top-center; shape: text }
a: substrate box { class: substrate }
b: app box { class: app }
a -> b: deploy
```

Run: `cd sre-v2/docs/architecture && d2 _smoke.d2 rendered/_smoke.svg`
Expected: exit 0; `rendered/_smoke.svg` exists.
Run: `grep -c "substrate box" rendered/_smoke.svg`
Expected: ≥ 1.

- [ ] **Step 5: Clean up smoke artifacts, make render.sh executable**

Run: `rm -f _smoke.d2 rendered/_smoke.svg rendered/_smoke.png && chmod +x render.sh && touch rendered/.gitkeep`

- [ ] **Step 6: Commit**

```bash
git add docs/architecture/_theme.d2 docs/architecture/render.sh docs/architecture/rendered/.gitkeep
git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" \
  commit -m "feat(diagrams): shared D2 theme + render harness"
```

---

## Task 2: View ① — System-of-Systems poster

**Files:**
- Create: `sre-v2/docs/architecture/01-system-of-systems.d2`
- Create: `sre-v2/docs/architecture/rendered/01-system-of-systems.svg` (+ `.png`)

**Interfaces:**
- Consumes: theme classes from Task 1.
- Produces: the anchor names each drill-down links back to (band ids: `repos`, `ci`, `ghcr`, `delivery`, `substrate`, `app`, `envs`).

**Element inventory (build every item as a box/boundary; left→right bands):**
1. `repos` (class boundary) containing `sre-v2` + `cosmos-v2` (class source), caption "SOPS-encrypted secrets".
2. `ci` (boundary) containing `build` · `test` · `scan` · `sbom` · `sign+attest` (class ci) — caption "DevSecOps → drill-down ④".
3. `ghcr` (boundary) containing `signed images` · `OCI Helm charts` · `UDS/Zarf bundles` (class artifact).
4. `delivery` (boundary) containing `Flux (connected)` · `signed-approval gate ◆` (class gate) · `Zarf/UDS (airgap)` (class gitops) — caption "→ drill-down ⑤".
5. `substrate` (boundary, class substrate) — collapsed box "SRE-v2 · UDS Core + PGO/MinIO on RKE2 → drill-down ②".
6. `app` (boundary, class app) — collapsed "app-on-substrate: Deployment + PGO + MinIO + UDS Package → drill-down ③".
7. `envs` (boundary, class env) — "dev (cosmos.uds.dev) → prod (pontis./defcon., Compose) · no staging → drill-down ⑥".
- Edges: `repos -> ci -> ghcr -> delivery -> substrate -> app` (solid); `app -> envs: promote right`.
- `legend` block (classes used) + `title`.

- [ ] **Step 1:** Author `01-system-of-systems.d2` per the inventory above, importing `...@_theme`, every box tagged with its class, each band captioned with its drill-down number.
- [ ] **Step 2: Render & verify**
Run: `cd sre-v2/docs/architecture && d2 01-system-of-systems.d2 rendered/01-system-of-systems.svg && d2 01-system-of-systems.d2 rendered/01-system-of-systems.png`
Expected: exit 0; both files created.
- [ ] **Step 3: Verify key anchors present**
Run: `for k in "SRE-v2" "app-on-substrate" "signed-approval" "cosmos.uds.dev" "drill-down"; do grep -q "$k" rendered/01-system-of-systems.svg && echo "OK $k" || echo "MISS $k"; done`
Expected: all `OK`.
- [ ] **Step 4: Commit**
```bash
git add docs/architecture/01-system-of-systems.d2 docs/architecture/rendered/01-system-of-systems.*
git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" \
  commit -m "feat(diagrams): view 1 — system-of-systems poster"
```

---

## Task 3: View ② — Substrate internals (granular)

**Files:**
- Create: `sre-v2/docs/architecture/02-substrate-internals.d2` (+ rendered `.svg`/`.png`)

**Interfaces:** Consumes theme classes. This is the most detailed view (spec §3②).

**Element inventory — nest boundaries: Host → RKE2 → platform add-ons → UDS Core layers → data operators. Each named component below is its own box inside its namespace boundary (class substrate; namespace boxes class boundary):**
- **Host/node:** `RKE2` containing `containerd` · `kubelet` · `CoreDNS` · `kube-proxy`; note "kernel ≥5.8 · swap off · /dev/kmsg".
- **Platform add-ons boundary:** `local-path-provisioner` (note "RKE2 has no default StorageClass — #1") · `MetalLB` (`IPAddressPool` + `L2Advertisement`, note "no cloud LB — #2") · `cert-manager` · `metrics-server`.
- **core-base** ns: `istiod` · `ztunnel` · `istio-cni`; gateway sub-boundary: `tenant` · `admin` · `passthrough` · `egress` · `egress-ambient`; waypoints `keycloak-waypoint` · `customer-waypoint`; `pepr-system` sub-boundary: `UDS Operator (Watcher+Admission)` · `Policy Engine`; CRDs box `Package · Exemption · ClusterConfig`.
- **core-identity-authorization** ns: `Keycloak (realm uds)` · `Authservice`; external `Keycloak Postgres` (class external).
- **core-runtime-security** ns: `Falco` · `FalcoSidekick` · `NeuVector (controller/enforcer/manager/scanner)`.
- **core-monitoring** ns: `Prometheus` · `prometheus-operator` · `Alertmanager` · `kube-state-metrics` · `node-exporter` · `blackbox-exporter` · `Grafana`.
- **core-logging** ns: `Vector` · `Loki (gateway/read/write/backend)`; external `S3` (class external).
- **core-backup-restore** ns: `Velero`; external `S3` (class external).
- **Data operators:** `postgres-operator` ns → `PGO 6.0.2` (note carried images pg16.14 pgvector + pgbackrest 2.58) · `minio-operator` ns → `MinIO operator 7.1.1` (note "lab only; prod=external FIPS S3").
- **Zarf airgap boundary:** `zarf-injector` -> `zarf-seed-registry` -> `zarf-registry` · `zarf-agent (mutating webhook)`.
- **Control plane:** `srectl` + `catalog.yaml` (note round-1 install flow + round-2 cosign-verify app install); `Exemptions` box (class note, "narrow admin-owned escapes: #4–#7").
- Edges: monitoring scrape lines use `class: obs`; deploy/own edges solid. Legend + title. Caption "image flavors: upstream (lab) · registry1 (Iron Bank/DoD)".

- [ ] **Step 1:** Author `02-substrate-internals.d2` with every component above as its own box inside the correct namespace boundary; scrape/log edges tagged `class: obs`.
- [ ] **Step 2: Render & verify**
Run: `cd sre-v2/docs/architecture && d2 02-substrate-internals.d2 rendered/02-substrate-internals.svg && d2 02-substrate-internals.d2 rendered/02-substrate-internals.png`
Expected: exit 0.
- [ ] **Step 3: Verify granularity (every marquee component present)**
Run: `for k in RKE2 local-path MetalLB cert-manager metrics-server istiod ztunnel istio-cni tenant admin Pepr Keycloak Authservice Falco NeuVector Prometheus Grafana Vector Loki Velero PGO MinIO zarf-injector zarf-agent srectl Exemption; do grep -q "$k" rendered/02-substrate-internals.svg && echo "OK $k" || echo "MISS $k"; done`
Expected: all `OK` (fix any MISS before commit).
- [ ] **Step 4: Commit**
```bash
git add docs/architecture/02-substrate-internals.d2 docs/architecture/rendered/02-substrate-internals.*
git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" \
  commit -m "feat(diagrams): view 2 — granular substrate internals"
```

---

## Task 4: View ③ — App-on-substrate contract

**Files:** Create `03-app-on-substrate.d2` (+ rendered).

**Element inventory (app namespace `cosmos`, class app; cosmos as reference for the reusable contract):**
- `Deployment` + `Service` (note "non-root · /api/health probes · digest-pinned image").
- `PostgresCluster (PGO)` (note "v16+pgvector · cosmos superuser + least-priv cosmos_app · pgBackRest repo1(+repo2→S3)") — from `charts/cosmos/templates/postgrescluster.yaml`.
- `MinIO Tenant` (buckets `uploads · pgbackrest · audit-worm(WORM)`).
- `migrate hook Job` (note "pre-install: init creates cosmos_app → prisma migrate deploy").
- `verify-audit-chain CronJob` (note "AU-9 · every 6h").
- `UDS Package CR` box with three labeled out-edges to the substrate (drawn as external boundary `substrate (view ②)`, class external): `expose → VirtualService (cosmos.uds.dev, tenant gw)`, `sso → Keycloak client (OIDC PKCE)`, `monitor → ServiceMonitor` (class obs).
- Gotcha notes (class note): "portless MinIO egress 443→9000 (#15)" · "KubeAPI egress for Patroni" · "Keycloak egress via tenant gateway (#10)".
- Contract caption (class note): "**Any compatible app supplies:** Helm chart + UDS Package CR + PostgresCluster/Tenant + migrate hook."
- Edges: app→PGO `psql/TLS`; app→MinIO `S3`; app→Keycloak `OIDC/HTTPS`. Legend + title.

- [ ] **Step 1:** Author `03-app-on-substrate.d2` per inventory.
- [ ] **Step 2: Render & verify** — `d2 03-app-on-substrate.d2 rendered/03-app-on-substrate.svg && d2 03-app-on-substrate.d2 rendered/03-app-on-substrate.png` → exit 0.
- [ ] **Step 3: Verify** — `for k in Deployment PostgresCluster "MinIO Tenant" "migrate" "UDS Package" VirtualService "OIDC" audit-worm "compatible app"; do grep -q "$k" rendered/03-app-on-substrate.svg && echo "OK $k" || echo "MISS $k"; done` → all OK.
- [ ] **Step 4: Commit** — `git add docs/architecture/03-app-on-substrate.* && git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" commit -m "feat(diagrams): view 3 — app-on-substrate contract"`

---

## Task 5: View ④ — DevSecOps supply chain

**Files:** Create `04-devsecops-supply-chain.d2` (+ rendered).

**Element inventory (DoD Fig-2 grammar; left→right):**
- Phase row: `Plan` ◆ `Build` ◆ `Test` ◆ `Release` ◆ `Deploy` (◆ = class gate between phases).
- Full-width **continuous security band** (boundary) with the 8 gates (class ci) from `cosmos-v2/.github/workflows/security.yml`: `SAST/CodeQL` · `SCA/Trivy+OSV` · `secrets/gitleaks` · `image-CVE/Trivy` · `IaC/hadolint+Checkov` · `SBOM/Syft` · `sign+provenance` · `config-assertions`; each noted with its 800-171 control id (3.11.2, 3.5.1/2, 3.4.x, 3.14.x).
- `release.yml` chain (class ci→artifact): `native multi-arch build (no QEMU)` -> `merge manifest` -> `Syft SBOM` -> `cosign keyless + KMS` -> `SLSA attest` -> `◆ verify-after-sign` -> `GHCR: images · OCI chart · UDS bundle` (class artifact).
- Externals (class external): `Fulcio` · `Rekor` (de-emphasized), edges from `cosign`.
- SLSA spine caption (class note): "Source → Build → Package → Consumer · Build L2/L3".
- Legend + title.

- [ ] **Step 1:** Author `04-devsecops-supply-chain.d2`.
- [ ] **Step 2: Render & verify** — render both formats → exit 0.
- [ ] **Step 3: Verify** — `for k in CodeQL gitleaks Syft SBOM cosign "verify-after-sign" SLSA GHCR "3.11.2" Rekor; do grep -q "$k" rendered/04-devsecops-supply-chain.svg && echo "OK $k" || echo "MISS $k"; done` → all OK.
- [ ] **Step 4: Commit** — `git add docs/architecture/04-devsecops-supply-chain.* && git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" commit -m "feat(diagrams): view 4 — devsecops supply chain"`

---

## Task 6: View ⑤ — GitOps & delivery / update model

**Files:** Create `05-gitops-delivery.d2` (+ rendered).

**Element inventory (three lanes sharing GHCR source):**
- Shared source: `GHCR signed artifacts` (class artifact).
- **Lane A — connected (Flux):** `Git/OCI source` -> `Source Controller` -> `Helm/Kustomize Controller` -> `cluster` — draw the **pull reconcile loop inside** a `cluster` boundary (arrow originates inside cluster = pull); label loop "observe→compare→apply→report".
- **Lane B — airgap (Zarf/UDS):** `uds/zarf create` -->〔`air-gap` boundary, class boundary〕--> `deploy` (edges class alt); inside far cluster: `injector → in-cluster registry → mutating-webhook rewrite`; `cosign --offline verify` as a `◆` gate before deploy.
- **Lane C — today:** `manual uds/helm pull` (class note "pre-Flux interim", edges class alt).
- Overlay: `Day-2 / ConMon console` (class app) with edges to `update policy + signed approvals` (`◆` gate class gate) and dashed `surfaces:` links to `Grafana · Falco · Flux` (class obs).
- Legend + title.

- [ ] **Step 1:** Author `05-gitops-delivery.d2`.
- [ ] **Step 2: Render & verify** → exit 0.
- [ ] **Step 3: Verify** — `for k in "Source Controller" reconcile air-gap injector "mutating-webhook" "cosign --offline" ConMon "signed approval"; do grep -q "$k" rendered/05-gitops-delivery.svg && echo "OK $k" || echo "MISS $k"; done` → all OK.
- [ ] **Step 4: Commit** — `git add docs/architecture/05-gitops-delivery.* && git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" commit -m "feat(diagrams): view 5 — gitops & delivery/update model"`

---

## Task 7: View ⑥ — Environments & promotion (honest current→target)

**Files:** Create `06-environments.d2` (+ rendered).

**Element inventory (enforce the honesty constraint):**
- `dev / lab` (class env): `cosmos-k8s / cosmos.uds.dev` note "live RKE2 + UDS Core — where views ②③ run today".
- `prod` boundary containing `pontis.fightingsmartcyber.com` + `defcon.fightingsmartcyber.com` (class env) each noted "**Docker Compose (live data)** · cloudflared→Caddy".
- **No staging:** an explicit `note` box "no staging environment exists".
- **SP10 gap:** a `gap`-class edge from `prod (Compose)` to a dashed target `prod on k8s/UDS` labeled "SP10 cutover — backup-first, no data loss".
- `posture` note: "baseline (upstream) vs DoD (registry1/Iron Bank · FIPS · strict netpol · 1095-day audit)".
- `future` (class external, dashed): `airgapped DoD enclave (external S3 · offline verify)`.
- Promotion arrow dev → prod (solid), prod → enclave (class alt). Legend + title with a visible "CURRENT vs TARGET" caption.

- [ ] **Step 1:** Author `06-environments.d2`; verify no element implies prod-on-k8s as current.
- [ ] **Step 2: Render & verify** → exit 0.
- [ ] **Step 3: Verify honesty markers** — `for k in "cosmos.uds.dev" "Docker Compose" "no staging" "SP10" pontis defcon baseline; do grep -q "$k" rendered/06-environments.svg && echo "OK $k" || echo "MISS $k"; done` → all OK.
- [ ] **Step 4: Commit** — `git add docs/architecture/06-environments.* && git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" commit -m "feat(diagrams): view 6 — environments & promotion (current vs target)"`

---

## Task 8: Finalize — full render, README, self-review, wrap-up

**Files:** Modify `sre-v2/docs/architecture/README.md` (already links the six views — verify links resolve); ensure all `rendered/*` committed.

- [ ] **Step 1: Full clean render**
Run: `cd sre-v2/docs/architecture && ./render.sh`
Expected: renders all six with exit 0; `rendered/` has 6 `.svg` + 6 `.png`.
- [ ] **Step 2: Verify README links resolve**
Run: `for f in 01-system-of-systems 02-substrate-internals 03-app-on-substrate 04-devsecops-supply-chain 05-gitops-delivery 06-environments; do test -f "$f.d2" && test -f "rendered/$f.svg" && echo "OK $f" || echo "MISS $f"; done`
Expected: all `OK`.
- [ ] **Step 3: Spec-coverage self-review** — confirm each spec §3 view ①–⑥ has a rendered artifact and each §2 visual-system rule (nesting=boundary, ◆ gate, obs edges, current-vs-target) appears in ≥1 view. Fix gaps inline.
- [ ] **Step 4: Commit any re-renders + README**
```bash
git add docs/architecture/README.md docs/architecture/rendered/
git -c user.email="198221045+JongoDB@users.noreply.github.com" -c user.name="JongoDB" \
  commit -m "docs(diagrams): full render + README index for the suite"
```
- [ ] **Step 5: Report** — show the user the six rendered SVGs (SendUserFile) and summarize; offer to open a PR or merge the branch.

---

## Self-Review (plan vs spec)

- **Spec coverage:** spec §3 views ①–⑥ → Tasks 2–7; §2 visual system → Task 1 (`_theme.d2`) + applied throughout; §4 toolchain/layout → Tasks 1 & 8; §6 success criteria → per-task verify steps + Task 8 §3. No gaps.
- **Placeholder scan:** element inventories are concrete (named components + real file paths); verify steps use real `grep` keys; theme + render.sh given in full. None.
- **Type consistency:** class names (`source/ci/artifact/gitops/substrate/app/env/external/boundary/gate/note/obs/alt/gap`) defined once in Task 1 and referenced verbatim in Tasks 2–7. Commit identity + branch identical across tasks.
