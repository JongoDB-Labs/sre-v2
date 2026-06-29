# srectl Day-2 console — Phase 3 (Day-2 k8s actions, slice 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give the monitor its first **Day-2 actions** — from a selected resource, press `a` to **restart** a pod, **rollout-restart** a workload, or **cordon/uncordon** a node, each behind a **confirm modal** and written to an **audit log**. This is the spec's operator-trust action model (§3.3, §7): direct, audited, confirm-gated k8s actions.

**Architecture:** Pure kubectl arg-builders + thin exec methods on the `Resources` interface (`DeletePod`/`RolloutRestart`/`SetCordon`, each returning output+exit). A new `data/audit.go` shapes + appends an append-only audit record. The monitor adds an **action catalog** (resource kind → applicable actions) and a single reconfigurable `tview.Modal` overlay that walks **action-menu → confirm → result**; the execute step runs OFF the UI goroutine (the P1.1 anti-freeze rule) and records the audit entry.

**Tech Stack:** Go 1.25, tview/tcell (`tview.Modal`), kubectl (`delete pod` / `rollout restart` / `cordon`/`uncordon`), the P1.x monitor packages.

## Design (operator-trust action model — from `docs/specs/srectl-day2-console-design.md` §3.3/§6/§7)

- **`a` = actions-on-selection.** On a resource row, `a` opens an action modal listing the actions applicable to that resource kind. Selecting one → a **confirm** modal (preview of exactly what will run) → **execute** → **audit record** → **result** modal. `Esc`/Cancel aborts at any step. (Spec §6 key `a`; §7 "preview → confirm → execute → audit → result".)
- **Direct + audited.** `srectl` performs these with the operator's kubeconfig (spec §3.3). Every action is recorded: actor (kubeconfig user), action, target (kind/ns/name), command, exit code, ok, timestamp.
- **Slice-1 scope = reversible actions, simple confirm.** Pod **Restart** (delete → controller recreates), Workload **Rollout restart**, Node **Cordon/Uncordon**. These are reversible/recreated, so a simple Confirm/Cancel gate suffices. **Deferred to slice 2** (need a typed-name gate or an input prompt, per spec §3.4 Confirm "typed-name gate for destructive"): **scale** (replicas prompt), **delete-pod-as-destructive**, **drain**, and any workload/PVC deletion. **Deferred (later phases):** backup/restore (P4), config (P5), updates via signed-approval (P6) — NOT in this slice; `srectl` never mutates platform version directly.
- **Audit sink (slice 1):** an operator-local append-only **JSONL file** (`$XDG_STATE_HOME/srectl/platform-actions.jsonl`, default `~/.local/state/srectl/...`), behind an `Auditor` interface. **NOTE / next:** the spec's preferred sink is a substrate **ConfigMap/Events** record (`srectl-platform-actions`) + the cosmos `platform_actions` hash-chain when reachable — a follow-up that swaps the `Auditor` implementation; the interface is introduced here to make that clean.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1–P1.4). Do NOT switch branches.
- **SAFETY (binding):** EVERY mutating action MUST pass through the confirm modal before executing — there is NO un-confirmed mutation path. The execute step only runs on an explicit Confirm. Slice 1 ships ONLY the reversible actions above; do not add delete/scale/drain here.
- **Exec-wrapper rule (binding):** kubectl is orchestrated via the fake-backed `Resources` interface. The arg-builders are PURE + unit-tested; the exec methods are thin wrappers bounded by `actionTimeout`.
- **Anti-freeze invariant (binding — the P1.1 rule):** the action EXEC runs in a background goroutine; only the modal/result draw is marshalled via `QueueUpdateDraw`. While a modal is active, the background refresh is paused (the P1.3 lesson: a modal that shares the header must pause/guard the refresh). No blocking I/O on the UI goroutine.
- **Audit (binding):** every executed action (success OR failure) is recorded via the `Auditor` before the result modal is shown.
- **Look:** `tview.Modal` over the dark console; consistent with the existing palette. Original code; tview/tcell MIT.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/audit.go` (package `data`) — `AuditEntry` + `.JSONL()` + `Auditor` interface + `NewFileAuditor` + `AuditPath()` + `CurrentActor()`.
- `installer/internal/tui/monitor/data/audit_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — arg-builders (`deletePodArgs`/`rolloutRestartArgs`/`cordonArgs`) + `runAction` + `Resources` methods `DeletePod`/`RolloutRestart`/`SetCordon` + `actionTimeout`.
- `installer/internal/tui/monitor/data/kube_test.go` — arg-builder tests.
- `installer/internal/tui/monitor/monitor.go` — action catalog + the modal overlay (root Pages) + the `a`→menu→confirm→execute→audit→result flow + refresh-pause + footer.

