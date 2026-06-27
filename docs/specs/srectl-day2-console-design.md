# srectl monitor — terminal Day-2 / ConMon console

**Status:** Design — approved 2026-06-27
**Scope:** sre-v2 installer (`srectl monitor`)
**Relationship to SP8:** This is the **terminal-native, operator-facing** realization of the SP8 Day-2/ConMon console. SP8 (`/pontis` `docs/specs/2026-06-26-SP8-day2-conmon-console.md`) designs the **app-hosted, org-admin** surface (cosmos `/admin/platform` + `cosmosctl`). The two are complementary surfaces for two audiences (operator vs org-admin); they share the same actuator + audit principles. This spec covers only the `srectl` terminal console.
**Seed:** the current dark-console `srectl monitor` (header/table/footer + 5s refresh + the app-catalog `kubectl` exec-wrapper, packages + apps views) becomes Phase 1's cluster/packages/apps views — no wasted work.

---

## 1. Goal

Make `srectl monitor` a **full-stack, platform-aware observability + Day-2 console** in the terminal: one console where an operator watches and operates the platform from the **OS up through the k8s cluster, the UDS Core services, and the business apps** — mirroring the information and feel of the browser monitoring (Grafana) and absorbing the SP8 Day-2 lifecycle (updates, backup/restore, config, ConMon export). It does not reinvent the platform's data; it **aggregates and surfaces** it (kubectl, metrics-server, Prometheus/Alertmanager) and **acts** through guarded, audited paths.

## 2. Non-goals

- Not the org-admin web console (that is SP8 / cosmos `/admin/platform`).
- Not a Grafana replacement — it surfaces analogous signals (gauges, sparklines, tables), not pixel-identical dashboards.
- Not a generic k8s client — it orchestrates `kubectl` (the established exec-wrapper rule); it never embeds a Kubernetes client library.

## 3. Architecture

### 3.1 Process model

One static Go binary (the existing `srectl`). `srectl monitor` launches a tview application over a **dark console canvas** (the look already shipped), driven by a background refresh loop. All cluster I/O goes through **exec-wrappers** (the app-catalog `Kube`/exec pattern), each a fake-backed interface so the data layer is unit-testable with no cluster.

### 3.2 Data sources (layered)

| Need | Source | Access |
|---|---|---|
| Inventory, status, events, logs, describe, YAML | `kubectl` | exec-wrapper `get/describe/logs -o json` |
| Live CPU/mem (nodes, pods) | metrics-server | `kubectl top` / `kubectl get --raw /apis/metrics.k8s.io/...` |
| Rich + historical + OS-level metrics, alerts | **Prometheus + Alertmanager** | `kubectl get --raw /api/v1/namespaces/monitoring/services/<prom-svc>:9090/proxy/api/v1/query[_range]?query=<PromQL>` |
| Day-2 state | PGO/pgBackRest, Flux, cosign/zarf, install-record ConfigMap, `catalog.yaml` | exec-wrappers (`uds`, `zarf`, `cosign`, `kubectl`) |

The Prometheus path reuses the kube-API proxy, so the monitor needs no extra network egress or credentials beyond the operator's kubeconfig. The exact Prometheus service name is discovered at startup (`kubectl get svc -n monitoring -l app.kubernetes.io/name=prometheus`), not hard-coded.

**Graceful degradation:** every metrics widget has a fallback — if Prometheus is unreachable, gauges fall back to `kubectl top`, sparklines/alerts show a dim "metrics unavailable" state. The console never crashes on a missing data source; it shows the gap.

### 3.3 Trust model (operator surface)

`srectl` is the **operator's** privileged terminal (run on the host with kubeconfig), distinct from the cosmos app console (org-admin, untrusted-relative-to-cluster).

- **Observability** — read-only, always available.
- **Direct k8s/host actions** (restart, scale, cordon/uncordon, rollout restart, backup trigger, config toggle) — `srectl` performs them with the operator's kubeconfig, behind a **typed-confirm gate**, and **audit-logged** to a substrate audit sink (a `srectl-platform-actions` append-only record — k8s ConfigMap/Events on the cluster; the cosmos `platform_actions` hash-chain when reachable). Each record: actor (kubeconfig user), action, target, before/after, timestamp.
- **Updates** (security-critical) — `srectl` does **not** mutate the platform version directly. It routes through the SP8 **signed-approval → privileged-agent → Flux/Zarf** actuator: `srectl` emits the approval token `{target digest, policy snapshot, approver, nonce}`; the out-of-app agent verifies signature + cosign-verifiability + the security floor, then actuates. The operator is trusted to *approve*, not to bypass the verified actuator.

