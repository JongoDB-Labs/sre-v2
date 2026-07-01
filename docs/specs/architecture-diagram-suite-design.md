# SRE-v2 Architecture & Delivery Diagram Suite — Design Spec

> **Design doc (spec).** Decided via brainstorming on 2026-07-01. Defines a
> maintainable **diagram-as-code (D2)** suite that replaces the single
> north-star poster with a **poster + 5 drill-downs**, covering the SRE-v2
> substrate, the app-on-substrate contract, the DevSecOps/GitOps delivery
> model, and the real environment topology.
>
> **Sources of truth:** `sre-v2` (substrate: `bundle/`, `catalog.yaml`,
> `installer/`, `packages/`) and `cosmos-v2` (reference app: `charts/cosmos/`,
> `deploy/airgap/`, `.github/workflows/`). Diagrams live in
> `sre-v2/docs/architecture/`; this spec co-locates in `sre-v2/docs/specs/`.

---

## 1. Goal & audience

The current poster (`pontis/docs/specs/2026-06-24-k8s-migration-north-star-design.md`
distilled to one image) flattens a **two-layer product** — a reusable UDS
substrate plus pluggable mission apps — into one column, and tries to hold
architecture + integration + pipeline + environments on a single canvas. It
"barely scratches the surface."

**Goal:** a legible, cited, maintainable suite that shows the substrate down to
its core services, how any compatible app deploys onto it and integrates with
auth/ingress/backend/monitoring, the DevSecOps + GitOps methodology across
develop → deliver → deploy → update, and the honest dev/prod topology.

**Audience (all three, ranked):**
1. **Gov / ATO reviewers** — trust boundaries, supply chain (cosign/SLSA/SBOM),
   ConMon/RMF evidence, default-deny mesh, FIPS/Iron Bank posture, airgap.
2. **Engineering onboarding** — component boundaries, data flow, repo→artifact→
   runtime mapping, "how do I deploy an app."
3. **Internal design reference** — maximum fidelity north-star working doc.

**Decisions locked (brainstorming 2026-07-01):** structure = poster + drill-downs;
format = diagram-as-code; language = **D2**; scope = all 6 views; home = **sre-v2**.

---

## 2. Visual system (the shared backbone)

One legend, applied across all six views, so the suite reads as one document.
Grounded in the conventions the target ecosystems already publish (§7):

### 2.1 Structural grammar (from UDS LikeC4 + C4)
- **Nesting = trust/scope boundary.** Infrastructure → Cluster → Namespace →
  Pod/CR. A **dashed enclosure** is always a boundary (namespace, mesh, air-gap);
  a **solid box** is a concrete thing (pod, operator, artifact, repo).
- **C4 altitude per view.** Landscape (①) → Container (②③) → Dynamic/pipeline
  (④⑤) → Deployment (⑥). Each view stays at one altitude so it stays legible
  ("Google Maps zoom": each level tells one story to one audience).
- **Every view carries a title + its own legend** (C4 rule). No view depends on
  another to be understood.

### 2.2 Color roles (carried across all views; extends the current poster's family)
| Role | Meaning |
|---|---|
| `source` | git repos / host / source of truth |
| `ci` | CI · DevSecOps jobs & gates |
| `artifact` | GHCR OCI artifacts (images, charts, bundles) |
| `gitops` | GitOps / delivery / gate |
| `substrate` | SRE-v2 substrate & core services |
| `app` | mission app (cosmos-v2 & compatible apps) |
| `env` | environments |
| `external` | de-emphasized externals (Fulcio/Rekor, IdP, S3) |

### 2.3 Flow & motif semantics
- **Solid arrow** = hard dependency / deploy / call. Label with protocol where it
  matters (e.g. `OIDC/HTTPS`, `psql/TLS`).
- **Dashed arrow** = **alternative path** (airgap vs connected) — Big Bang's
  `stroke-dasharray` convention.
