# srectl Day-2 console — Phase 3 slice 4 (more actions: substrate audit sink + delete-pod) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** (1) Add a **substrate ConfigMap audit sink** (spec §3.3 preferred sink) alongside the existing reliable host-file JSONL, via a **multi-auditor** that writes to both. (2) Add **delete-pod** behind the typed-name gate (completes delete across pods + workloads).

**Architecture:** A `configMapAuditor` appends each `AuditEntry` as a new key in a `srectl-platform-actions` ConfigMap (single `kubectl patch --type merge` per action; bootstraps the ns+cm on first write). A `multiAuditor` fans `Record` out to N sub-auditors (host-file + ConfigMap) so the local audit always works even if the cluster write fails. The monitor swaps its single auditor for the multi-auditor. delete-pod reuses the P3.3 typed-name modal + `Resources.Delete`.

**Tech Stack:** Go 1.25, kubectl (`patch`/`create configmap`), the P3.x action framework + `data/audit.go`.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-day2-more`, off the merged main which has the full console). Do NOT switch branches.
- **Audit is best-effort (binding):** `Record` errors must NOT break an action (the monitor already does `_ = m.auditor.Record(...)`). The multi-auditor records to ALL sub-auditors even if one errors (so a ConfigMap/RBAC failure never loses the local host-file audit). The host-file write happens regardless of the ConfigMap result.
- **delete-pod safety (binding):** delete-pod is destructive → it uses the SAME typed-name gate as delete-workload (`needsTypedName`); no new un-gated delete path. Reuses `executePending` (off-UI + audited). Lab-only e2e; never defcon/prod.
- **Exec-wrapper rule:** the ConfigMap writes go through kubectl exec (bounded by a timeout); the patch-payload builder is PURE + unit-tested. No embedded client.
- **Read/idempotence:** the ConfigMap sink ensures the ns+cm exist (create-if-missing, ignore "already exists"); appends are a single merge-patch with a unique per-action key (no read-modify-write race).
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Modify:**
- `installer/internal/tui/monitor/data/audit.go` — `configMapAuditor` + `NewConfigMapAuditor` + pure `auditPatch(key, jsonl string) string` + `multiAuditor` + `NewMultiAuditor`.
- `installer/internal/tui/monitor/data/audit_test.go` — `auditPatch` + `multiAuditor` tests.
- `installer/internal/tui/monitor/monitor.go` — swap `m.auditor` to the multi-auditor in `Run`; add a delete-pod Delete marker to the `pods` case in `actionsFor`.

---

## Task 1: data/audit.go — auditPatch + configMapAuditor + multiAuditor

**Files:**
- Modify: `installer/internal/tui/monitor/data/audit.go`, `data/audit_test.go`

**Interfaces:**
- Produces: `func auditPatch(key, jsonl string) string` (a JSON merge-patch body `{"data":{"<key>":"<jsonl>"}}`, properly escaped via `json.Marshal`); `func NewConfigMapAuditor(namespace, name string) Auditor`; `func NewMultiAuditor(auditors ...Auditor) Auditor` (records to all; returns the first non-nil error but always attempts every sub-auditor).

- [ ] **Step 1: Write the failing test** — append to `data/audit_test.go`:

```go
func TestAuditPatch(t *testing.T) {
	got := auditPatch("a123", `{"action":"delete","ok":true}`+"\n")
	// must be valid JSON of shape {"data":{"a123":"<jsonl>"}} with the jsonl escaped
	var p struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("auditPatch not valid json: %v (%s)", err, got)
	}
	if p.Data["a123"] != `{"action":"delete","ok":true}`+"\n" {
		t.Fatalf("round-trip lost the jsonl value: %q", p.Data["a123"])
	}
}

type fakeAuditor struct {
	got []AuditEntry
	err error
}

func (f *fakeAuditor) Record(e AuditEntry) error { f.got = append(f.got, e); return f.err }