---

## Task 1: data/kube.go — action arg-builders + Resources methods

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `Resources` gains `DeletePod(namespace, name string) (string, int, error)`, `RolloutRestart(kind, namespace, name string) (string, int, error)`, `SetCordon(node string, cordon bool) (string, int, error)` — each returns combined output, the process exit code, and error. Backed by pure `deletePodArgs`/`rolloutRestartArgs`/`cordonArgs`.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
func TestActionArgs(t *testing.T) {
	if got := deletePodArgs("cosmos", "cosmos-pg-0"); !reflect.DeepEqual(got, []string{"delete", "pod", "-n", "cosmos", "cosmos-pg-0"}) {
		t.Fatalf("deletePodArgs: %v", got)
	}
	if got := rolloutRestartArgs("deployments", "authservice", "authservice"); !reflect.DeepEqual(got, []string{"rollout", "restart", "deployments", "-n", "authservice", "authservice"}) {
		t.Fatalf("rolloutRestartArgs: %v", got)
	}
	if got := cordonArgs("cosmos-k8s", true); !reflect.DeepEqual(got, []string{"cordon", "cosmos-k8s"}) {
		t.Fatalf("cordonArgs cordon: %v", got)
	}
	if got := cordonArgs("cosmos-k8s", false); !reflect.DeepEqual(got, []string{"uncordon", "cosmos-k8s"}) {
		t.Fatalf("cordonArgs uncordon: %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestActionArgs -v`
Expected: FAIL — `undefined: deletePodArgs`.

- [ ] **Step 3: Implement** — append to `data/kube.go`:

```go
// actionTimeout bounds a mutating kubectl action (restart/cordon/rollout). Drain
// (deferred) would need longer; these are quick.
const actionTimeout = 15 * time.Second

// deletePodArgs builds `kubectl delete pod -n <ns> <name>` (pod restart — the
// controller recreates it).
func deletePodArgs(namespace, name string) []string {
	return []string{"delete", "pod", "-n", namespace, name}
}

// rolloutRestartArgs builds `kubectl rollout restart <kind> -n <ns> <name>`.
func rolloutRestartArgs(kind, namespace, name string) []string {
	return []string{"rollout", "restart", kind, "-n", namespace, name}
}

// cordonArgs builds `kubectl cordon|uncordon <node>`.
func cordonArgs(node string, cordon bool) []string {
	verb := "uncordon"
	if cordon {
		verb = "cordon"
	}
	return []string{verb, node}
}

// runAction runs a mutating `kubectl <args...>` bounded by actionTimeout, returning
// combined output, the process exit code (0 on success), and error.
func runAction(args []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", args...).CombinedOutput()
	code := 0
	if err != nil {
		code = 1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return string(out), code, fmt.Errorf("kubectl %s: %w", strings.Join(args, " "), err)
	}
	return string(out), code, nil
}

// DeletePod restarts a pod by deleting it (its controller recreates it).
func (execResources) DeletePod(namespace, name string) (string, int, error) {
	return runAction(deletePodArgs(namespace, name))
}

// RolloutRestart triggers a rolling restart of a workload.
func (execResources) RolloutRestart(kind, namespace, name string) (string, int, error) {
	return runAction(rolloutRestartArgs(kind, namespace, name))
}

// SetCordon cordons (cordon=true) or uncordons a node.
func (execResources) SetCordon(node string, cordon bool) (string, int, error) {
	return runAction(cordonArgs(node, cordon))
}
```

Add `"errors"` to the import block if not present (the diff: `errors.As` is used). Then extend the `Resources` interface:

```go
type Resources interface {
	Get(args ...string) ([]byte, error)
	Describe(kind, namespace, name string) (string, error)
	Yaml(kind, namespace, name string) (string, error)
	Logs(namespace, name string, tail int) (string, error)
	DeletePod(namespace, name string) (string, int, error)
	RolloutRestart(kind, namespace, name string) (string, int, error)
	SetCordon(node string, cordon bool) (string, int, error)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/kube.go`
Expected: PASS (the arg-builder test + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): action arg-builders + DeletePod/RolloutRestart/SetCordon"
```

---

## Task 2: data/audit.go — audit entry + file auditor

**Files:**
- Create: `installer/internal/tui/monitor/data/audit.go`, `data/audit_test.go`

**Interfaces:**
- Produces: `type AuditEntry struct { Time, Actor, Action, Kind, Namespace, Name, Command string; ExitCode int; OK bool }`; `func (e AuditEntry) JSONL() string` (a single JSON object + trailing newline); `type Auditor interface { Record(e AuditEntry) error }`; `func NewFileAuditor(path string) Auditor`; `func AuditPath() string`; `func CurrentActor() string`.

- [ ] **Step 1: Write the failing test** — `data/audit_test.go`:

```go
package data

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditEntryJSONL(t *testing.T) {
	e := AuditEntry{
		Time: "2026-06-27T03:00:00Z", Actor: "default", Action: "cordon",
		Kind: "nodes", Name: "cosmos-k8s", Command: "kubectl cordon cosmos-k8s",
		ExitCode: 0, OK: true,
	}
	line := e.JSONL()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("JSONL must end in newline: %q", line)
	}
	var back AuditEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &back); err != nil {
		t.Fatalf("JSONL not valid json: %v", err)
	}
	if back.Action != "cordon" || back.Name != "cosmos-k8s" || !back.OK {
		t.Fatalf("round-trip lost fields: %+v", back)
	}
}

func TestAuditPath_NonEmpty(t *testing.T) {
	if AuditPath() == "" {
		t.Fatal("AuditPath must not be empty")
	}
	if !strings.HasSuffix(AuditPath(), "platform-actions.jsonl") {
		t.Fatalf("unexpected audit path: %s", AuditPath())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run 'TestAuditEntryJSONL|TestAuditPath' -v`
Expected: FAIL — `undefined: AuditEntry`.

- [ ] **Step 3: Implement** — create `data/audit.go`:

```go
package data

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// AuditEntry is one record of an executed Day-2 action.
type AuditEntry struct {
	Time      string `json:"time"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	Command   string `json:"command"`
	ExitCode  int    `json:"exitCode"`
	OK        bool   `json:"ok"`
}

// JSONL renders the entry as one JSON line (trailing newline) for an append log.
func (e AuditEntry) JSONL() string {
	b, _ := json.Marshal(e) // AuditEntry has only json-safe fields; Marshal cannot fail
	return string(b) + "\n"
}

// Auditor records executed actions. Tests inject a fake; the file impl appends JSONL.
type Auditor interface {
	Record(e AuditEntry) error
}

type fileAuditor struct{ path string }

// NewFileAuditor returns an Auditor that appends each entry to path (creating the
// parent dir). The substrate ConfigMap/Events sink is a future swap behind this iface.
func NewFileAuditor(path string) Auditor { return fileAuditor{path: path} }

func (a fileAuditor) Record(e AuditEntry) error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(a.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(e.JSONL())
	return err
}

// AuditPath is the operator-local action log: $XDG_STATE_HOME/srectl/… or ~/.local/state/srectl/…
func AuditPath() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "srectl-platform-actions.jsonl" // last-resort cwd-relative
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "srectl", "platform-actions.jsonl")
}

// CurrentActor returns the kubeconfig current-context user (best-effort), else $USER, else "unknown".
func CurrentActor() string {
	out, err := exec.Command("kubectl", "config", "view", "--minify", "-o", "jsonpath={.contexts[0].context.user}").Output()
	if err == nil {
		if u := strings.TrimSpace(string(out)); u != "" {
			return u
		}
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "unknown"
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/audit.go internal/tui/monitor/data/audit_test.go`
Expected: PASS (both new tests + all existing data tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/audit.go installer/internal/tui/monitor/data/audit_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Day-2 action audit entry + file auditor"
```

---

## Task 3: monitor — action catalog + modal overlay + menu→confirm flow (NO execute yet)

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `drillTarget` (P1.3), the `app`/`layout` from `Run`, the input capture.
- Produces (consumed by Task 4): `type action struct { label, preview, auditAction string; exec func() (string, int, error) }`; `func (m *monitor) actionsFor(dt drillTarget) []action`; `m.modal *tview.Modal` (one reconfigurable modal, a page on a new root Pages); `m.modalActive bool`; `m.pending action`; `openActions(dt)` (show the action menu), `showConfirm(a action)` (show the confirm modal), `closeModal()`.

This task builds the modal FLOW with NO mutation — Confirm currently just closes (Task 4 adds the execute). It proves the overlay + navigation safely.

- [ ] **Step 1: Wrap the layout in a root Pages + add the modal**

In `Run`, after building `layout`, wrap it so modals can overlay it:

```go
modal := tview.NewModal()
m.modal = modal
root := tview.NewPages().
	AddPage("main", layout, true, true).
	AddPage("modal", modal, true, false) // hidden; shown over main during an action
```
and change `app.SetRoot(layout, true)` to `app.SetRoot(root, true)`. Keep `m.root = root` (add the field) so the flow can show/hide the "modal" page.

- [ ] **Step 2: Add the action model + catalog**

```go
// action is one Day-2 action applicable to a selected resource.
type action struct {
	label       string                       // menu button text
	preview     string                       // confirm-modal body
	auditAction string                       // audit "action" name
	exec        func() (string, int, error)  // runs OFF the UI goroutine (Task 4)
}

// actionsFor returns the actions available for a resource (slice-1: reversible only).
func (m *monitor) actionsFor(dt drillTarget) []action {
	switch dt.kind {
	case "pods":
		return []action{{
			label: "Restart", auditAction: "restart-pod",
			preview: fmt.Sprintf("Restart pod %s/%s?\n\nDeletes the pod; its controller recreates it.", dt.namespace, dt.name),
			exec:    func() (string, int, error) { return m.res.DeletePod(dt.namespace, dt.name) },
		}}
	case "deployments", "statefulsets", "daemonsets":
		return []action{{
			label: "Rollout restart", auditAction: "rollout-restart",
			preview: fmt.Sprintf("Rollout-restart %s %s/%s?\n\nCycles its pods with a rolling update.", dt.kind, dt.namespace, dt.name),
			exec:    func() (string, int, error) { return m.res.RolloutRestart(dt.kind, dt.namespace, dt.name) },
		}}
	case "nodes":
		return []action{
			{label: "Cordon", auditAction: "cordon",
				preview: fmt.Sprintf("Cordon node %s?\n\nMarks it unschedulable (running pods keep running).", dt.name),
				exec:    func() (string, int, error) { return m.res.SetCordon(dt.name, true) }},
			{label: "Uncordon", auditAction: "uncordon",
				preview: fmt.Sprintf("Uncordon node %s?\n\nMarks it schedulable again.", dt.name),
				exec:    func() (string, int, error) { return m.res.SetCordon(dt.name, false) }},
		}
	}
	return nil
}
```

- [ ] **Step 3: The menu → confirm flow (Confirm closes for now)**

```go
// openActions shows the action menu for the selected row's resource.
func (m *monitor) openActions(dt drillTarget) {
	acts := m.actionsFor(dt)
	if len(acts) == 0 {
		return
	}
	labels := make([]string, 0, len(acts)+1)
	for _, a := range acts {
		labels = append(labels, a.label)
	}
	labels = append(labels, "Cancel")
	m.modalActive = true
	m.modal.SetText(fmt.Sprintf("Actions · %s/%s", dt.kind, dt.name)).
		ClearButtons().AddButtons(labels).
		SetDoneFunc(func(i int, label string) {
			if label == "Cancel" || i < 0 {
				m.closeModal()
				return
			}
			m.showConfirm(acts[i])
		})
	m.root.ShowPage("modal")
	m.app.SetFocus(m.modal)
}

// showConfirm shows the confirm modal for a chosen action. (Task 4 wires execute.)
func (m *monitor) showConfirm(a action) {
	m.pending = a
	m.modal.SetText(a.preview).
		ClearButtons().AddButtons([]string{"Confirm", "Cancel"}).
		SetDoneFunc(func(i int, label string) {
			if label == "Confirm" {
				m.closeModal() // TASK 4 replaces this with executePending()
				return
			}
			m.closeModal()
		})
}

// closeModal hides the overlay and returns focus to the table.
func (m *monitor) closeModal() {
	m.modalActive = false
	m.root.HidePage("modal")
	m.app.SetFocus(m.main)
}
```

- [ ] **Step 4: Wire the `a` key + the input guard + footer**

In the input capture, add the modal pass-through guard near the top (after the cmdBar + inDetail guards): `if m.modalActive { return ev }` (so the modal's own button nav handles keys). Then add the `a` trigger in the table-view rune handling (NOT overview, NOT while detailed):

```go
case 'a':
	if m.view != "overview" && !m.inDetail {
		row, _ := m.table.GetSelection()
		if c := m.table.GetCell(row, 0); c != nil {
			if dt, ok := c.GetReference().(drillTarget); ok {
				m.openActions(dt)
			}
		}
	}
	return nil
```

Add `a actions` to `footerText()` (bright-key/dim-label style).

- [ ] **Step 5: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green. Adapt `tview.Modal` API specifics (`ClearButtons`/`SetDoneFunc` signature) to the installed version if needed.

- [ ] **Step 6: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): action menu + confirm modal overlay (a → menu → confirm; no-op confirm)"
```

---

## Task 4: monitor — execute + audit + result (the mutation lands here)

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.pending` (Task 3), `data.NewFileAuditor`/`AuditPath`/`CurrentActor`/`AuditEntry`, the anti-freeze `QueueUpdateDraw`.
- Produces: `m.auditor data.Auditor`; `m.actor string`; `executePending()`; `showResult(title, body string)`.

- [ ] **Step 1: Add the auditor + actor to the monitor**

Add fields `auditor data.Auditor` and `actor string` to the `monitor` struct, and in `Run` set `auditor: data.NewFileAuditor(data.AuditPath())`; discover the actor in the existing startup goroutine (off the UI thread, like the prom Ref): `actor := data.CurrentActor()` then set `m.actor = actor` inside the `QueueUpdate` that already sets `m.prom.Ref`/`m.ctx`.

- [ ] **Step 2: Replace the no-op Confirm with executePending (OFF the UI goroutine)**

In `showConfirm`'s done-func, replace the `Confirm` branch `m.closeModal()` with `m.executePending()`. Add:

```go
// executePending runs the pending action OFF the UI goroutine, records the audit
// entry (success or failure), and shows the result. Anti-freeze: only the result
// draw is marshalled back; the kubectl mutation never runs on the UI goroutine.
func (m *monitor) executePending() {
	a := m.pending
	m.modal.SetText(a.preview + "\n\nrunning…").ClearButtons().AddButtons([]string{"…"})
	go func() {
		out, code, err := a.exec()
		entry := data.AuditEntry{
			Time:      time.Now().UTC().Format(time.RFC3339),
			Actor:     m.actor,
			Action:    a.auditAction,
			Name:      a.label, // refined below via target; see note
			Command:   a.label,
			ExitCode:  code,
			OK:        err == nil,
		}
		_ = m.auditor.Record(entry)
		title, body := "✓ "+a.auditAction, strings.TrimSpace(out)
		if err != nil {
			title = "✗ " + a.auditAction + " failed"
			if body == "" {
				body = err.Error()
			}
		}
		m.app.QueueUpdateDraw(func() {
			m.showResult(title, body)
		})
	}()
}

// showResult shows the action result; OK closes the overlay and refreshes the view.
func (m *monitor) showResult(title, body string) {
	m.modal.SetText(title + "\n\n" + body).
		ClearButtons().AddButtons([]string{"OK"}).
		SetDoneFunc(func(int, string) {
			m.closeModal()
			m.refresh()
		})
	m.root.ShowPage("modal")
	m.app.SetFocus(m.modal)
}
```

NOTE on the audit target fields: thread the selected `drillTarget` into `m.pending` so the audit records `Kind`/`Namespace`/`Name`/`Command` accurately. Simplest: add `kind, namespace, name, command string` to the `action` struct, set them in `actionsFor` (where `dt` and the kubectl verb are known — e.g. `command: "kubectl delete pod -n "+dt.namespace+" "+dt.name`), and copy them into the `AuditEntry` here (`Kind: a.kind, Namespace: a.namespace, Name: a.name, Command: a.command`) instead of the `a.label` placeholders above. Make that refinement as part of this task so the audit log is accurate.

- [ ] **Step 3: Pause the background refresh while a modal is active**

`refresh()` already early-returns when `m.inDetail`. Extend that guard so an action modal also pauses it: change the guard to `if m.inDetail || m.modalActive { return }`. (Prevents the refresh from redrawing the header under the modal — the P1.3 lesson.)

- [ ] **Step 4: Build + suite + race**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; gofmt clean; suite green. The mutation path has no unit test (needs a cluster) — it's proven in the Task-5 smoke.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): execute Day-2 action off-UI + audit + result modal"
```

---

## Task 5: Lab smoke (controller-driven — SAFE, reversible targets only)

> The controller drives this on the LAB cluster (cosmos-k8s) ONLY — never the defcon/prod instance. Pick reversible targets: cordon→immediately uncordon; restart a replicated pod.

- [ ] **Step 1: Cross-compile + deliver** (free the binary via `tmux kill-server`, never `pkill -f srectl`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive a NODE cordon→uncordon (safest, fully reversible)**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
echo "node schedulable BEFORE:"; kubectl get node cosmos-k8s -o jsonpath='{.spec.unschedulable}'; echo
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 142 -y 44; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon '3'; sleep 2                    # nodes view
tmux send-keys -t mon 'a'; sleep 1                    # action menu
echo "=== ACTION MENU (node) ==="; tmux capture-pane -t mon -p | sed -n '16,26p'
tmux send-keys -t mon Enter; sleep 1                  # select first button (Cordon)
echo "=== CONFIRM (cordon) ==="; tmux capture-pane -t mon -p | sed -n '16,26p'
tmux send-keys -t mon Enter; sleep 3                  # Confirm → execute
echo "=== RESULT (cordon) ==="; tmux capture-pane -t mon -p | sed -n '16,26p'
tmux send-keys -t mon Enter; sleep 1                  # OK
echo "node schedulable AFTER cordon:"; kubectl get node cosmos-k8s -o jsonpath='{.spec.unschedulable}'; echo
# now uncordon to restore
tmux send-keys -t mon 'a'; sleep 1; tmux send-keys -t mon Down Enter; sleep 1   # Uncordon (2nd button)
tmux send-keys -t mon Enter; sleep 3; tmux send-keys -t mon Enter; sleep 1      # Confirm → OK
echo "node schedulable AFTER uncordon (restored):"; kubectl get node cosmos-k8s -o jsonpath='{.spec.unschedulable}'; echo
echo "=== AUDIT LOG ==="; cat ~/.local/state/srectl/platform-actions.jsonl 2>/dev/null | tail -4
tmux send-keys -t mon q
tmux kill-server 2>/dev/null || true
EOF
```
Expected (controller-verified): the action menu shows `Cordon`/`Uncordon`/`Cancel`; confirm shows the preview; result shows kubectl's `node/cosmos-k8s cordoned`; the node's `.spec.unschedulable` flips `true` then back to empty/`false` after uncordon; the audit log has two JSONL entries (cordon, uncordon) with `actor`/`action`/`name`/`ok`. **Cluster left in its original (schedulable) state.**

- [ ] **Step 3: (Optional, if step 2 clean) drive a POD restart on a replicated/recreatable pod**

Pick a pod that recovers cleanly (e.g., a node-exporter daemonset pod or a monitoring deployment pod). Drive `4` → select that pod → `a` → Restart → Confirm → verify the result shows `pod "…" deleted` and `kubectl get pod` shows it recreated (new age). Confirm an audit entry. Avoid stateful singletons (PG primary, etcd).

- [ ] **Step 4: Record the smoke outcome in the ledger** (controller does NOT ping the user — the user has delegated all e2e driving). Note any bug found + fix before the final review.

---

## Self-Review

**1. Spec coverage (P3 Day-2 k8s actions, slice 1):** the action model `a → menu → preview/confirm → execute → audit → result` (spec §6/§7) → Tasks 3–4; restart/rollout/cordon (spec §7 P3) → Tasks 1+3; audited with actor/action/target/timestamp/result (spec §3.3) → Task 2 + 4. Deferred + noted: scale (replicas prompt) + delete/drain (typed-name gate) → slice 2; backup/config/updates/ConMon → P4–P7; substrate ConfigMap/Events audit sink → next (Auditor swap).

**2. Placeholder scan:** Tasks 1–2 ship full code + tests for the pure arg-builders, the audit formatter, and the path. Tasks 3–4 are concrete integration with code; the mutation is isolated in Task 4 (gated by Task 3's reviewed modal flow), build+suite-gated, smoke-proven (controller-driven) in Task 5. The audit-target refinement (threading `drillTarget` into the `action`/`AuditEntry`) is called out explicitly in Task 4 Step 2. No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `Resources.DeletePod/RolloutRestart/SetCordon` (Task 1) called by the `action.exec` closures in `actionsFor` (Task 3) and run in `executePending` (Task 4). `data.AuditEntry`/`Auditor`/`NewFileAuditor`/`AuditPath`/`CurrentActor` (Task 2) used by `m.auditor`/`m.actor`/`executePending` (Task 4). `action{label,preview,auditAction,exec,…kind,namespace,name,command}` + `m.modal`/`m.root`/`m.modalActive`/`m.pending` (Task 3) consumed by Task 4. The `refresh()` guard gains `|| m.modalActive` (Task 4) alongside the P1.3 `m.inDetail`. The anti-freeze rule (exec off-UI, draw via QueueUpdateDraw) holds; `a` is guarded to table views (cell-0 `drillTarget`), so overview/packages/apps don't trigger it.
