# srectl Day-2 console — Phase 3 slice 2 (scale action) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a **Scale** Day-2 action for deployments/statefulsets — `a` → Scale → a **replicas input prompt** → execute `kubectl scale` → audit → result. This introduces the input-prompt modal on top of P3.1's action framework.

**Architecture:** A pure `scaleArgs` builder + `Resources.Scale` (slice-1 pattern). The action catalog gains a `needsReplicas` marker action for deploy/sts; the action menu routes such actions to a new **input modal** (a centered `tview.Form` with a digits-only replicas field); on Confirm it validates N, builds the concrete scale action, and reuses P3.1's `executePending` (off-UI exec → audit → result). Scale is reversible (scale back up/down), so it uses a simple input+confirm gate (no typed-name gate; that's for destructive actions in a later slice).

**Tech Stack:** Go 1.25, tview/tcell (`tview.Form` + `tview.Grid` to center), kubectl `scale`, the P3.1 action framework.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1–P1.4 + P3.1). Do NOT switch branches.
- **SAFETY (binding):** scale still passes through a confirm gate — the **input modal's "Scale" button is the deliberate confirm**; no scale executes without the operator entering a value and pressing Scale. Reuse P3.1's `executePending` so the mutation runs OFF the UI goroutine and is **audited** (success + failure). Lab-only e2e; never defcon/prod.
- **Exec-wrapper rule (binding):** `scaleArgs` is PURE + unit-tested; `Resources.Scale` is a thin wrapper via `runAction` (the P3.1 helper).
- **Validation (binding):** the replicas input is digits-only (input filter) AND re-validated as a non-negative integer before execute; an invalid/empty value is a no-op (do not execute).
- **Fresh-modal rule (binding — the P3.1 lesson):** build a FRESH form/modal per step (no in-place reconfigure carrying focus). Reuse the `showModal` helper for the menu/confirm/result; the input modal is a fresh `tview.Form` each time.
- **Scope:** ONLY scale (deploy/sts). Daemonsets are NOT scalable (`kubectl scale ds` errors) → Scale must NOT be offered for `daemonsets`. No delete/drain/destructive action here.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — `scaleArgs(kind, namespace, name string, replicas int) []string` + `Resources.Scale(kind, namespace, name string, replicas int) (string, int, error)`.
- `installer/internal/tui/monitor/data/kube_test.go` — `scaleArgs` test.
- `installer/internal/tui/monitor/monitor.go` — `action.needsReplicas` field; `actionsFor` offers Scale for deploy/sts; the menu routes `needsReplicas` → `showScaleInput`; `showScaleInput` (the input modal) builds the concrete scale action + reuses `executePending`.

---