### 3.4 Module layout

```
installer/internal/tui/monitor/
  monitor.go      # the tview app shell: layout, nav controller, refresh loop, view registry
  theme.go        # console palette (dark canvas; shares tui accent/selection/status)
  views/          # one file per view: row/panel builders (pure) + the tview render
    overview.go   cluster.go  host.go  core.go  apps.go  security.go  daytwo.go
  widgets/        # reusable tview primitives (pure data→draw)
    table.go  gauge.go  sparkline.go  stattile.go  drillin.go  confirm.go
  data/           # data layer behind exec-wrappers (fake-backed, unit-tested)
    kube.go       # kubectl: resources, events, describe, logs, top
    prom.go       # Prometheus/Alertmanager via kube-API proxy; PromQL query/query_range
    daytwo.go     # PGO/pgBackRest, Flux, cosign/zarf digests, catalog, audit sink, approvals
installer/cmd/srectl/monitor.go   # cobra wiring (exists)
```

Each `views/*` exposes a pure **panel/row builder** (`data → []row` / `data → panel model`) that is unit-tested; the tview render is smoke-tested. Each `widgets/*` is a small primitive with a unit-tested data→cells/bars function and a manual-smoke draw.

## 4. View model — platform-aware layers

The landing screen is **OVERVIEW**; the rest are grouped by layer. Each layer view is a Table or a dashboard of panels.

```
OVERVIEW   cross-layer health: stat tiles (nodes·pods·ns·alerts) + cluster CPU/mem gauges +
           cpu/mem/net sparklines + per-layer health rollup (✓/⚠/✗) + top alerts + recent events
HOST/OS    node-exporter: per-node cpu·mem·load·filesystem·disk-io·net·uptime·kernel (gauges + table)
CLUSTER    nodes · namespaces · workloads(deploy/sts/ds: ready/desired) · pods(status·restarts·cpu·mem·node)
           · services · pvcs · events                                          (k9s-style tables)
CORE       UDS Core health: Istio · Keycloak · Falco · Prometheus · Grafana up/ready + UDS Packages(phase)
APPS       cosmos + mission apps: install records · workloads · pod health · exposed endpoints · SSO clients
SECURITY   Falco events (severity) · Prometheus alerts (firing) · per-digest signature/SLSA status · audit-chain status
DAY-2      updates (channel·policy·available·approve) · backup/restore(PGO) · config(catalog/SSO) · ConMon export
```

Each resource row carries live status color and, where applicable, live CPU/mem. **Drill-in** (Enter) opens describe / YAML / logs for the selected resource.

## 5. Widgets

Reusable tview primitives in `widgets/` — each a pure data→draw unit:

- **Table** — the workhorse: fixed header, selection bar (white-on-accent), per-cell color, sort, `/` filter, live columns. (Generalizes the shipped monitor table.)
- **Gauge** — horizontal `█` bar for a 0–100% value, colored by threshold (green <70, amber <90, red ≥90), with the numeric label. Used for CPU/mem/disk.
- **Sparkline** — a single-line `▁▂▃▄▅▆▇█` trend from a numeric series (Prometheus `query_range`, last N minutes), with min/max labels.
- **StatTile** — a boxed big-number + label (e.g. `56 pods`), optional accent.
- **HealthRollup** — `✓ 5  ⚠ 1  ✗ 0` per layer, colored.
- **DrillIn** — a scrollable, bordered pane (describe/YAML/logs) over the current view; `Esc` closes.
- **Confirm** — a modal with a typed-name gate for destructive actions (returns confirmed/cancelled).

The widget data functions (e.g. `gaugeCells(pct)`, `sparkBars(series)`, `tableRows(...)`) are unit-tested; rendering is smoke.

## 6. Navigation

k9s-inspired, scaled to many views:

- **Landing** = OVERVIEW.
- **`:` command bar** — `:nodes`, `:pods`, `:pods <ns>`, `:alerts`, `:updates`, `:host`, … (resource/view jump). A small command registry maps names → views.
- **Breadcrumb header** — shows the layer path (`Overview › Cluster › Pods (cosmos)`) + context + refresh indicator.
- **Keys** — `j/k`/arrows move · `/` filter · `Enter` drill-in · `Esc` back/close · `a` actions-on-selection (→ Confirm) · `1–9` quick-jump within a layer · `Tab/⇧Tab` cycle layers · `?` help overlay (full keymap) · `q` quit.
- **Live refresh** — per-view interval (default 5s, `:set refresh <s>`), background goroutine + `QueueUpdateDraw`; a spinner/age indicator shows freshness. Heavy PromQL is rate-limited (cached between ticks).

