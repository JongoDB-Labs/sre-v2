# srectl Day-2 console — Phase 3 slice 3 (delete + typed-name gate) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a **destructive Delete** action for workloads, gated by a **typed-name confirm** (spec §3.4): the operator must type the resource's exact name to confirm — the strongest gate, for irreversible actions.

**Architecture:** A pure `deleteArgs` + `Resources.Delete` (slice-1 pattern). The action catalog gains a `needsTypedName` Delete marker for deploy/sts/ds; the menu routes it to a new **typed-name confirm modal** (a `tview.Form` whose "Delete" button executes ONLY when the typed text exactly equals the resource name). On match it builds the concrete delete action and reuses the audited `executePending`. A name mismatch is a no-op (no delete).

**Tech Stack:** Go 1.25, tview/tcell (`tview.Form`), kubectl `delete`, the P3.1/P3.2 action framework.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1–P1.4 + P3.1 + P3.2). Do NOT switch branches.
- **SAFETY — typed-name gate (binding):** Delete executes ONLY when the operator types the resource's EXACT name (`strings.TrimSpace(typed) == a.name`) and presses Delete. Any mismatch (incl. empty) is a no-op (modal stays). This is the irreversible-action gate; there is NO other path to delete.
- **Reuse the audited path (binding):** the delete runs through the existing `executePending` (off-UI goroutine + `auditor.Record` before result). Do NOT write a new exec/audit path.
- **Exec-wrapper rule (binding):** `deleteArgs` is PURE + unit-tested; `Resources.Delete` is a thin `runAction` wrapper.
- **Scope:** Delete for WORKLOADS only (deployments/statefulsets/daemonsets). NO node/pvc/namespace delete; NO drain (untestable on a 1-node lab). Pods keep their non-destructive Restart (no separate Delete-pod here).
- **Fresh form per call (binding — P3.1/P3.2 lesson):** a fresh `tview.Form` each call; danger styling (red title) to signal irreversibility.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — `deleteArgs(kind, namespace, name string) []string` + `Resources.Delete(kind, namespace, name string) (string, int, error)`.
- `installer/internal/tui/monitor/data/kube_test.go` — `deleteArgs` test.
- `installer/internal/tui/monitor/monitor.go` — `action.needsTypedName`; `actionsFor` offers Delete for workloads; the menu routes `needsTypedName` → `showTypedConfirm`; `showTypedConfirm` (the typed-name modal) builds the concrete delete action + reuses `executePending`.

---

