# srectl Day-2 console — Phase 2 (security views) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the SECURITY layer (spec §4/§10 P2): two read-only views — **`alerts`** (firing Prometheus alerts, by severity) and **`falco`** (recent Falco runtime-security events) — so the operator sees what's firing and what Falco caught.

**Architecture:** Pure row-builders (`AlertRows` over Prometheus vector samples; `FalcoRows` over Falco's JSON-lines log output) + a `LogsByLabel` exec-wrapper to pull the Falco daemonset's events. Two new table views registered in the existing string-keyed registry, fetched off the UI goroutine via the established `refresh()`→fetch→`drawTable` pattern. Read-only; no actions.

**Tech Stack:** Go 1.25, tview/tcell, Prometheus (`ALERTS`) via the kube-API proxy, `kubectl logs -l` (Falco JSON output), the P1.x monitor packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1–P1.4 + P3.1–P3.3). Do NOT switch branches.
- **Read-only (binding):** this whole phase is observability — no mutations.
- **Exec-wrapper rule (binding):** Prometheus via the existing `Prom.Query`; Falco via a new `Resources.LogsByLabel` (thin, behind the fake-backed interface). Row-builders (`AlertRows`, `FalcoRows`) are PURE + unit-tested with fixtures.
- **Anti-freeze invariant (binding):** the new fetchers run off the UI goroutine (they're `tableView.fetch` closures invoked from `refresh()`'s background goroutine); only the `drawTable` draw is marshalled. Bound the Falco log call with a timeout.
- **Graceful degrade (binding):** `alerts` shows a notice if Prometheus is unreachable (`m.prom.Ref == ""` or query error); `falco` shows a notice if the log fetch errors or no events parse. Neither blanks the app.
- **Look:** reuse `cell`/`drawTable`/the dark palette; colour severity/priority (critical/Critical→`statusRed`, warning/Warning→`statusAmber`, else `consoleDim`).
- **Confirmed lab data (recon 2026-06-28):** `ALERTS{alertstate="firing"}` → labels `alertname`/`severity`/`namespace` (13 firing: 2 critical, 10 warning, 1 none). Falco daemonset pod (ns `falco`, label `app.kubernetes.io/name=falco`, container `falco`) emits JSON lines with `priority`, `rule`, `time`, `output_fields["k8s.ns.name"]`, `output_fields["k8s.pod.name"]`.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/falco.go` (package `data`) — `FalcoRow` + `FalcoRows([]byte) []FalcoRow` + `logsByLabelArgs(...)` + `Resources.LogsByLabel`.
- `installer/internal/tui/monitor/data/falco_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/prom.go` — `AlertRow` + `AlertRows([]Sample) []AlertRow`.
- `installer/internal/tui/monitor/data/prom_test.go` — `AlertRows` test.
- `installer/internal/tui/monitor/data/kube.go` — extend the `Resources` interface with `LogsByLabel`.
- `installer/internal/tui/monitor/monitor.go` — `fetchAlerts` + `fetchFalco`; register the `alerts`/`falco` views; nav (keys + viewOrder + footer).

> Deferred (noted): per-image signature/SLSA status + audit-chain status (spec §4 SECURITY) — need cosign/the audit data; later P2 slices.

---

## Task 1: data/prom.go — AlertRows

**Files:**
- Modify: `installer/internal/tui/monitor/data/prom.go`, `data/prom_test.go`

**Interfaces:**
- Produces: `type AlertRow struct { Name, Severity, Namespace string }`; `func AlertRows(samples []Sample) []AlertRow` — extracts `alertname`/`severity`/`namespace` labels; sorts by severity rank (critical < warning < everything else) then name then namespace; skips the synthetic `Watchdog`/empty-name. (Reuses the existing `QFiringAlerts` constant + `Prom.Query`.)

- [ ] **Step 1: Write the failing test** — append to `data/prom_test.go`:

```go
func TestAlertRows(t *testing.T) {
	samples := []Sample{
		{Labels: map[string]string{"alertname": "UDSProbeEndpointDown", "severity": "warning", "namespace": "grafana"}},
		{Labels: map[string]string{"alertname": "etcdInsufficientMembers", "severity": "critical", "namespace": "kube-system"}},
		{Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}}, // synthetic → skipped
	}
	got := AlertRows(samples)
	if len(got) != 2 {
		t.Fatalf("want 2 rows (Watchdog skipped), got %d: %+v", len(got), got)
	}
	if got[0].Name != "etcdInsufficientMembers" || got[0].Severity != "critical" {
		t.Fatalf("critical must sort first: %+v", got[0])
	}
	if got[1].Name != "UDSProbeEndpointDown" || got[1].Namespace != "grafana" {
		t.Fatalf("row2 wrong: %+v", got[1])
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestAlertRows -v`
Expected: FAIL — `undefined: AlertRows`.

- [ ] **Step 3: Implement** — add to `data/prom.go` (near the catalog constants / parsers):

```go
// AlertRow is one firing alert for the security view.
type AlertRow struct {
	Name, Severity, Namespace string
}

// severityRank orders alert/event severities (lower = more urgent).
func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical", "emergency", "alert":
		return 0
	case "warning":
		return 1
	case "error", "err":
		return 2
	default:
		return 3
	}
}