## 7. Day-2 actions

Actions hang off a selected resource (`a`) or a Day-2 view. Every action: **preview → typed-confirm → execute → audit record → result toast**.

- **k8s actions (P3):** rollout restart, scale (replicas prompt), cordon/uncordon, delete-pod (drain-style). Direct via `kubectl`, operator privilege.
- **Backup/restore (P4):** trigger an on-demand pgBackRest backup, list repos/backups, restore-to-point. **Restore is guarded**: typed-cluster-name confirm; the console enforces the hard rule that restore tests run on a copy (surfaces the target, never an in-place prod restore without an explicit, separately-typed acknowledgement).
- **Config (P5):** toggle optional services in `catalog.yaml`; SSO mode (Keycloak/external/local) where runtime-toggleable; structural changes emit an update (P6) rather than mutating live.
- **Updates (P6):** show channel/policy/available-version; `approve` emits the signed-approval token to the privileged agent (§3.3); show reconcile/rollout status + rollback-on-failed-health. Never a direct version mutation from the TUI.
- **ConMon export (P7):** aggregate the RMF posture artifact (vuln/compliance from Falco/scan, audit-chain integrity, WORM anchor, cosign+SLSA per running digest, patch level vs latest signed, backup posture) → signed JSON (+ PDF where a renderer is available); hash-chained + WORM-archived like the audit log. Maps rows to NIST 800-171 / spec control IDs.

All actions require the corresponding intent and are denied (with a clear message) when the operator's kubeconfig lacks the RBAC; negative paths are tested.

## 8. Metrics layer (Prometheus)

`data/prom.go` wraps the kube-API-proxy PromQL path behind a fake-backed interface:

- `Query(promql) → []sample` (instant) and `QueryRange(promql, dur, step) → series` (for sparklines).
- A small **catalog of PromQL** the views use (cluster/node CPU·mem·disk·net from node-exporter + kube-state-metrics; per-namespace/pod resource usage; firing `ALERTS`). Each query string is a named constant, unit-tested by shape.
- A thin **response parser** (Prometheus JSON `data.result`) → typed samples/series — pure, unit-tested with fixtures.
- **Discovery + degradation:** find the Prometheus service at startup; on any failure, mark the metrics source unavailable and let widgets fall back (gauges → metrics-server; sparklines/alerts → dim placeholder).

## 9. File / test boundaries (the testable seam)

- **Pure + unit-tested:** every `views/*` panel/row builder; every `widgets/*` data function; `data/prom.go` parser + query catalog; `data/daytwo.go` approval-token sign/verify/floor + audit-record shaping; action preview/guard logic.
- **Smoke (manual, on the lab):** all tview rendering; the live refresh loop; the Prometheus proxy round-trip; each Day-2 action against the lab cluster.
- **Reuse:** the app-catalog `Kube`/exec-wrappers + `State`; the shared `tui` accent/selection/status colors; the shipped table/header/footer/refresh.

## 10. Build phasing (one spec, incremental delivery)

Each phase is independently shippable and testable; later phases add views/actions without reworking earlier ones.

- **P1 — Observability core:** the nav framework (command bar, breadcrumb, help, refresh), the OVERVIEW dashboard, and the HOST / CLUSTER / CORE / APPS views with metrics (Prometheus + metrics-server), the gauge/sparkline/stat-tile/table widgets, and drill-in. Read-only. (Absorbs the shipped dark monitor.)
- **P2 — Security + alerts:** SECURITY view — Falco events, Prometheus alerts, per-digest signature/SLSA, audit-chain status.
- **P3 — Day-2 k8s actions:** the action model (`a` → preview → confirm → execute → audit) + restart/scale/cordon/rollout.
- **P4 — Backup/restore:** PGO/pgBackRest trigger/list/restore-guard.
- **P5 — Config:** catalog/SSO toggles.
- **P6 — Updates:** signed-approval → agent → Flux/Zarf, with reconcile/rollback status.
- **P7 — ConMon export:** the RMF posture artifact (signed JSON/PDF, hash-chained + WORM).

## 11. Constraints

- One static binary; orchestrate `kubectl`/`uds`/`zarf`/`cosign` via exec-wrappers — never embed a k8s client.
- Read-only by default; every write is typed-confirm-gated, RBAC-checked, and audited; updates never bypass the signed-approval/verified-actuator path.
- Original code; the dark-console look; titles `SRE Monitor — <version>`; never "Security Onion".
- Keep the data/view/widget builders unit-testable behind tview; rendering + live cluster = manual smoke.
- Commit with the noreply email; PR route (branch → squash-merge) on `JongoDB-Labs/sre-v2`.