## Task 1: data/kube.go — deleteArgs + Resources.Delete

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `Resources.Delete(kind, namespace, name string) (string, int, error)` backed by pure `deleteArgs(kind, namespace, name string) []string` → `["delete", kind, "-n", namespace, name]`.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
func TestDeleteArgs(t *testing.T) {
	got := deleteArgs("deployments", "default", "smoke-target")
	want := []string{"delete", "deployments", "-n", "default", "smoke-target"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("deleteArgs: got %v want %v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestDeleteArgs -v`
Expected: FAIL — `undefined: deleteArgs`.

- [ ] **Step 3: Implement** — append to `data/kube.go` (near the other action arg-builders):

```go
// deleteArgs builds `kubectl delete <kind> -n <ns> <name>` (the generic delete used
// by the destructive Delete action; deletePodArgs stays the pod-restart variant).
func deleteArgs(kind, namespace, name string) []string {
	return []string{"delete", kind, "-n", namespace, name}
}

// Delete removes a namespaced resource.
func (execResources) Delete(kind, namespace, name string) (string, int, error) {
	return runAction(deleteArgs(kind, namespace, name))
}
```

Extend the `Resources` interface with `Delete(kind, namespace, name string) (string, int, error)`.

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/kube.go`
Expected: PASS (the delete test + all existing data tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): generic delete arg-builder + Resources.Delete"
```

---

## Task 2: monitor — offer Delete for workloads + route to the typed-name modal

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: the `action` struct (with `needsReplicas` from slice 2), `actionsFor`, `openActions`'s menu done-func.
- Produces: `action.needsTypedName bool`; `actionsFor` appends a Delete marker to the workload cases (deploy/sts AND daemonsets); the menu routes `needsTypedName` → `m.showTypedConfirm` (Task 3) — ordered BEFORE the `needsReplicas` branch.

- [ ] **Step 1: Add the field + offer Delete**

Add `needsTypedName bool` to the `action` struct. In `actionsFor`, append a Delete marker to BOTH workload cases. The deploy/sts case becomes `[Rollout restart, Scale, Delete]`; the daemonsets case becomes `[Rollout restart, Delete]`:

```go
// (deploy/sts case) — append after the Scale marker:
		{label: "Delete", auditAction: "delete", needsTypedName: true,
			kind: dt.kind, namespace: dt.namespace, name: dt.name},
// (daemonsets case) — append after the Rollout-restart action:
		{label: "Delete", auditAction: "delete", needsTypedName: true,
			kind: dt.kind, namespace: dt.namespace, name: dt.name},
```

(The Delete marker has no exec/preview/command — Task 3's typed-name modal builds the concrete action.)

- [ ] **Step 2: Route the marker** in `openActions`'s done-func — add the `needsTypedName` branch FIRST (before `needsReplicas`):

```go
if acts[i].needsTypedName {
	m.showTypedConfirm(acts[i])
	return
}
if acts[i].needsReplicas {
	m.showScaleInput(acts[i])
	return
}
m.showConfirm(acts[i])
```

- [ ] **Step 3: Compiling stub** (replaced in Task 3):

```go
// showTypedConfirm is implemented in slice-3 Task 3 (typed-name confirm modal).
func (m *monitor) showTypedConfirm(a action) { m.closeModal() }
```

- [ ] **Step 4: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; suite green. (No mutation this task — the stub closes; the Delete marker isn't executed.)

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): offer Delete for workloads + route to typed-name modal (stub)"
```

---

## Task 3: monitor — the typed-name confirm modal

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.root`, `m.modalActive`, `m.pending`, `executePending`, `closeModal`, `m.res.Delete`, `consoleBg`, `statusRed`, the `action` struct.
- Produces: the real `showTypedConfirm(a action)` (replacing the Task-2 stub).

- [ ] **Step 1: Implement showTypedConfirm**

Replace the stub with:

```go
// showTypedConfirm is the typed-name gate for a destructive action (spec §3.4): the
// operator must type the resource's exact name to confirm. On match it runs the
// action via the shared executePending (off-UI + audited). Fresh form per call.
func (m *monitor) showTypedConfirm(a action) {
	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField(fmt.Sprintf("Type \"%s\" to confirm", a.name), "", 40, nil, nil)
	form.AddButton("Delete", func() {
		typed := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if typed != a.name {
			return // name mismatch (incl. empty) → no delete; operator can correct or Cancel
		}
		del := a
		del.command = fmt.Sprintf("kubectl delete %s -n %s %s", a.kind, a.namespace, a.name)
		del.preview = fmt.Sprintf("Delete %s %s/%s", a.kind, a.namespace, a.name)
		del.exec = func() (string, int, error) { return m.res.Delete(a.kind, a.namespace, a.name) }
		m.pending = del
		m.executePending()
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetBorder(true).
		SetTitle(fmt.Sprintf(" ⚠ Delete %s/%s ", a.kind, a.name)).
		SetTitleColor(statusRed)
	form.SetButtonsAlign(tview.AlignCenter)
	// Center the form over "main" with a Grid (transparent margins).
	grid := tview.NewGrid().SetColumns(0, 56, 0).SetRows(0, 11, 0).AddItem(form, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}
```

(`strings` + `consoleBg`/`statusRed` are already in monitor.go. `del.auditAction` is "delete" from the marker, so the audit records `action:"delete"` with the kind/ns/name + command.)

- [ ] **Step 2: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green. Adapt `tview.Form`/`Grid` API to the installed version if needed.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): typed-name confirm modal for the destructive Delete action"
```

---

## Task 4: Lab smoke (controller-driven — throwaway fixture, SAFE)

> Controller drives on the LAB only. Use a self-created THROWAWAY deployment in an early-sorting namespace so it's row 1 of the workloads view and nothing real is touched. The typed-name gate means a delete only fires when the EXACT name is typed.

- [ ] **Step 1: Cross-compile + deliver + create the fixture** (free the binary via `tmux kill-server`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'kubectl create ns aaa-srectl-smoke 2>/dev/null; kubectl label ns aaa-srectl-smoke istio-injection- pod-security.kubernetes.io/enforce=privileged --overwrite 2>/dev/null; kubectl create deployment smoke-target --image=registry.k8s.io/pause:3.9 -n aaa-srectl-smoke; kubectl get deploy -n aaa-srectl-smoke'
```

- [ ] **Step 2: Drive — verify (a) wrong name BLOCKS, (b) exact name DELETES.** `5` (workloads, row 1 = `aaa-srectl-smoke/smoke-target`) → `a` → Delete (3rd button: Tab Tab from Rollout-restart, or navigate) → typed-name modal. First type a WRONG name → Delete button → confirm the deployment is STILL there (mismatch blocked). Then `a` → Delete again → type the EXACT name `smoke-target` → Delete → result "✓ delete / deployment.apps/smoke-target deleted" → verify `kubectl get deploy -n aaa-srectl-smoke` shows it GONE + an audit `action:"delete"` entry. (Capture the modal title — it should be the red `⚠ Delete deployments/smoke-target`.)

- [ ] **Step 3: Clean up the fixture**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'kubectl delete ns aaa-srectl-smoke --wait=false 2>/dev/null; echo cleaned'
```

- [ ] **Step 4: Record the smoke outcome in the ledger** (note any bug + fix before the final review; no user ping).

---

## Self-Review

**1. Spec coverage (P3 §3.4 typed-name gate + §7 destructive delete):** the typed-name confirm gate → Task 3; Delete for workloads → Tasks 1+2; audited via `executePending` → Task 3. Deferred + noted: node-drain (untestable on 1-node), pvc/namespace delete, force-delete-pod; backup/config/updates (P4–P6).

**2. Placeholder scan:** Task 1 ships full code + a test. Task 2 ships the field + the markers + the route, with a compiling stub. Task 3 replaces the stub with the real typed-name modal. The mutation reuses the safety-reviewed `executePending`. No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `Resources.Delete(kind,namespace,name)` (Task 1) called by `del.exec` in `showTypedConfirm` (Task 3). `action.needsTypedName` (Task 2) routed FIRST in `openActions` → `showTypedConfirm` (Task 3). `showTypedConfirm` executes ONLY on `typed == a.name` (the typed-name gate), builds the concrete action, sets `m.pending`, calls `executePending` (P3.1, off-UI + audited) → audit records `action:"delete"`. The route order (needsTypedName → needsReplicas → plain confirm) keeps each marker on its own path; the Delete marker (nil exec) never reaches `showConfirm`/`executePending` directly. Fresh form + `showModal`/`closeModal`/`modalActive` discipline matches P3.1/P3.2.