// AlertRows reduces a firing-ALERTS vector to rows, skipping the synthetic
// always-on Watchdog and any empty alertname, sorted by severity then name/namespace.
func AlertRows(samples []Sample) []AlertRow {
	rows := make([]AlertRow, 0, len(samples))
	for _, s := range samples {
		name := s.Labels["alertname"]
		if name == "" || name == "Watchdog" {
			continue
		}
		rows = append(rows, AlertRow{Name: name, Severity: s.Labels["severity"], Namespace: s.Labels["namespace"]})
	}
	sort.Slice(rows, func(i, j int) bool {
		if ri, rj := severityRank(rows[i].Severity), severityRank(rows[j].Severity); ri != rj {
			return ri < rj
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Namespace < rows[j].Namespace
	})
	return rows
}
```

(`sort` and `strings` are already imported in prom.go.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/prom.go`
Expected: PASS; gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/prom.go installer/internal/tui/monitor/data/prom_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): AlertRows — firing alerts by severity for the security view"
```

---

## Task 2: data/falco.go — FalcoRows + LogsByLabel

**Files:**
- Create: `installer/internal/tui/monitor/data/falco.go`, `data/falco_test.go`
- Modify: `installer/internal/tui/monitor/data/kube.go` (extend the `Resources` interface)

**Interfaces:**
- Produces: `type FalcoRow struct { Time, Priority, Rule, Namespace, Pod string }`; `func FalcoRows(raw []byte) []FalcoRow` (parses Falco JSON lines, newest-first, caps at 50, skips non-JSON lines); `func logsByLabelArgs(namespace, selector, container string, tail int) []string`; `Resources.LogsByLabel(namespace, selector, container string, tail int) ([]byte, error)`.

- [ ] **Step 1: Write the failing test** — `data/falco_test.go` (fixture = two real-shape Falco JSON lines + a junk line):

```go
package data

import "testing"

const falcoLines = `{"priority":"Notice","rule":"Run shell untrusted","time":"2026-06-27T00:00:12.834189113Z","output_fields":{"k8s.ns.name":"cosmos","k8s.pod.name":"cosmos-pg-instance1-m9hm-0"}}
not-json-garbage-line
{"priority":"Warning","rule":"Terminal shell in container","time":"2026-06-27T01:18:14.911096955Z","output_fields":{"k8s.ns.name":"default","k8s.pod.name":"app-xyz"}}`

func TestFalcoRows(t *testing.T) {
	got := FalcoRows([]byte(falcoLines))
	if len(got) != 2 {
		t.Fatalf("want 2 rows (junk skipped), got %d: %+v", len(got), got)
	}
	// newest-first: the Warning at 01:18 comes before the Notice at 00:00
	if got[0].Rule != "Terminal shell in container" || got[0].Priority != "Warning" {
		t.Fatalf("newest-first wrong: %+v", got[0])
	}
	if got[0].Namespace != "default" || got[0].Pod != "app-xyz" {
		t.Fatalf("k8s fields wrong: %+v", got[0])
	}
	if got[1].Rule != "Run shell untrusted" || got[1].Namespace != "cosmos" {
		t.Fatalf("row2 wrong: %+v", got[1])
	}
}