- **`observability`-tagged arrow** (distinct style) = scrape/log/trace flows, so
  monitoring lines never clutter the call graph (UDS LikeC4 does this).
- **◆ control-gate diamond** = every Go/No-Go promotion point — a DevSecOps
  security gate, `verify-after-sign`, or a verify-before-deploy admission check.
  This one motif visually unifies DevSecOps → delivery → deploy. (DoD DevSecOps
  Reference Design Fig. 2; identical concept to a Sigstore policy-controller /
  Binary-Authorization admission gate.)
- **Current-vs-target ribbon.** Where reality diverges from the north star
  (notably prod still on Compose), a labeled ribbon / dashed "gap" arrow marks
  it. The suite must be **honest**, not aspirational.

---

## 3. The six views

Every box traces to a real file so onboarding readers jump from picture to code.

### ① System-of-Systems poster — *C4 System Landscape*
The master map and the anchor for all drill-downs. Left→right bands:
`sre-v2` + `cosmos-v2` repos (SOPS-encrypted secrets) → **DevSecOps CI**
(build·test·scan·SBOM·sign) → **GHCR** signed artifacts (images · OCI charts ·
UDS/Zarf bundles) → **delivery gate** (Flux connected / signed-approval / Zarf
airgap) → **SRE-v2 substrate runtime** (UDS Core + operators) → **app-on-
substrate** (Deployment + PGO + MinIO + UDS Package) → **environments**
(dev → prod, promote right). Each band names its drill-down (②–⑥).
*Replaces the current poster.*

### ② Substrate internals (`sre-v2`) — *C4 Container*
The nitty-gritty of what the substrate IS. Cluster boundary containing one
namespace box per UDS Core layer (from `bundle/uds-bundle.yaml` v1.7.0):
- **core-base** — Istio **ambient** (`istiod`, `ztunnel`, `istio-cni`) + gateways
  (**tenant**, **admin**, passthrough, egress) + waypoints; **Pepr** (UDS
  Operator + Policy Engine: default-deny + mutate-to-nonroot + `Exemption` CRs).
- **core-identity-authorization** — Keycloak (realm `uds`) + Authservice.
- **core-runtime-security** — Falco (+ Falco UI).
- **core-monitoring** — Prometheus/Alertmanager/Grafana (+ Loki/Vector, Jaeger).
- **PGO** (`packages/pgo`, 6.0.2) & **MinIO** operator (`packages/minio`, 7.1.1)
  — cluster-wide data-service operators; per-app CRs live in ③.
- **Zarf init** — in-cluster registry + mutating agent.
- **srectl + catalog.yaml** — round-1 installer (preflight→posture→sizing→
  services→SSO→secrets→render `uds-config.yaml`+`values.overlay.yaml`→deploy) and
  round-2 app-install flow (resolve→**cosign verify**→preflight→`uds deploy`→
  record→confirm).
Callouts: admin vs tenant gateway split; default-deny + narrow exemptions;
image flavors `upstream` (lab) vs `registry1` (Iron Bank/DoD).

### ③ App-on-substrate contract (cosmos-v2 as reference) — *C4 Container→Component*
The **reusable contract** any compatible app implements. App namespace box
(`charts/cosmos/`):
- **Deployment + Service** (non-root, health probes, digest-pinned image).
- **PGO `PostgresCluster`** (`templates/postgrescluster.yaml`) — v16 + pgvector,
  `cosmos` superuser + least-priv `cosmos_app`, pgBackRest repo1 (+repo2→S3).
- **MinIO `Tenant` / StatefulSet** — buckets: uploads, pgbackrest, **audit-worm**
  (object-lock/COMPLIANCE).
- **migrate pre-install hook Job** — init container creates least-priv role →
  `prisma migrate deploy`.
