# srectl Day-2 console — Phase 1.3 (drill-in detail pane) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Enter`-to-drill-in: from any resource row (nodes/pods/workloads/services), open a scrollable detail pane showing `kubectl describe`, with one-key switching to YAML (`get -o yaml`) and, for pods, `logs` — the "why is this thing unhealthy?" investigation step.

**Architecture:** The `data` layer gains three pure kubectl-arg builders (unit-tested) plus three thin exec-wrapper methods on the existing `Resources` interface (`Describe`/`Yaml`/`Logs`). The monitor attaches a `drillTarget{kind,namespace,name}` reference to each resource row; `Enter` on a row opens a `detail` page (a scrollable TextView in the existing `main` Pages) and fetches the describe text OFF the UI goroutine; `d`/`y`/`l` switch modes; `Esc`/`q` return to the table. All fetches obey the P1.1 anti-freeze rule.

**Tech Stack:** Go 1.25, tview/tcell, kubectl (`describe` / `get -o yaml` / `logs`), the P1.2 `data`/monitor packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1 + P1.2). Do NOT switch branches.
- **Exec-wrapper rule (binding):** kubectl is orchestrated via the fake-backed `Resources` interface — never an embedded client. The arg-building logic is PURE and unit-tested; the exec methods are thin wrappers bounded by a timeout.
- **Anti-freeze invariant (binding — the P1.1 rule):** cluster I/O NEVER runs on the tview UI goroutine. The drill fetch (describe/yaml/logs) runs in a background goroutine; only the `detail.SetText` draw is marshalled via `QueueUpdateDraw`, guarded by a staleness check so a late fetch for a closed/replaced detail is dropped.
- **Read-only:** describe / get -o yaml / logs are all reads. No mutations.
- **Look:** the detail pane uses the dark console palette (`consoleBg`/`consoleText`/`consoleDim`); raw kubectl text (no tview color-tag interpretation — set `SetDynamicColors(false)` so `[...]` in YAML/logs is literal).
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — add `describeArgs`/`yamlArgs`/`logsArgs` (pure) + `detailTimeout`/`logsTailLines` consts; extend the `Resources` interface + `execResources` with `Describe`/`Yaml`/`Logs`.
- `installer/internal/tui/monitor/data/kube_test.go` — tests for the three arg-builders.
- `installer/internal/tui/monitor/monitor.go` — `drillTarget` type; row references in the four resource fetchers; the `detail` page + `openDetail`/drill fetch; `Enter` to drill, `d`/`y`/`l` to switch, `Esc`/`q` to close; footer hints.

> Deferred (noted, not in this plan): the **events** view (the lab currently has 0 events → can't be smoke-proven; revisit when there's event data), the **host/OS** node-exporter panel, and overview **sparkline** range-queries.

---

## Task 1: data/kube.go — describe/yaml/logs arg-builders + Resources methods

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces (consumed by monitor Tasks 3–4): three methods on the `Resources` interface — `Describe(kind, namespace, name string) (string, error)`, `Yaml(kind, namespace, name string) (string, error)`, `Logs(namespace, name string, tail int) (string, error)`. Internally backed by pure helpers `describeArgs`/`yamlArgs`/`logsArgs` returning the `kubectl` argument slice. A node has `namespace == ""` (cluster-scoped → no `-n`).

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
func TestDescribeArgs(t *testing.T) {
	if got := describeArgs("pods", "cosmos", "cosmos-abc"); !reflect.DeepEqual(got, []string{"describe", "pods", "-n", "cosmos", "cosmos-abc"}) {
		t.Fatalf("namespaced: got %v", got)
	}
	if got := describeArgs("nodes", "", "cosmos-k8s"); !reflect.DeepEqual(got, []string{"describe", "nodes", "cosmos-k8s"}) {
		t.Fatalf("cluster-scoped: got %v", got)
	}
}