## Task 1: data/kube.go — scaleArgs + Resources.Scale

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `Resources.Scale(kind, namespace, name string, replicas int) (string, int, error)` backed by pure `scaleArgs(kind, namespace, name string, replicas int) []string` → `["scale", kind, "-n", namespace, name, "--replicas=<n>"]`.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
func TestScaleArgs(t *testing.T) {
	got := scaleArgs("deployments", "cosmos", "cosmos", 3)
	want := []string{"scale", "deployments", "-n", "cosmos", "cosmos", "--replicas=3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("scaleArgs: got %v want %v", got, want)
	}
	if got := scaleArgs("statefulsets", "cosmos", "cosmos-pg", 0); got[len(got)-1] != "--replicas=0" {
		t.Fatalf("scaleArgs replicas=0: %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestScaleArgs -v`
Expected: FAIL — `undefined: scaleArgs`.

- [ ] **Step 3: Implement** — append to `data/kube.go` (near the other action arg-builders):

```go
// scaleArgs builds `kubectl scale <kind> -n <ns> <name> --replicas=<n>`.
func scaleArgs(kind, namespace, name string, replicas int) []string {
	return []string{"scale", kind, "-n", namespace, name, fmt.Sprintf("--replicas=%d", replicas)}
}

// Scale sets a workload's replica count.
func (execResources) Scale(kind, namespace, name string, replicas int) (string, int, error) {
	return runAction(scaleArgs(kind, namespace, name, replicas))
}
```

Extend the `Resources` interface with `Scale(kind, namespace, name string, replicas int) (string, int, error)`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/kube.go`
Expected: PASS (the scale test + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): scale arg-builder + Resources.Scale"
```

---

## Task 2: monitor — offer Scale for deploy/sts + route it to the input modal

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: the `action` struct, `actionsFor`, the menu modal's done-func (from P3.1).
- Produces: `action` gains a `needsReplicas bool` field; `actionsFor` returns a Scale marker action for `deployments`/`statefulsets` (NOT `daemonsets`); the menu's done-func routes `act.needsReplicas` → `m.showScaleInput(act)` (Task 3) instead of `m.showConfirm(act)`.

- [ ] **Step 1: Add the field + offer Scale**

Add `needsReplicas bool` to the `action` struct. In `actionsFor`, split the workload case so deploy/sts get a Scale marker in addition to Rollout restart, and daemonsets do not:

```go
case "deployments", "statefulsets":
	return []action{
		{label: "Rollout restart", auditAction: "rollout-restart",
			kind: dt.kind, namespace: dt.namespace, name: dt.name,
			command: fmt.Sprintf("kubectl rollout restart %s -n %s %s", dt.kind, dt.namespace, dt.name),
			preview: fmt.Sprintf("Rollout-restart %s %s/%s?\n\nCycles its pods with a rolling update.", dt.kind, dt.namespace, dt.name),
			exec:    func() (string, int, error) { return m.res.RolloutRestart(dt.kind, dt.namespace, dt.name) }},
		{label: "Scale", auditAction: "scale", needsReplicas: true,
			kind: dt.kind, namespace: dt.namespace, name: dt.name},
	}
case "daemonsets":
	return []action{
		{label: "Rollout restart", auditAction: "rollout-restart",
			kind: dt.kind, namespace: dt.namespace, name: dt.name,
			command: fmt.Sprintf("kubectl rollout restart %s -n %s %s", dt.kind, dt.namespace, dt.name),
			preview: fmt.Sprintf("Rollout-restart %s %s/%s?\n\nCycles its pods with a rolling update.", dt.kind, dt.namespace, dt.name),
			exec:    func() (string, int, error) { return m.res.RolloutRestart(dt.kind, dt.namespace, dt.name) }},
	}
```

(The Scale marker has no `exec`/`preview`/`command` yet — Task 3's input modal builds the concrete action with the entered replica count.)

- [ ] **Step 2: Route needsReplicas in the menu done-func**

In `openActions`, where the menu's done-func currently does `m.showConfirm(acts[i])`, route the marker:

```go
if acts[i].needsReplicas {
	m.showScaleInput(acts[i])
	return
}
m.showConfirm(acts[i])
```

- [ ] **Step 3: Build + suite (showScaleInput not defined yet — add a temporary stub to compile, replaced in Task 3)**

To keep this task compiling on its own, add a minimal stub `func (m *monitor) showScaleInput(a action) { m.closeModal() }` (Task 3 replaces it). Then:

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): offer Scale for deploy/sts + route to input modal (stub)"
```

---

## Task 3: monitor — the replicas input modal

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.root`, `m.modalActive`, `m.pending`, `executePending`, `closeModal`, `m.res.Scale`, the `action` struct.
- Produces: the real `showScaleInput(a action)` (replacing the Task-2 stub).

- [ ] **Step 1: Implement showScaleInput (a fresh centered Form modal)**

Replace the stub with:

```go
// showScaleInput prompts for a replica count, then runs the scale via the shared
// executePending path (off-UI + audited). A FRESH form each call (P3.1 focus lesson).
func (m *monitor) showScaleInput(a action) {
	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField("Replicas", "", 6, func(textToCheck string, lastChar rune) bool {
		return lastChar >= '0' && lastChar <= '9' // digits only
	}, nil)
	form.AddButton("Scale", func() {
		text := form.GetFormItem(0).(*tview.InputField).GetText()
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 {
			return // invalid/empty → no-op (operator can correct or Cancel)
		}
		scaled := a
		scaled.command = fmt.Sprintf("kubectl scale %s -n %s %s --replicas=%d", a.kind, a.namespace, a.name, n)
		scaled.preview = fmt.Sprintf("Scale %s/%s to %d?", a.name, a.namespace, n)
		scaled.exec = func() (string, int, error) { return m.res.Scale(a.kind, a.namespace, a.name, n) }
		m.pending = scaled
		m.executePending() // shows running… → off-UI scale → audit → result
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetBorder(true).SetTitle(fmt.Sprintf(" Scale %s/%s ", a.kind, a.name)).SetTitleColor(consoleText)
	form.SetButtonsAlign(tview.AlignCenter)
	// Center the form over "main" with a Grid (transparent margins).
	grid := tview.NewGrid().SetColumns(0, 44, 0).SetRows(0, 9, 0).AddItem(form, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}
```

Ensure `strconv` is imported in monitor.go (add it if not). The `scaled.auditAction` is already "scale" (from the marker), so the audit records `action:"scale"` with the N-bearing `command`.

- [ ] **Step 2: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green. Adapt `tview.Form`/`Grid` API specifics to the installed version if needed.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): replicas input modal for the Scale action"
```

---

## Task 4: Lab smoke (controller-driven — reversible: scale up then back)

> Controller drives on the LAB only. Pick a workload safe to scale (a Deployment with no strict singleton requirement). Scale it UP by one, verify, then scale back to the original — net-zero.

- [ ] **Step 1: Cross-compile + deliver** (free the binary via `tmux kill-server`, never `pkill -f srectl`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Identify a safe deployment + its current replicas**, then drive `5` (workloads) → select that row → `a` → Scale → type the new count → Scale button → verify `kubectl get deploy <x>` shows the new replicas + an audit `action:"scale"` entry with the right `--replicas=N` command. Then scale it back to the original count the same way. Confirm both audit entries and the workload returns to its starting replica count. (Choose the target by capturing the workloads view first; prefer a deployment like `grafana`/`authservice` that tolerates a brief 2-replica state on a 1-node cluster, or scale a 1→1 no-op if nothing is safe to actually change — but a real 1→2→1 is the better proof.)

- [ ] **Step 3: Record the smoke outcome in the ledger.** Note any bug found + fix before the final review. (No user ping — the user drives their own full e2e later.)

---

## Self-Review

**1. Spec coverage (P3 §7 scale):** scale with a replicas prompt → Task 1 (data) + 2 (offer/route) + 3 (input modal); reuses the audited execute path → Task 3 via `executePending`. Daemonsets correctly excluded. Deferred + noted: delete/drain + the typed-name gate (later slice); backup/config/updates (P4–P6).

**2. Placeholder scan:** Task 1 ships full code + a test. Task 2 ships the field + the catalog split + the route, with a compiling stub for `showScaleInput`. Task 3 replaces the stub with the real input modal. The mutation reuses the P3.1-reviewed `executePending` (off-UI + audited). No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `Resources.Scale(kind,namespace,name,replicas)` (Task 1) called by the `scaled.exec` closure in `showScaleInput` (Task 3). `action.needsReplicas` (Task 2) read by `openActions`'s route (Task 2) → `showScaleInput` (Task 3). `showScaleInput` builds a concrete `action` (command/preview/exec) from the marker + N, sets `m.pending`, and calls `executePending` (P3.1) — so the audit records `action:"scale"` with the N-bearing command, on the off-UI path, gated by the operator pressing "Scale". The fresh-form-per-call + `showModal`/`closeModal`/`modalActive` discipline matches P3.1.