func TestLogsByLabelArgs(t *testing.T) {
	got := logsByLabelArgs("falco", "app.kubernetes.io/name=falco", "falco", 200)
	want := []string{"logs", "-n", "falco", "-l", "app.kubernetes.io/name=falco", "-c", "falco", "--tail", "200"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("logsByLabelArgs[%d]: got %q want %q (full %v)", i, got[i], want[i], got)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run 'TestFalcoRows|TestLogsByLabelArgs' -v`
Expected: FAIL — `undefined: FalcoRows`.

- [ ] **Step 3: Implement** — create `data/falco.go`:

```go
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// falcoMax caps how many Falco events the view shows (newest first).
const falcoMax = 50

// FalcoRow is one Falco runtime-security event for the security view.
type FalcoRow struct {
	Time, Priority, Rule, Namespace, Pod string
}

// falcoEvent is the subset of Falco's JSON output we surface.
type falcoEvent struct {
	Priority string `json:"priority"`
	Rule     string `json:"rule"`
	Time     string `json:"time"`
	Fields   struct {
		Namespace string `json:"k8s.ns.name"`
		Pod       string `json:"k8s.pod.name"`
	} `json:"output_fields"`
}

// FalcoRows parses Falco JSON-lines output (skipping non-JSON lines), returns the
// most recent falcoMax events newest-first. Time is shortened to HH:MM:SS when parseable.
func FalcoRows(raw []byte) []FalcoRow {
	lines := strings.Split(string(raw), "\n")
	rows := make([]FalcoRow, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" || ln[0] != '{' {
			continue
		}
		var e falcoEvent
		if err := json.Unmarshal([]byte(ln), &e); err != nil || e.Rule == "" {
			continue
		}
		t := e.Time
		if parsed, perr := time.Parse(time.RFC3339Nano, e.Time); perr == nil {
			t = parsed.Format("15:04:05")
		}
		rows = append(rows, FalcoRow{Time: t, Priority: e.Priority, Rule: e.Rule,
			Namespace: e.Fields.Namespace, Pod: e.Fields.Pod})
	}
	// Reverse to newest-first (Falco logs are oldest→newest).
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if len(rows) > falcoMax {
		rows = rows[:falcoMax]
	}
	return rows
}

// logsByLabelArgs builds `kubectl logs -n <ns> -l <selector> -c <container> --tail <n>`.
func logsByLabelArgs(namespace, selector, container string, tail int) []string {
	return []string{"logs", "-n", namespace, "-l", selector, "-c", container, "--tail", fmt.Sprintf("%d", tail)}
}

// LogsByLabel returns logs from pods matching a label selector (e.g. the Falco DS).
func (execResources) LogsByLabel(namespace, selector, container string, tail int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detailTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", logsByLabelArgs(namespace, selector, container, tail)...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl logs -l %s: %w", selector, err)
	}
	return out, nil
}
```

Then extend the `Resources` interface in `kube.go` with:

```go
	LogsByLabel(namespace, selector, container string, tail int) ([]byte, error)
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/falco.go internal/tui/monitor/data/falco_test.go`
Expected: PASS (Falco + LogsByLabel arg tests + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/falco.go installer/internal/tui/monitor/data/falco_test.go installer/internal/tui/monitor/data/kube.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Falco event parser + LogsByLabel exec-wrapper"
```

---

## Task 3: monitor — register the alerts + falco security views

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `data.QFiringAlerts`/`Prom.Query`/`data.AlertRows`; `m.res.LogsByLabel`/`data.FalcoRows`; the `tableView` registry, `tableResult`, `cell`, `statusRed`/`statusAmber`/`consoleDim`, `m.prom`.

- [ ] **Step 1: Add the two fetchers + a severity cell helper**

