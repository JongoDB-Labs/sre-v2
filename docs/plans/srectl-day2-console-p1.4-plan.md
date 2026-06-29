# srectl Day-2 console — Phase 1.4 (dashboard enrichment) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the OVERVIEW feel like a real dashboard — wire the (already-stubbed) CPU/MEM **sparkline trends** to live Prometheus range-queries, and add a **disk gauge**, a **load-average** stat, and a **pod-phase breakdown** (Running/Pending/Failed/Succeeded). All from one cohesive Prometheus data path.

**Architecture:** Three new PromQL constants + one pure parser (`PodPhaseCounts`) in `data/prom.go`. The overview's `Inputs` gains `DiskPct`/`Load`/`PodPhases`; `BuildOverview` renders the new panels (reusing the existing `Bar`/`Spark` widgets). `fetchOverview` (already off the UI goroutine) populates them best-effort — a failed enrichment query leaves its panel empty without blanking the dashboard.

**Tech Stack:** Go 1.25, tview/tcell, Prometheus via the kube-API proxy (`Prom.Query`/`Prom.QueryRange`), the P1.1 `widgets`/`views` packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1–P1.3). Do NOT switch branches.
- **Exec-wrapper rule (binding):** Prometheus is queried only through the existing `Prom.Query`/`Prom.QueryRange` (fake-backed `Raw`); never a new client. Parsing logic (`PodPhaseCounts`) is PURE + unit-tested.
- **Anti-freeze invariant (binding):** the new queries run inside the EXISTING `fetchOverview` (which `refresh()` already calls in a background goroutine); only the draw is marshalled via `QueueUpdateDraw`. Add NO blocking call on the UI goroutine.
- **Graceful degrade (binding):** disk/load/phase/series are BEST-EFFORT enrichments — if a query errors or returns empty, leave that panel absent (zero/empty) and still render the rest. Do NOT let an enrichment failure flip the whole dashboard to "metrics unavailable" (only the core CPU/MEM/alerts failure does that, as in P1.1). The CPU/MEM **gauges** stay as today; the sparklines are additive.
- **Read-only.** **Look:** dark console palette; reuse `widgets.Bar` (gauge) + `widgets.Spark` (sparkline) + the established `BuildOverview` markup style; keep it uncluttered and aligned.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

## Confirmed lab PromQL (recon 2026-06-27)

- Disk %: `100 * (1 - sum(node_filesystem_avail_bytes{mountpoint="/"}) / sum(node_filesystem_size_bytes{mountpoint="/"}))` → 27.6 (sum-based, matches the existing `QNodeMemPct` style; root fs is ext4 on `/`).
- Load: `avg(node_load1)` → 0.43.
- Pod phases: `sum by (phase) (kube_pod_status_phase)` → vector with a `phase` label per sample (Running 44, Succeeded 12, Pending 0, Failed 0, Unknown 0).
- CPU/MEM series: the existing `QNodeCPUSeries`/`QNodeMemSeries` via `QueryRange` (31 points over 30m at 60s step — confirmed matrix data).

---

## File Structure

**Modify:**
- `installer/internal/tui/monitor/data/prom.go` — add `QNodeDiskPct`/`QNodeLoad`/`QPodPhase` constants + `PodPhaseCounts(samples []Sample) map[string]int`.
- `installer/internal/tui/monitor/data/prom_test.go` — `PodPhaseCounts` test.
- `installer/internal/tui/monitor/views/overview.go` — extend `Inputs` (`DiskPct`/`Load float64`, `PodPhases map[string]int`) + render the disk gauge, sparklines, load + pod-phase line in `BuildOverview`.
- `installer/internal/tui/monitor/views/overview_test.go` — assert the new panels render.
- `installer/internal/tui/monitor/monitor.go` — `fetchOverview` populates the new fields (range + instant queries), best-effort.

> Deferred (noted): the standalone **events** view (lab has 0 events) and a standalone **host/OS resource view** — the host metrics most worth seeing (disk/load) are folded into the dashboard here.

---