func TestMultiAuditor_RecordsAllEvenOnError(t *testing.T) {
	a := &fakeAuditor{err: errorString("boom")} // a fails
	b := &fakeAuditor{}                          // b should still get it
	m := NewMultiAuditor(a, b)
	err := m.Record(AuditEntry{Action: "delete"})
	if err == nil {
		t.Fatal("multi must surface the sub-auditor error")
	}
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("both sub-auditors must be attempted: a=%d b=%d", len(a.got), len(b.got))
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestAuditPatch|TestMultiAuditor' -v`
Expected: FAIL — `undefined: auditPatch`.

- [ ] **Step 3: Implement** — append to `data/audit.go` (note the imports already include `encoding/json`, `os/exec`, `fmt`, `time`; add `context` + `strconv` if missing):

```go
// auditPatch builds a strategic/merge-patch body that ADDS one data key (no
// read-modify-write). json.Marshal escapes the JSONL value safely.
func auditPatch(key, jsonl string) string {
	b, _ := json.Marshal(map[string]any{"data": map[string]string{key: jsonl}})
	return string(b)
}

type configMapAuditor struct{ namespace, name string }

// NewConfigMapAuditor records each entry as a new key in a ConfigMap (the substrate
// audit sink, spec §3.3). It bootstraps the ns+cm on first write. Best-effort: a
// failure is returned but does not panic; the monitor ignores audit errors.
func NewConfigMapAuditor(namespace, name string) Auditor {
	return configMapAuditor{namespace: namespace, name: name}
}

func (a configMapAuditor) Record(e AuditEntry) error {
	key := "a" + strconv.FormatInt(time.Now().UnixNano(), 10) // valid cm key (letter + digits)
	patch := auditPatch(key, e.JSONL())
	if err := a.run("patch", "configmap", a.name, "-n", a.namespace, "--type", "merge", "-p", patch); err != nil {
		a.ensure() // bootstrap ns + cm, then retry once
		return a.run("patch", "configmap", a.name, "-n", a.namespace, "--type", "merge", "-p", patch)
	}
	return nil
}

func (a configMapAuditor) ensure() {
	_ = a.run("create", "namespace", a.namespace)             // ignore "already exists"
	_ = a.run("create", "configmap", a.name, "-n", a.namespace) // ignore "already exists"
}

func (a configMapAuditor) run(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", args...).Run()
}

// multiAuditor fans Record out to all sub-auditors (e.g. host-file + ConfigMap),
// attempting every one even if an earlier sink errors; returns the first error.
type multiAuditor struct{ auditors []Auditor }

// NewMultiAuditor records each entry to all the given auditors.
func NewMultiAuditor(auditors ...Auditor) Auditor { return multiAuditor{auditors: auditors} }

func (m multiAuditor) Record(e AuditEntry) error {
	var firstErr error
	for _, a := range m.auditors {
		if err := a.Record(e); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/audit.go internal/tui/monitor/data/audit_test.go`
Expected: PASS (auditPatch + multiAuditor tests + all existing data tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/audit.go installer/internal/tui/monitor/data/audit_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): ConfigMap audit sink + multi-auditor"
```

---

## Task 2: monitor — wire the multi-auditor + add delete-pod

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `data.NewMultiAuditor`/`NewFileAuditor`/`NewConfigMapAuditor`/`AuditPath`; the `action` struct (`needsTypedName`), `actionsFor`'s `pods` case.

- [ ] **Step 1: Swap the auditor to the multi-auditor**

In `Run`, where `m.auditor` is set (currently `data.NewFileAuditor(data.AuditPath())`), change it to:

```go
auditor: data.NewMultiAuditor(
	data.NewFileAuditor(data.AuditPath()),
	data.NewConfigMapAuditor("sre-system", "srectl-platform-actions"),
),
```

- [ ] **Step 2: Add a delete-pod Delete marker to the pods case in actionsFor**

The `pods` case currently returns one action (`Restart`). Append a `Delete` marker (typed-name gated) after it:

```go
case "pods":
	return []action{
		{label: "Restart", auditAction: "restart-pod",
			kind: dt.kind, namespace: dt.namespace, name: dt.name,
			command: fmt.Sprintf("kubectl delete pod -n %s %s", dt.namespace, dt.name),
			preview: fmt.Sprintf("Restart pod %s/%s?\n\nDeletes the pod; its controller recreates it.", dt.namespace, dt.name),
			exec:    func() (string, int, error) { return m.res.DeletePod(dt.namespace, dt.name) }},
		{label: "Delete", auditAction: "delete", needsTypedName: true,
			kind: dt.kind, namespace: dt.namespace, name: dt.name},
	}
```

(The `Delete` marker reuses `showTypedConfirm` — the menu routes `needsTypedName` first; `showTypedConfirm` builds the concrete delete via `m.res.Delete("pods", ns, name)` and runs it through `executePending`. NOTE: "Restart" already deletes the pod for a recreate; "Delete" is the explicit, typed-name-gated removal — both exist intentionally, Restart for the common recycle, Delete for the deliberate force-removal of e.g. a stuck/orphan pod.)

- [ ] **Step 3: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): multi-auditor (host-file + ConfigMap) + delete-pod (typed-name)"
```

---

## Task 3: Lab smoke (controller-driven — throwaway pod, SAFE)

- [ ] **Step 1: Cross-compile + deliver + create a throwaway pod fixture** (`tmux kill-server`, not `pkill`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'kubectl create ns aaa-srectl-smoke 2>/dev/null; kubectl run smoke-pod --image=registry.k8s.io/pause:3.9 -n aaa-srectl-smoke 2>/dev/null; kubectl delete cm srectl-platform-actions -n sre-system 2>/dev/null; rm -f ~/.local/state/srectl/platform-actions.jsonl; kubectl get pod smoke-pod -n aaa-srectl-smoke 2>&1'
```

- [ ] **Step 2: Drive delete-pod via the typed-name gate** — `4` (pods) → the `aaa-srectl-smoke/smoke-pod` row (it sorts first) → `a` → Delete → typed-name modal → wrong name BLOCKS → exact `smoke-pod` DELETES. Then verify BOTH audit sinks:
```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'echo "=== host-file audit ==="; cat ~/.local/state/srectl/platform-actions.jsonl 2>/dev/null | tail -2; echo "=== ConfigMap audit (sre-system/srectl-platform-actions) ==="; kubectl get cm srectl-platform-actions -n sre-system -o json 2>&1 | jq -r ".data | to_entries[] | .value" 2>/dev/null | tail -2'
```
Expected (controller-verified): the typed-name gate blocks the wrong name and deletes `smoke-pod` on the exact name; the audit entry (`action:"delete"`, `kind:"pods"`) appears in BOTH the host-file JSONL AND the `sre-system/srectl-platform-actions` ConfigMap.

- [ ] **Step 3: Clean up + record** the smoke outcome in the ledger; `kubectl delete ns aaa-srectl-smoke --wait=false`. (No user ping.)

---

## Self-Review

**1. Spec coverage (more-actions):** substrate ConfigMap audit sink (spec §3.3) → Task 1 (configMapAuditor) + 2 (wire multi-auditor); delete-pod (spec §7) → Task 2 (typed-name marker). Deferred + noted: delete-pvc (needs a PVC resource view first), node drain (multi-node lab), the cosmos `platform_actions` hash-chain sink.

**2. Placeholder scan:** Task 1 ships full code + tests for the pure `auditPatch` + the `multiAuditor` (with fake sub-auditors). Task 2 wires the multi-auditor + adds the delete-pod marker (reusing the reviewed `showTypedConfirm`/`executePending`/`Resources.Delete`). Smoke-proven (Task 3). No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `NewMultiAuditor`/`NewConfigMapAuditor`/`auditPatch` (Task 1) wired in `Run` (Task 2). `multiAuditor.Record` attempts all sub-auditors (host-file always written even if ConfigMap errors). The delete-pod `Delete` marker (`needsTypedName`, `kind:"pods"`) routes to `showTypedConfirm` (P3.3), which builds `m.res.Delete("pods", ns, name)` + runs via `executePending` (off-UI + audited). The ConfigMap sink is best-effort (errors ignored by `_ = m.auditor.Record`).