```go
// falco discovery constants (the UDS Core Falco daemonset).
const (
	falcoNS       = "falco"
	falcoSelector = "app.kubernetes.io/name=falco"
	falcoContainer = "falco"
)

// sevCell colours a severity/priority: critical→red, warning→amber, else dim.
func sevCell(s string) *tview.TableCell {
	c := tview.NewTableCell(s + "  ")
	switch strings.ToLower(s) {
	case "critical", "emergency", "alert":
		return c.SetTextColor(statusRed)
	case "warning":
		return c.SetTextColor(statusAmber)
	default:
		return c.SetTextColor(consoleDim)
	}
}

func (m *monitor) fetchAlerts() tableResult {
	if m.prom.Ref == "" {
		return tableResult{title: "ALERTS", notice: "metrics unavailable (Prometheus unreachable)"}
	}
	samples, err := m.prom.Query(data.QFiringAlerts)
	if err != nil {
		return tableResult{title: "ALERTS", notice: "error: " + err.Error(), isError: true}
	}
	rows := data.AlertRows(samples)
	res := tableResult{title: "ALERTS"}
	if len(rows) == 0 {
		res.notice = "no alerts firing"
		return res
	}
	res.cols = []string{"ALERT", "SEVERITY", "NAMESPACE"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Name), sevCell(r.Severity), cell(r.Namespace)})
	}
	return res
}

func (m *monitor) fetchFalco() tableResult {
	raw, err := m.res.LogsByLabel(falcoNS, falcoSelector, falcoContainer, 200)
	if err != nil {
		return tableResult{title: "FALCO", notice: "error: " + err.Error(), isError: true}
	}
	rows := data.FalcoRows(raw)
	res := tableResult{title: "FALCO"}
	if len(rows) == 0 {
		res.notice = "no recent Falco events"
		return res
	}
	res.cols = []string{"TIME", "PRIORITY", "RULE", "NAMESPACE", "POD"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Time), sevCell(r.Priority), cell(r.Rule), cell(r.Namespace), cell(r.Pod)})
	}
	return res
}
```

- [ ] **Step 2: Register the views + nav**

In `Run`, add to the `tableViews` map: `"alerts": {fetch: m.fetchAlerts}`, `"falco": {fetch: m.fetchFalco}`. Append them to `m.viewOrder` (after services, before packages/apps): `…, "services", "alerts", "falco", "packages", "apps"`. Add number-key cases `'7'` → `setView("alerts")` and `'8'` → `setView("falco")` in the input capture (alongside 0–6). Add a compact `7 alerts  8 falco` to `footerText()` (or, if the footer is already long, rely on the `:` command bar — the implementer chooses, but at minimum the views must be reachable via `:alerts`/`:falco` and Tab).

- [ ] **Step 3: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): security views — alerts + falco (registered, nav)"
```

---

## Task 4: Lab smoke (controller-driven)

- [ ] **Step 1: Cross-compile + deliver** (`tmux kill-server`, not `pkill -f srectl`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive the two security views**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 150 -y 44; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon '7'; sleep 2; echo "=== ALERTS (key 7) ==="; tmux capture-pane -t mon -p | sed -n '1,12p'
tmux send-keys -t mon '8'; sleep 2.5; echo "=== FALCO (key 8) ==="; tmux capture-pane -t mon -p | sed -n '1,12p'
tmux send-keys -t mon ':'; sleep 0.3; tmux send-keys -t mon 'alerts' Enter; sleep 1.5; echo "=== :alerts jump ==="; tmux capture-pane -t mon -p | sed -n '1,4p'
tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
EOF
```
Expected (controller-verified): the **ALERTS** view shows the firing alerts severity-sorted (etcdInsufficientMembers `critical` first, then the `warning` UDSProbeEndpointDown rows by namespace); the **FALCO** view shows recent events newest-first (TIME · PRIORITY · RULE · NAMESPACE · POD — e.g. the "Run shell untrusted" Notice from cosmos); `:alerts` jumps. No mutation; both read-only.

- [ ] **Step 3: Record the smoke outcome in the ledger** (note any bug + fix before the final review; no user ping).

---

## Self-Review

**1. Spec coverage (P2 SECURITY §4/§10):** firing alerts (severity) → Tasks 1+3; Falco events → Tasks 2+3. Deferred + noted: per-image signature/SLSA + audit-chain status (need cosign/audit data; later P2 slices).

**2. Placeholder scan:** Tasks 1–2 ship full code + real-shape fixtures (the Falco JSON + the alert labels from the recon). Task 3 wires two fetchers (off-UI, degrade-handling) into the registry, build+suite-gated, smoke-proven in Task 4. No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `AlertRows([]Sample)` (Task 1) + `FalcoRows([]byte)`/`Resources.LogsByLabel` (Task 2) consumed by `fetchAlerts`/`fetchFalco` (Task 3). The fetchers return `tableResult` and run via the existing off-UI `refresh()`→`tableView.fetch`→`drawTable` path (anti-freeze preserved); `m.prom` read is set-once (same pattern as `fetchOverview`). `sevCell` colours both alert severity and Falco priority. The `alerts`/`falco` views join `m.tableViews` + `m.viewOrder`; keys `7`/`8` + the `:` command bar reach them. Degrade: `alerts` notice on no-Prom/empty; `falco` notice on error/empty.