## Task 1: data/prom.go — disk/load/phase PromQL + PodPhaseCounts

**Files:**
- Modify: `installer/internal/tui/monitor/data/prom.go`, `data/prom_test.go`

**Interfaces:**
- Produces: consts `QNodeDiskPct`, `QNodeLoad`, `QPodPhase` (strings); `func PodPhaseCounts(samples []Sample) map[string]int` — maps each sample's `phase` label to `int(value)` (skips samples with an empty `phase` label).

- [ ] **Step 1: Write the failing test** — append to `data/prom_test.go`:

```go
func TestPodPhaseCounts(t *testing.T) {
	samples := []Sample{
		{Labels: map[string]string{"phase": "Running"}, Value: 44},
		{Labels: map[string]string{"phase": "Succeeded"}, Value: 12},
		{Labels: map[string]string{"phase": "Pending"}, Value: 0},
		{Labels: map[string]string{"phase": ""}, Value: 7}, // no phase label → skipped
	}
	got := PodPhaseCounts(samples)
	if got["Running"] != 44 || got["Succeeded"] != 12 || got["Pending"] != 0 {
		t.Fatalf("counts wrong: %+v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty-phase sample must be skipped: %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestPodPhaseCounts -v`
Expected: FAIL — `undefined: PodPhaseCounts`.

- [ ] **Step 3: Implement** — in `data/prom.go`, add to the PromQL `const` block (next to `QNodeCPUPct` etc.):

```go
	// QNodeDiskPct is root-filesystem usage %, sum-based across nodes (matches QNodeMemPct).
	QNodeDiskPct = `100 * (1 - sum(node_filesystem_avail_bytes{mountpoint="/"}) / sum(node_filesystem_size_bytes{mountpoint="/"}))`
	// QNodeLoad is the cluster-average 1-minute load.
	QNodeLoad = `avg(node_load1)`
	// QPodPhase is the pod count grouped by lifecycle phase (kube-state-metrics).
	QPodPhase = `sum by (phase) (kube_pod_status_phase)`
```

and add the parser (near `ParseVector`):

```go
// PodPhaseCounts reduces a `sum by (phase) (kube_pod_status_phase)` vector to a
// phase→count map. Samples without a phase label are skipped.
func PodPhaseCounts(samples []Sample) map[string]int {
	out := make(map[string]int, len(samples))
	for _, s := range samples {
		phase := s.Labels["phase"]
		if phase == "" {
			continue
		}
		out[phase] = int(s.Value)
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS (the new test + all existing data tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/prom.go installer/internal/tui/monitor/data/prom_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): disk/load/pod-phase PromQL + PodPhaseCounts parser"
```

---

## Task 2: views/overview.go — render disk gauge, sparklines, load + pod-phase

**Files:**
- Modify: `installer/internal/tui/monitor/views/overview.go`, `views/overview_test.go`

**Interfaces:**
- Consumes: the existing `Inputs` struct + `BuildOverview`; `widgets.Bar(pct float64, width int) string`; `widgets.Spark(vals []float64) string`.
- Produces: `Inputs` gains `DiskPct float64`, `Load float64`, `PodPhases map[string]int` (the existing `CPUSeries`/`MemSeries []float64` are now actually populated by Task 3). `BuildOverview` renders them.

This task is rendering — read the current `overview.go` and `overview_test.go` first to match the established markup/layout style (dark-console color tags, the `Bar`/`Tile`/`Health` usage, the section ordering).

- [ ] **Step 1: Write the failing test** — append to `views/overview_test.go`:

```go
func TestBuildOverview_Enriched(t *testing.T) {
	in := Inputs{
		Nodes: 1, Pods: 56, Namespaces: 22, Packages: 6,
		CPUPct: 43, MemPct: 10, DiskPct: 27.6, Load: 0.43,
		CPUSeries: []float64{5, 9, 12, 20, 43}, MemSeries: []float64{8, 9, 10, 10, 10},
		PodPhases:  map[string]int{"Running": 44, "Succeeded": 12, "Pending": 0, "Failed": 0},
		LayerHealth: [3]int{6, 0, 0}, MetricsOK: true,
	}
	out := BuildOverview(in)
	for _, want := range []string{"DISK", "Load", "0.43", "44", "running"} {
		if !strings.Contains(out, want) {
			t.Fatalf("overview missing %q\n%s", want, out)
		}
	}
	// a sparkline renders at least one block glyph from the CPU series
	if !strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Fatalf("overview missing sparkline glyphs\n%s", out)
	}
}

func TestBuildOverview_DegradeNoEnrichment(t *testing.T) {
	// metrics down → no DISK panel, no sparkline glyphs, no panic
	out := BuildOverview(Inputs{Nodes: 1, Pods: 56, MetricsOK: false})
	if strings.Contains(out, "DISK") || strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Fatalf("degraded overview must omit enrichment panels\n%s", out)
	}
}
```