func TestYamlArgs(t *testing.T) {
	if got := yamlArgs("services", "istio", "gw"); !reflect.DeepEqual(got, []string{"get", "services", "-n", "istio", "gw", "-o", "yaml"}) {
		t.Fatalf("namespaced: got %v", got)
	}
	if got := yamlArgs("nodes", "", "cosmos-k8s"); !reflect.DeepEqual(got, []string{"get", "nodes", "cosmos-k8s", "-o", "yaml"}) {
		t.Fatalf("cluster-scoped: got %v", got)
	}
}

func TestLogsArgs(t *testing.T) {
	if got := logsArgs("cosmos", "cosmos-abc", 200); !reflect.DeepEqual(got, []string{"logs", "-n", "cosmos", "cosmos-abc", "--tail", "200"}) {
		t.Fatalf("got %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestDescribeArgs|TestYamlArgs|TestLogsArgs' -v`
Expected: FAIL — `undefined: describeArgs` (etc.).

- [ ] **Step 3: Implement** — append to `data/kube.go`:

```go
// detailTimeout bounds describe/yaml/logs shell-outs (slightly longer than the
// list timeout: describe and a tailed log can be a touch slower than a get).
const detailTimeout = 8 * time.Second

// logsTailLines is how many trailing log lines the detail pane fetches.
const logsTailLines = 200

// describeArgs builds `kubectl describe <kind> [-n ns] <name>`. A cluster-scoped
// resource (node) has ns == "" and omits the namespace flag.
func describeArgs(kind, namespace, name string) []string {
	args := []string{"describe", kind}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name)
}

// yamlArgs builds `kubectl get <kind> [-n ns] <name> -o yaml`.
func yamlArgs(kind, namespace, name string) []string {
	args := []string{"get", kind}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name, "-o", "yaml")
}

// logsArgs builds `kubectl logs -n <ns> <name> --tail <tail>` (pods only).
func logsArgs(namespace, name string, tail int) []string {
	args := []string{"logs"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	return append(args, name, "--tail", fmt.Sprintf("%d", tail))
}

// runDetail runs `kubectl <args...>` bounded by detailTimeout, returning combined
// output (stderr is informative on a describe/logs failure).
func runDetail(args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), detailTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Describe returns `kubectl describe` text for a resource.
func (execResources) Describe(kind, namespace, name string) (string, error) {
	return runDetail(describeArgs(kind, namespace, name))
}

// Yaml returns the resource's manifest via `kubectl get -o yaml`.
func (execResources) Yaml(kind, namespace, name string) (string, error) {
	return runDetail(yamlArgs(kind, namespace, name))
}

// Logs returns the last `tail` log lines of a pod.
func (execResources) Logs(namespace, name string, tail int) (string, error) {
	return runDetail(logsArgs(namespace, name, tail))
}
```

Then extend the `Resources` interface (find its declaration) to:

```go
// Resources runs read-only kubectl against the cluster. Tests inject a fake.
type Resources interface {
	Get(args ...string) ([]byte, error)
	Describe(kind, namespace, name string) (string, error)
	Yaml(kind, namespace, name string) (string, error)
	Logs(namespace, name string, tail int) (string, error)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS — the 3 new arg-builder tests plus all existing data tests (the `execResources` methods compile against the extended interface).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): describe/yaml/logs detail fetchers + arg-builders"
```

---

## Task 2: monitor — attach drillTarget references to resource rows

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Produces (consumed by Tasks 3–4): `type drillTarget struct { kind, namespace, name string }` where `kind` is the kubectl resource (`nodes`/`pods`/`deployments`/`statefulsets`/`daemonsets`/`services`). Each resource-view row carries a `drillTarget` as the reference on its FIRST cell (via `.SetReference(dt)`), so `Enter` can read it back off the selected row.

- [ ] **Step 1: Add the type + set references in the four fetchers**

Add near the other monitor types:

```go
// drillTarget identifies the resource a table row points at, for Enter-to-drill.
// namespace == "" means cluster-scoped (node). kind is the kubectl resource name.
type drillTarget struct {
	kind, namespace, name string
}
```

Then in each resource fetcher, attach the reference to the first cell of each row. Concretely:
- `fetchNodes`: the first cell is `cell(r.Name)`; change to `cell(r.Name).SetReference(drillTarget{kind: "nodes", name: r.Name})`.
- `fetchPods`: first cell `cell(r.Namespace)` — attach to it: `cell(r.Namespace).SetReference(drillTarget{kind: "pods", namespace: r.Namespace, name: r.Name})`.
- `fetchWorkloads`: the loop has `s.arg` (the kubectl resource, e.g. "deployments") in scope per kind — attach to the first cell: `cell(r.Namespace).SetReference(drillTarget{kind: <the kubectl resource for this row>, namespace: r.Namespace, name: r.Name})`. (Carry the resource alongside each row: when appending rows for spec `s`, set `drillTarget{kind: s.arg, …}`. If the current structure builds `all` across kinds before making cells, extend `WorkloadRow` handling so each row remembers its `s.arg`, OR build cells inside the per-spec loop where `s.arg` is in scope — the latter is simpler: move row-cell construction into the per-spec loop.)
- `fetchServices`: `cell(r.Namespace).SetReference(drillTarget{kind: "services", namespace: r.Namespace, name: r.Name})`.

(`SetReference` returns the `*tview.TableCell`, so it chains. The reference is read back in Task 3 via `m.table.GetCell(row,0).GetReference()`.)

- [ ] **Step 2: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; gofmt clean; suite green (no behaviour change yet — references are inert until Task 3 reads them).

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): tag resource rows with drillTarget references"
```

---

## Task 3: monitor — detail page + Enter-to-drill (describe) + close

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `drillTarget` (Task 2), `m.res.Describe` (Task 1), the `main` Pages, the anti-freeze `QueueUpdateDraw` pattern.
- Produces (consumed by Task 4): `m.detail *tview.TextView`; `m.drill drillTarget` + `m.drillMode string` (current mode: "describe"/"yaml"/"logs") + `m.inDetail bool`; `openDetail(dt drillTarget)` and `drawDetail()` (fetches the current mode off-UI and sets `m.detail`).

- [ ] **Step 1: Add the detail TextView to the struct + the `main` Pages**

Add to the `monitor` struct: `detail *tview.TextView`, `drill drillTarget`, `drillMode string`, `inDetail bool`. In `Run`, build it and add it as a third page of `main`:

```go
detail := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetWrap(false)
detail.SetTextColor(consoleText).SetBackgroundColor(consoleBg)
// after m is built: m.detail = detail
main := tview.NewPages().
	AddPage("overview", overviewTV, true, true).
	AddPage("table", table, true, false).
	AddPage("detail", detail, true, false)
```

- [ ] **Step 2: openDetail + drawDetail (off-UI fetch)**

```go
// openDetail enters the detail pane for a row's target, defaulting to describe.
func (m *monitor) openDetail(dt drillTarget) {
	m.drill = dt
	m.drillMode = "describe"
	m.inDetail = true
	m.detail.SetText("  loading…").ScrollToBeginning()
	m.main.SwitchToPage("detail")
	m.app.SetFocus(m.detail)
	m.setHeader(detailTitle(dt, "describe"), 0)
	m.drawDetail()
}

// drawDetail fetches the current drill mode OFF the UI goroutine and sets the
// detail text on it (anti-freeze: only the SetText draw is marshalled back, and
// it's dropped if the user has since left this target/mode).
func (m *monitor) drawDetail() {
	dt := m.drill
	mode := m.drillMode
	go func() {
		var text string
		var err error
		switch mode {
		case "yaml":
			text, err = m.res.Yaml(dt.kind, dt.namespace, dt.name)
		case "logs":
			text, err = m.res.Logs(dt.namespace, dt.name, logsTailLines)
		default:
			text, err = m.res.Describe(dt.kind, dt.namespace, dt.name)
		}
		if err != nil && text == "" {
			text = "error: " + err.Error()
		}
		m.app.QueueUpdateDraw(func() {
			if !m.inDetail || m.drill != dt || m.drillMode != mode {
				return
			}
			m.detail.SetText(text).ScrollToBeginning()
		})
	}()
}

// detailTitle is the header label for a drill view, e.g. "PODS/cosmos-abc · describe".
func detailTitle(dt drillTarget, mode string) string {
	return fmt.Sprintf("%s/%s · %s", strings.ToUpper(dt.kind), dt.name, mode)
}

// closeDetail returns from the detail pane to the table.
func (m *monitor) closeDetail() {
	m.inDetail = false
	m.main.SwitchToPage("table")
	m.app.SetFocus(m.main)
	m.refresh() // restore the table header/count for the current view
}
```

- [ ] **Step 3: Wire Enter (open) + Esc/q (close) in the input capture**

At the TOP of the input-capture function, AFTER the existing `if m.app.GetFocus() == m.cmdBar { return ev }` guard, add a detail-mode block that runs before the normal hotkeys:

```go
if m.inDetail {
	switch ev.Rune() {
	case 'q':
		m.closeDetail()
		return nil
	}
	if ev.Key() == tcell.KeyEscape {
		m.closeDetail()
		return nil
	}
	return ev // let the detail TextView handle scrolling (arrows/PgUp/PgDn/j/k via tview)
}
```

(Place this so that when NOT in detail, the existing behaviour is untouched.) Then add `Enter` handling for the table views — in the `switch ev.Key()` block add:

```go
case tcell.KeyEnter:
	if m.view != "overview" {
		row, _ := m.table.GetSelection()
		if c := m.table.GetCell(row, 0); c != nil {
			if dt, ok := c.GetReference().(drillTarget); ok {
				m.openDetail(dt)
			}
		}
	}
	return nil
```

(Guard `m.view != "overview"` because the overview page has no table rows. Packages rows carry a `packageRow` reference, not a `drillTarget`, so the type assertion safely fails there — no drill on packages/apps, which is correct for this slice.)

- [ ] **Step 4: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; gofmt clean; suite green. Adapt tview TextView/Pages API specifics against the installed version if needed (rendering-only).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Enter-to-drill detail pane (describe) + close"
```

---

## Task 4: monitor — d/y/l mode switching + footer

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.drill`/`m.drillMode`/`drawDetail`/`detailTitle`/`m.inDetail` (Task 3).

- [ ] **Step 1: Add d/y/l handling inside the `m.inDetail` block**

Extend the `if m.inDetail { … }` block's rune switch (from Task 3) so `d`/`y`/`l` re-fetch the same target in a new mode (logs only for pods); the header updates to match:

```go
if m.inDetail {
	switch ev.Rune() {
	case 'q':
		m.closeDetail()
		return nil
	case 'd':
		m.setDrillMode("describe")
		return nil
	case 'y':
		m.setDrillMode("yaml")
		return nil
	case 'l':
		if m.drill.kind == "pods" {
			m.setDrillMode("logs")
		}
		return nil
	}
	if ev.Key() == tcell.KeyEscape {
		m.closeDetail()
		return nil
	}
	return ev
}
```

Add the helper:

```go
// setDrillMode switches the detail pane to a new mode and re-fetches (off-UI).
func (m *monitor) setDrillMode(mode string) {
	if m.drillMode == mode {
		return
	}
	m.drillMode = mode
	m.detail.SetText("  loading…").ScrollToBeginning()
	m.setHeader(detailTitle(m.drill, mode), 0)
	m.drawDetail()
}
```

- [ ] **Step 2: Detail-mode footer**

Give the detail pane its own footer hint. Add a helper and call it on open/close so the hotkey bar reflects context:

```go
// detailFooter is the hotkey bar shown while drilled into a resource.
func detailFooter(podLogs bool) string {
	logs := ""
	if podLogs {
		logs = "[#FFFFFF::b]l[-:-:-] [#7C8694]logs[-]   "
	}
	return "  [#FFFFFF::b]d[-:-:-] [#7C8694]describe[-]   [#FFFFFF::b]y[-:-:-] [#7C8694]yaml[-]   " + logs +
		"[#FFFFFF::b]j/k[-:-:-] [#7C8694]scroll[-]   [#FFFFFF::b]q/Esc[-:-:-] [#7C8694]back[-]"
}
```

In `openDetail`, after switching the page, set `m.footer.SetText(detailFooter(dt.kind == "pods"))`; in `closeDetail`, restore `m.footer.SetText(footerText())`. (This requires `m.footer *tview.TextView` to be a struct field — if it isn't already, add it and assign `m.footer = footer` in `Run`; the footer currently lives behind the `bottom` Pages "footer" page, so keep that wiring and just retain a handle to set its text.)

- [ ] **Step 3: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): drill-in d/y/l mode switching + detail footer"
```

---

## Task 5: Lab smoke (manual)

- [ ] **Step 1: Cross-compile + deliver**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
```

- [ ] **Step 2: Drive the drill-in in tmux**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-session -t mon 2>/dev/null || true
tmux new-session -d -s mon -x 140 -y 44; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 3
tmux send-keys -t mon '4'; sleep 2                 # pods view
tmux send-keys -t mon Down Down Enter; sleep 2.5   # drill into a pod → describe
echo "=== DRILL: describe (header + first lines) ==="; tmux capture-pane -t mon -p | sed -n '1,14p'
tmux send-keys -t mon 'y'; sleep 2.5               # → yaml
echo "=== DRILL: yaml ==="; tmux capture-pane -t mon -p | sed -n '1,6p'
tmux send-keys -t mon 'l'; sleep 2.5               # → logs (pod only)
echo "=== DRILL: logs ==="; tmux capture-pane -t mon -p | sed -n '1,12p'
echo "=== FOOTER (detail-mode) ==="; tmux capture-pane -t mon -p | sed -n '44p'
tmux send-keys -t mon 'q'; sleep 1.5               # back to pods table
echo "=== BACK to PODS table ==="; tmux capture-pane -t mon -p | sed -n '1,4p'
tmux send-keys -t mon 'q'; sleep 1                 # quit
tmux kill-session -t mon 2>/dev/null || true
EOF
```
Expected (manual): drilling a pod shows its **describe** (Name/Namespace/… + Events), `y` shows the **YAML manifest** (apiVersion/kind/metadata…), `l` shows the **last log lines**, the detail footer shows `d describe · y yaml · l logs · j/k scroll · q/Esc back`, `q` returns to the PODS table, and a second `q` quits to the shell.

- [ ] **Step 3: PING the user** to drive it interactively (`ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor`): `4` → arrow to a pod → `Enter` → `d`/`y`/`l` → `q` back → also try drilling a node (`3`→Enter→`d`/`y`, no logs) and a service (`6`→Enter).

---

## Self-Review

**1. Spec coverage (drill-in slice of the Day-2 console §5 widgets / §4 views):** the detail/drill-in widget (describe/YAML/logs) → Tasks 1 (fetchers) + 3–4 (UI); applies to all four resource views via the row `drillTarget` (Task 2). Deferred + noted: events view, host/OS panel, sparkline range-queries.

**2. Placeholder scan:** Task 1 ships full code + tests for the pure arg-builders. Tasks 2–4 are concrete integration edits with code, build+gofmt+suite-gated, smoke-proven in Task 5. tview API specifics adapt against the installed version (rendering-only). No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `Resources.Describe/Yaml/Logs(kind,namespace,name…)` (Task 1) called by `drawDetail` (Task 3). `drillTarget{kind,namespace,name}` (Task 2) read back in the `Enter` handler (Task 3) and used for logs-gating `kind == "pods"` (Task 4). `m.detail`/`m.drill`/`m.drillMode`/`m.inDetail` (Task 3) consumed by `setDrillMode`/the `m.inDetail` input block (Task 4). The anti-freeze staleness guard (`!m.inDetail || m.drill != dt || m.drillMode != mode`) mirrors the P1.1/P1.2 `if m.view != view { return }` pattern. `kind` values are the kubectl resources set by the fetchers (`nodes`/`pods`/`deployments`/`statefulsets`/`daemonsets`/`services`) — consumed directly by the arg-builders.