- **ops CronJob** — `verify-audit-chain` (AU-9) every 6h.
- **UDS `Package` CR** (`templates/uds-package.yaml`) auto-wiring:
  `expose`→Istio VirtualService (`cosmos.uds.dev`, tenant gateway);
  `sso`→Keycloak client + `secretName` (**OIDC PKCE** flow, identity =
  `(idpConnId, subject)`, secret sealed AES-256 via `SSO_VAULT_KEY`);
  `monitor`→ServiceMonitor; default-deny + allow-lists.
Real gotchas as annotations: **portless** MinIO egress (443→9000 post-DNAT);
**KubeAPI** egress for Patroni DCS; Keycloak egress **via tenant gateway**
(gotcha #10). A short "any compatible app supplies: chart + Package CR +
PostgresCluster/Tenant + migrate hook" caption states the contract explicitly.

### ④ DevSecOps supply chain — *Dynamic / pipeline*
DoD Fig-2 grammar: phase row `Plan → Build → Test → Release → Deploy` with ◆
gates; a full-width **continuous security band** = the 8 CMMC-mapped gates from
`cosmos-v2/.github/workflows/security.yml` (SAST/CodeQL · SCA/Trivy·OSV ·
secrets/gitleaks · image-CVE/Trivy · IaC/hadolint·Checkov · SBOM/Syft ·
sign+provenance · config-assertions). Then the `release.yml` chain: native
multi-arch build (no QEMU) → merge manifest → **Syft SBOM** → **cosign
keyless + KMS** → **SLSA attest** → **◆ verify-after-sign** → GHCR (images +
OCI Helm chart + UDS bundle). Annotate with **SLSA Build L2/L3** and 800-171
control IDs (3.11.2, 3.4.x, 3.14.x, 3.5.1/2). Externals (Fulcio/Rekor)
de-emphasized. Mirror the SLSA `Source→Build→Package→Consumer` spine.

### ⑤ GitOps & delivery / update model — *Dynamic*
Three lanes sharing the GHCR artifact source:
- **Connected (Flux)** — Git/OCI source → Source Controller (artifact) →
  Helm/Kustomize Controller → cluster; the **pull reconcile loop drawn inside
  the cluster boundary** (OpenGitOps: agent-in-cluster = pull).
- **Airgap (Zarf/UDS)** — `uds/zarf create` →〔**dashed air-gap boundary**〕→
  `deploy`; the injector→in-cluster registry→**mutating-webhook rewrite** trick;
  **`cosign --offline`** verify before deploy.
- **Today** — manual `uds`/`helm` pull (pre-Flux reality, dashed as interim).
Overlay: the app-hosted **Day-2 / ConMon console** (owns update policy + signed
approvals + backup/restore; surfaces Grafana/Falco/Flux) and the
**signed-approval update gate** (◆) that re-gates every update.

### ⑥ Environments & promotion — *C4 Deployment (honest current → target)*
The real topology, with a current-vs-target ribbon:
- **dev / lab** — `cosmos-k8s` / **`cosmos.uds.dev`**: the live RKE2 + UDS Core
  substrate where ②③ actually run today.
- **prod** — **`pontis.fightingsmartcyber.com`** + **`defcon.fightingsmartcyber.com`**:
  **both live, both still on Docker Compose** (cloudflared → Caddy), pending
  the **SP10 Compose→k8s cutover** (drawn as the gap arrow to close;
  backup-first, no data loss).
- **no staging** (stated explicitly, not implied).
- **posture** — baseline (upstream images) vs DoD (registry1/Iron Bank, FIPS,
  strict netpol, 1095-day audit).
- **future** — airgapped DoD enclave (external S3, offline verify).

---

## 4. Toolchain, files, maintainability

- **Language:** D2 (`d2lang.com`). Install: `brew install d2`.
- **Layout** (`sre-v2/docs/architecture/`): `_theme.d2` (shared legend + color
  classes) imported by each view; `01..06-*.d2`; `render.sh`
  (`d2 <in> rendered/<out>.svg` + `.png`); `rendered/` committed; `README.md`
  index. Spec in `sre-v2/docs/specs/`.
- **Consistency:** all views import `_theme.d2`, so one edit to a color/class
  propagates. Cross-links: each poster band names its drill-down; each drill-down
  footer links back to ①.
- **Grounding & citations:** every box maps to a real path (e.g.
  `bundle/uds-bundle.yaml`, `charts/cosmos/templates/uds-package.yaml`,
  `.github/workflows/release.yml`); reference URLs (§7) captioned in-diagram or
  listed in `README.md`.
- **Commit rendered artifacts** so GitHub + ATO PDFs need no toolchain.

---

## 5. Non-goals (YAGNI)

- Not a live/generated-from-cluster diagram (no LikeC4/Structurizr model sync);
  hand-authored D2 is the maintainable unit.
- Not per-app diagrams beyond cosmos-v2 as the reference; the contract (③) is
  what generalizes.
- Not a replacement for the north-star spec or the runbook; this is the visual
  layer that references them.

---

## 6. Success criteria

1. Six D2 sources render cleanly to committed SVG/PNG via `render.sh`.
2. A gov reviewer can trace supply-chain + trust-boundary claims (④②) to controls.
3. An engineer can read ③ and know exactly what a new app must supply.
4. ⑥ is honest: prod-on-Compose and the SP10 gap are unmistakable.
5. Every box traces to a real file or a cited external reference.

---

## 7. References

**Product ecosystem**
- Defense Unicorns UDS — Core overview, functional layers, technical structure;
  LikeC4 diagram sources (`uds-core/docs/.c4/`): https://uds.defenseunicorns.com/reference/uds-core/overview/ · https://docs.defenseunicorns.com/
- DoD Big Bang / Platform One — package architecture (Mermaid `flowchart`,
  `stroke-dasharray` alternatives), draw.io data-flow (numbered arrows keyed to
  protocol/port/encryption), Iron Bank: https://docs-bigbang.dso.mil/latest/ · https://p1.dso.mil/services/iron-bank

**Method & supply chain**
- DoD Enterprise DevSecOps Fundamentals v2.5 + Playbook (Fig. 1 factory
  lifecycle ring; **Fig. 2 unpacked infinity loop w/ ◆ control gates**) +
  Source Diagrams pack: https://dodcio.defense.gov/Portals/0/Documents/Library/DoD%20Enterprise%20DevSecOps%20Fundamentals%20v2.5.pdf · https://dodcio.defense.gov/Portals/0/Documents/Library/DevSecOpsFundamentalsPlaybook.pdf
- SLSA v1.0 — supply-chain model (Source→Build→Package→Consumer), threats A–H,
  Build L0–L3: https://slsa.dev/spec/v1.0/threats · https://slsa.dev/spec/v1.0/levels
- Sigstore/cosign — keyless (OIDC→Fulcio→Rekor→TUF); policy-controller
  admission verify (= control gate): https://docs.sigstore.dev/cosign/signing/overview/ · https://docs.sigstore.dev/policy-controller/overview/

**GitOps / airgap / notation**
- OpenGitOps (CNCF) 4 principles: https://opengitops.dev/ · Flux components
  (Source/Kustomize/Helm/Notification controllers): https://fluxcd.io/flux/components/
- Zarf — "develop connected, deploy disconnected"; init/injector reference:
  https://docs.zarf.dev/ref/init-package/
- C4 model — Landscape→Context→Container→Component, legend+title rule:
  https://c4model.com/ · D2: https://d2lang.com/

**Internal**
- `pontis/docs/specs/2026-06-24-k8s-migration-north-star-design.md` (north star,
  SP1–SP10), `sre-v2/docs/{platform-runbook,app-onboarding,MIGRATION}.md`,
  `sre-v2/docs/specs/app-catalog-round2-design.md`.