(If `overview_test.go` doesn't yet import `strings`, add it.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/views/ -run TestBuildOverview_Enriched -v`
Expected: FAIL — `unknown field 'DiskPct'` (Inputs not yet extended) or missing-substring.

- [ ] **Step 3: Implement**

(a) Extend the `Inputs` struct with:
```go
	DiskPct   float64
	Load      float64
	PodPhases map[string]int
```

(b) In `BuildOverview`, gated on `in.MetricsOK` (same gate the CPU/MEM gauges use), add — in the established style and section order:
- a **DISK** gauge line mirroring the CPU/MEM gauge lines: `DISK  ` + `widgets.Bar(in.DiskPct, <same width as CPU/MEM>)` + a `%` readout (e.g. `fmt.Sprintf("%.0f%%", in.DiskPct)`).
- **sparklines**: after (or beside) the CPU and MEM gauge lines, render `widgets.Spark(in.CPUSeries)` and `widgets.Spark(in.MemSeries)` — but ONLY when the series is non-empty (`if len(in.CPUSeries) > 0 { … }`), so a failed range-query just omits the trend.
- a **stats line**: `Load ` + `fmt.Sprintf("%.2f", in.Load)` + `   Pods ` + the phase breakdown from `in.PodPhases`, e.g. `fmt.Sprintf("%d running · %d pending · %d failed · %d done", in.PodPhases["Running"], in.PodPhases["Pending"], in.PodPhases["Failed"], in.PodPhases["Succeeded"])`. (Use lowercase `running`/`pending`/`failed`/`done` labels; the test asserts `"running"` and `"44"`.)

Keep the existing tiles / CPU-MEM gauges / health / alerts. The degraded path (`!in.MetricsOK`) must NOT render the DISK gauge or sparklines (the test asserts their absence). Nil-safe: `in.PodPhases` may be nil → `in.PodPhases["Running"]` on a nil map returns 0 (safe in Go), so no guard needed, but only render the Load/Pods line under the `MetricsOK` gate.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/views/ -v && gofmt -l internal/tui/monitor/views/overview.go`
Expected: PASS (both new tests + existing overview tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/views/overview.go installer/internal/tui/monitor/views/overview_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): overview disk gauge + CPU/MEM sparklines + load/pod-phase panel"
```

---

## Task 3: monitor.go — populate the enrichment in fetchOverview

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `data.QNodeDiskPct`/`QNodeLoad`/`QPodPhase`/`QNodeCPUSeries`/`QNodeMemSeries`, `data.PodPhaseCounts`, `Prom.Query`/`Prom.QueryRange`, the existing `fetchOverview(prom data.Prom) views.Inputs` + `firstValue`.

This is integration — read the current `fetchOverview` first. It already (P1.1) queries CPU/MEM/alerts under `if prom.Ref == "" { MetricsOK=false } else { … }` and sets `in.MetricsOK`. You ADD the enrichment inside that same `else` block, AFTER the existing CPU/MEM/alerts handling, best-effort.

- [ ] **Step 1: Add the enrichment queries (best-effort) in fetchOverview**

Inside the `else` branch (where `prom.Ref != ""`), after the existing `in.CPUPct/in.MemPct/in.AlertNames` handling, add:

```go
	// Best-effort enrichments: a failure leaves the panel empty, it does NOT
	// flip MetricsOK (the core CPU/MEM/alerts above own that).
	if disk, err := prom.Query(data.QNodeDiskPct); err == nil {
		in.DiskPct = firstValue(disk)
	}
	if load, err := prom.Query(data.QNodeLoad); err == nil {
		in.Load = firstValue(load)
	}
	if phases, err := prom.Query(data.QPodPhase); err == nil {
		in.PodPhases = data.PodPhaseCounts(phases)
	}
	// CPU/MEM trend sparklines: last 30 minutes at 1-minute resolution.
	end := time.Now().Unix()
	start := end - 1800
	const step = int64(60)
	if s, err := prom.QueryRange(data.QNodeCPUSeries, start, end, step); err == nil && len(s) > 0 {
		in.CPUSeries = s[0].Values
	}
	if s, err := prom.QueryRange(data.QNodeMemSeries, start, end, step); err == nil && len(s) > 0 {
		in.MemSeries = s[0].Values
	}
```

(`time` is already imported in monitor.go. `firstValue` already exists. This code runs on the background fetch goroutine — `fetchOverview` is called from `refresh()`'s goroutine — so it does not touch the UI goroutine.)

- [ ] **Step 2: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -5`
Expected: build clean; gofmt clean; full suite green.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): populate overview disk/load/phase + CPU/MEM range-series"
```

---

## Task 4: Lab smoke (manual)

- [ ] **Step 1: Cross-compile + deliver** (free the bastion binary via `tmux kill-server` — do NOT `pkill -f srectl`, it self-matches the remote shell)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Capture the enriched OVERVIEW** (overview is the default view)

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 142 -y 44; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
echo "===== OVERVIEW (enriched) ====="; tmux capture-pane -t mon -p | sed -n '1,22p'
tmux send-keys -t mon q; sleep 1
tmux kill-server 2>/dev/null || true
EOF
```
Expected (manual): the OVERVIEW now shows the stat tiles, CPU/MEM gauges EACH WITH a sparkline trend, a DISK gauge (~28%), a `Load 0.43   Pods 44 running · 0 pending · 0 failed · 12 done` line, the health rollup, and the firing-alerts list — a visibly denser dashboard. Degrades cleanly if Prometheus is unreachable (no disk/sparkline panels, no panic).

- [ ] **Step 3: PING the user** to drive it (`ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor`) — the OVERVIEW (default `0`) should read like a real dashboard now: CPU/MEM with trend sparklines, disk, load, and the pod-phase breakdown.

---

## Self-Review

**1. Spec coverage (dashboard-enrichment slice of the Day-2 console §4 overview / §5 widgets):** sparkline trends (the P1.1 `CPUSeries`/`MemSeries` stub) → Tasks 1/3 (range-query wiring) + 2 (render); disk/load host metrics folded into the dashboard → Tasks 1–3; pod-phase observability → Tasks 1–3. Deferred + noted: standalone events view, standalone host/OS resource view.

**2. Placeholder scan:** Task 1 ships full code + a real-shape test. Task 2 ships the Inputs additions + render intent + two tests (enriched + degrade), integrating with the read-first existing `BuildOverview`. Task 3 is concrete best-effort query wiring, build+suite-gated, smoke-proven in Task 4. No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `QNodeDiskPct`/`QNodeLoad`/`QPodPhase` + `PodPhaseCounts(samples) map[string]int` (Task 1) consumed by `fetchOverview` (Task 3). `Inputs.DiskPct`/`Load`/`PodPhases` + the already-present `CPUSeries`/`MemSeries` (Task 2) populated by Task 3 and rendered by `BuildOverview` (Task 2). `firstValue` + `Prom.Query`/`QueryRange` (P1.1) reused. The `MetricsOK` gate governs both the existing gauges and the new panels; enrichment failures are best-effort (no `MetricsOK` flip). `fetchOverview` runs on the existing background goroutine (anti-freeze preserved).
