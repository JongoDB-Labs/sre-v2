# srectl Day-2 console — Phase 4 slice 2 (restore → clone-to-new-cluster) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a **non-destructive RESTORE** to the Day-2 console (spec §7 P4): "Restore to new cluster" clones a PostgresCluster into a **brand-new** cluster bootstrapped from its pgBackRest backups (PGO `spec.dataSource.postgresCluster`). The original is never touched — the operator gets a verified copy to cut over to. **In-place (destructive) restore is DEFERRED** to its own typed-cluster-name-gated slice.

**Architecture:** A PURE `cloneManifest` transform (source PostgresCluster JSON → a new-cluster manifest: clean metadata, status + in-place-restore/manual specs stripped, `spec.dataSource.postgresCluster` added → faithfully inherits the source's PG version / storage / users / patroni). A thin `Resources.CloneCluster` exec-wrapper (get source → cloneManifest → `kubectl create -f -`). A "Restore to new cluster" action on a `postgrescluster` target routes (like Scale) to a fresh, **box-contained** input modal collecting the new cluster name, then reuses the audited `executePending`. Non-destructive ⇒ simple input-gate (no typed-name). PITR `options` are wired through the data layer (slice 1 modal passes none ⇒ latest backup).

**Tech Stack:** Go 1.25, kubectl (`get postgrescluster -o json`, `create -f -`), CrunchyData PGO 6.0.2 (API `v1`) / pgBackRest, the P1.x/P3.x/P4-backups monitor packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-restore`** (off main 0504b82, which has the merged console + more-actions + P4 backups). Do NOT switch branches.
- **Non-destructive ONLY:** `cloneManifest`/`CloneCluster` create a NEW cluster; they MUST NOT modify or delete the source. No in-place restore, no `spec.backups.pgbackrest.restore`, no restore annotation on the source — those are the DEFERRED destructive slice. The clone is a normal additive resource creation.
- **Exec-wrapper rule:** kubectl via the fake-backed `Resources`; `cloneManifest` + arg-builders are PURE + unit-tested. `CloneCluster` is the only new method and it CREATES (non-destructive to existing data).
- **Anti-freeze:** the restore runs through the existing `executePending` (off the UI goroutine) + is audited (both sinks) exactly like scale/trigger-backup.
- **Dialog containment (learned 96699ce):** the new-name input modal MUST keep its field inside the box — never put variable-length text in a form-field LABEL; use a short fixed label (or empty label + fill width) and let the box title carry the cluster name. (tview does not wrap/truncate field labels.)
- **Confirmed lab facts (recon 2026-06-29):** PGO `registry.developers.crunchydata.com/crunchydata/postgres-operator:ubi9-6.0.2-0`, CRD `postgres-operator.crunchydata.com/v1`. `spec.dataSource.postgresCluster` fields: `clusterName`, `clusterNamespace`, `repoName`, `options []string`. Source `cosmos-pg` (ns cosmos): `spec.postgresVersion:16`, `instances:[{name:instance1,replicas:1,storage:5Gi}]`, `backups.pgbackrest.repos:[{name:repo1, volume}]`, `users:[{name:cosmos,databases:[cosmos],options:SUPERUSER}]`.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/pgrestore.go` (package `data`) — `cloneManifest` + `Resources.CloneCluster` + a stdin-create helper.
- `installer/internal/tui/monitor/data/pgrestore_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — extend the `Resources` interface with `CloneCluster`.
- `installer/internal/tui/monitor/monitor.go` — the "Restore to new cluster" action (postgrescluster case) + `needsRestoreName` routing + `showRestoreInput` (box-contained input modal).

---

## Task 1: data/pgrestore.go — cloneManifest (pure) + CloneCluster

**Files:**
- Create: `installer/internal/tui/monitor/data/pgrestore.go`, `data/pgrestore_test.go`
- Modify: `installer/internal/tui/monitor/data/kube.go`

**Interfaces:**
- Produces: `func cloneManifest(sourceJSON []byte, newName string, options []string) ([]byte, error)`; `Resources` gains `CloneCluster(sourceNamespace, sourceName, newName string, options []string) (string, int, error)`.

- [ ] **Step 1: Write the failing test** — `data/pgrestore_test.go`:

```go
package data

import (
	"encoding/json"
	"strings"
	"testing"
)

const srcCluster = `{
 "apiVersion":"postgres-operator.crunchydata.com/v1","kind":"PostgresCluster",
 "metadata":{"name":"cosmos-pg","namespace":"cosmos","resourceVersion":"12345","uid":"abc-123",
   "creationTimestamp":"2026-06-26T02:00:00Z","generation":7,"managedFields":[{"manager":"pgo"}],
   "annotations":{"postgres-operator.crunchydata.com/pgbackrest-backup":"2026-06-29T12:00:00Z"}},
 "spec":{"postgresVersion":16,
   "instances":[{"name":"instance1","replicas":1,"dataVolumeClaimSpec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"5Gi"}}}}],
   "users":[{"name":"cosmos","databases":["cosmos"],"options":"SUPERUSER"}],
   "backups":{"pgbackrest":{
     "repos":[{"name":"repo1","volume":{"volumeClaimSpec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"5Gi"}}}}}],
     "manual":{"repoName":"repo1"},
     "restore":{"enabled":true,"repoName":"repo1"}}},
   "dataSource":{"postgresCluster":{"clusterName":"old-thing","repoName":"repo1"}}},
 "status":{"instances":[{"name":"instance1","readyReplicas":1}]}}`

func TestCloneManifest(t *testing.T) {
	out, err := cloneManifest([]byte(srcCluster), "cosmos-pg-restore", nil)
	if err != nil {
		t.Fatalf("cloneManifest err: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not json: %v", err)
	}
	meta := m["metadata"].(map[string]any)
	if meta["name"] != "cosmos-pg-restore" || meta["namespace"] != "cosmos" {
		t.Fatalf("metadata name/namespace wrong: %+v", meta)
	}
	// runtime metadata + status stripped
	for _, k := range []string{"resourceVersion", "uid", "creationTimestamp", "generation", "managedFields", "annotations"} {
		if _, ok := meta[k]; ok {
			t.Fatalf("metadata.%s should be stripped", k)
		}
	}
	if _, ok := m["status"]; ok {
		t.Fatalf("status should be stripped")
	}
	spec := m["spec"].(map[string]any)
	// dataSource points at the SOURCE cluster + its repo
	ds := spec["dataSource"].(map[string]any)["postgresCluster"].(map[string]any)
	if ds["clusterName"] != "cosmos-pg" || ds["repoName"] != "repo1" {
		t.Fatalf("dataSource wrong (must point at source + repo1): %+v", ds)
	}
	if _, ok := ds["options"]; ok {
		t.Fatalf("options must be omitted when none given (got %v)", ds["options"])
	}
	// in-place restore + manual stripped from the clone; source spec otherwise inherited
	pgb := spec["backups"].(map[string]any)["pgbackrest"].(map[string]any)
	if _, ok := pgb["restore"]; ok {
		t.Fatalf("spec.backups.pgbackrest.restore must be stripped (no in-place restore on a clone)")
	}
	if _, ok := pgb["manual"]; ok {
		t.Fatalf("spec.backups.pgbackrest.manual should be stripped")
	}
	if spec["postgresVersion"].(float64) != 16 {
		t.Fatalf("postgresVersion not inherited: %v", spec["postgresVersion"])
	}
}

func TestCloneManifestPITROptions(t *testing.T) {
	out, err := cloneManifest([]byte(srcCluster), "c2", []string{"--type=time", "--target=2026-06-29 12:00:00"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(out), "--type=time") {
		t.Fatalf("PITR options not threaded into the manifest")
	}
}

func TestCloneManifestNoRepo(t *testing.T) {
	if _, err := cloneManifest([]byte(`{"metadata":{"name":"x","namespace":"y"},"spec":{"backups":{"pgbackrest":{"repos":[]}}}}`), "z", nil); err == nil {
		t.Fatalf("expected error when the source has no pgBackRest repo")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestClone -v`
Expected: FAIL — `undefined: cloneManifest`.

- [ ] **Step 3: Implement** — create `data/pgrestore.go`:

```go
package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// cloneManifest transforms a source PostgresCluster's JSON into a manifest for a
// NEW cluster that restores from the source's pgBackRest backups (PGO dataSource).
// It strips runtime metadata + status + any in-place restore/manual specs, renames
// the cluster, and points spec.dataSource.postgresCluster at the source + its first
// repo. The source is NOT modified — this only produces the new manifest bytes.
func cloneManifest(sourceJSON []byte, newName string, options []string) ([]byte, error) {
	var src map[string]any
	if err := json.Unmarshal(sourceJSON, &src); err != nil {
		return nil, fmt.Errorf("parse source cluster: %w", err)
	}
	spec, _ := src["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("source cluster has no spec")
	}
	srcMeta, _ := src["metadata"].(map[string]any)
	srcName, _ := srcMeta["name"].(string)
	namespace, _ := srcMeta["namespace"].(string)
	if srcName == "" {
		return nil, fmt.Errorf("source cluster has no metadata.name")
	}

	// First pgBackRest repo to restore from.
	repoName := ""
	if b, ok := spec["backups"].(map[string]any); ok {
		if pgb, ok := b["pgbackrest"].(map[string]any); ok {
			if repos, ok := pgb["repos"].([]any); ok && len(repos) > 0 {
				if r0, ok := repos[0].(map[string]any); ok {
					repoName, _ = r0["name"].(string)
				}
			}
			// A clone must not carry the source's in-place restore directive or manual config.
			delete(pgb, "restore")
			delete(pgb, "manual")
		}
	}
	if repoName == "" {
		return nil, fmt.Errorf("source cluster %s has no pgBackRest repo to restore from", srcName)
	}

	// Clean metadata: keep only name (new) + namespace.
	src["metadata"] = map[string]any{"name": newName, "namespace": namespace}
	delete(src, "status")

	// Point dataSource at the source cluster's backups (replaces any existing dataSource).
	pc := map[string]any{"clusterName": srcName, "repoName": repoName}
	if len(options) > 0 {
		pc["options"] = options
	}
	spec["dataSource"] = map[string]any{"postgresCluster": pc}

	return json.Marshal(src)
}

// CloneCluster gets the source PostgresCluster and creates a NEW cluster (newName)
// that restores from the source's backups. Non-destructive: the source is untouched.
func (execResources) CloneCluster(sourceNamespace, sourceName, newName string, options []string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgbTimeout)
	defer cancel()
	srcJSON, err := exec.CommandContext(ctx, "kubectl", "get", "postgrescluster", sourceName, "-n", sourceNamespace, "-o", "json").Output()
	if err != nil {
		return "", 1, fmt.Errorf("get source cluster %s/%s: %w", sourceNamespace, sourceName, err)
	}
	manifest, err := cloneManifest(srcJSON, newName, options)
	if err != nil {
		return "", 1, err
	}
	return createFromStdin(manifest)
}

// createFromStdin runs `kubectl create -f -` with the manifest on stdin.
func createFromStdin(manifest []byte) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "kubectl", "create", "-f", "-")
	cmd.Stdin = bytes.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	return exitInfo(string(out), err)
}
```

(Reuse the existing `pgbTimeout` from `pgbackrest.go`. `actionTimeout` + `exitInfo` are the helpers `runAction` already uses in `kube.go` — REUSE them; do not redefine. If `runAction`'s timeout const / exit-code extraction has different names, read `kube.go` and reuse whatever it uses — match the exit-code-via-`errors.As` pattern so `createFromStdin` returns the kubectl exit code on failure, like the other mutating wrappers.)

Then extend the `Resources` interface in `kube.go`:

```go
	CloneCluster(sourceNamespace, sourceName, newName string, options []string) (string, int, error)
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/pgrestore.go internal/tui/monitor/data/pgrestore_test.go internal/tui/monitor/data/kube.go`
Expected: PASS (clone tests + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/pgrestore.go installer/internal/tui/monitor/data/pgrestore_test.go installer/internal/tui/monitor/data/kube.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): pgBackRest clone-to-new-cluster manifest builder + CloneCluster wrapper"
```

---

## Task 2: monitor — "Restore to new cluster" action + contained input modal

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `actionsFor` (postgrescluster case), `m.res.CloneCluster`, `openActions` routing, `executePending`, the `action` struct, `showScaleInput` (pattern to mirror), the dialog-containment pattern (from `showTypedConfirm`).
- Produces: an `action.needsRestoreName bool` marker; `showRestoreInput`; the postgrescluster `Restore to new cluster` action.

- [ ] **Step 1: Add the marker + route**

Add a `needsRestoreName bool` field to the `action` struct (next to `needsReplicas`/`needsTypedName`). In `openActions`'s route chain, add `needsRestoreName` BEFORE the plain-confirm fallback (order: needsTypedName → needsReplicas → needsRestoreName → plain confirm):

```go
	case a.needsRestoreName:
		m.showRestoreInput(a)
```

- [ ] **Step 2: Add the `Restore to new cluster` action**

In `actionsFor`'s `case "postgrescluster":`, append a second action (after the existing `Trigger backup`):

```go
		{
			label: "Restore to new cluster", auditAction: "restore-clone", needsRestoreName: true,
			kind: dt.kind, namespace: dt.namespace, name: dt.name,
			preview: fmt.Sprintf("Clone %s/%s into a NEW cluster from its latest backup (the original is untouched).", dt.namespace, dt.name),
		},
```

- [ ] **Step 3: Add `showRestoreInput` (box-contained, mirrors showScaleInput + the containment fix)**

```go
// showRestoreInput collects the new cluster name, then clones the source into a NEW
// cluster via the audited executePending. Non-destructive → a simple input gate (no
// typed-name). The field uses a short fixed label + a width that fits the box so the
// input never overflows the dialog border.
func (m *monitor) showRestoreInput(a action) {
	form := tview.NewForm()
	form.SetBackgroundColor(consoleBg)
	form.AddInputField("New cluster name", a.name+"-restore", 28, func(textToCheck string, lastChar rune) bool {
		return (lastChar >= 'a' && lastChar <= 'z') || (lastChar >= '0' && lastChar <= '9') || lastChar == '-'
	}, nil)
	form.AddButton("Restore", func() {
		newName := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if newName == "" || newName == a.name {
			return // empty or same-as-source → no-op; operator can correct or Cancel
		}
		r := a
		r.command = fmt.Sprintf("kubectl create postgrescluster %s -n %s (clone of %s)", newName, a.namespace, a.name)
		r.preview = fmt.Sprintf("Clone %s/%s → new cluster %s", a.namespace, a.name, newName)
		r.exec = func() (string, int, error) { return m.res.CloneCluster(a.namespace, a.name, newName, nil) }
		m.pending = r
		m.executePending()
	})
	form.AddButton("Cancel", func() { m.closeModal() })
	form.SetBorder(true).SetTitle(fmt.Sprintf(" Restore %s/%s → new cluster ", a.kind, a.name)).SetTitleColor(consoleText)
	form.SetButtonsAlign(tview.AlignCenter)
	grid := tview.NewGrid().SetColumns(0, 60, 0).SetRows(0, 9, 0).AddItem(form, 1, 1, 1, 1, 0, 0, true)
	m.root.RemovePage("modal")
	m.root.AddPage("modal", grid, true, true)
	m.modalActive = true
	m.app.SetFocus(form)
}
```

(Label "New cluster name" (15) + field 28 = ~44 < 60-col box inner (58) → contained. Default value `<source>-restore` gives the operator a ready name to accept or edit. The action is non-destructive so it routes to a plain input + the audited `executePending`, exactly like Scale.)

- [ ] **Step 4: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Restore to new cluster action (non-destructive pgBackRest clone)"
```

---

## Task 3: Lab smoke (controller-driven) — clone, verify, clean up

- [ ] **Step 1: Cross-compile + deliver** (`tmux kill-server`, not `pkill`).

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive the restore (clone) + verify PGO provisions it**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
echo "### clusters BEFORE: $(kubectl get postgrescluster -n cosmos --no-headers 2>/dev/null | awk '{print $1}' | tr '\n' ' ')"
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon '9'; sleep 3                              # backups view
tmux send-keys -t mon 'a'; sleep 1.3                            # cluster action menu
echo "=== menu (expect Trigger backup + Restore to new cluster) ==="; tmux capture-pane -t mon -p | grep -iE 'Actions ·|Trigger backup|Restore to new|Cancel' | head -4
tmux send-keys -t mon Tab; sleep 0.6; tmux send-keys -t mon Enter; sleep 1.5   # Trigger backup -> Restore to new cluster -> input modal
echo "=== restore input modal (name field must stay inside ║) ==="; tmux capture-pane -t mon -p | sed -n '18,30p' | grep -E '╔|║|╚'
tmux send-keys -t mon Tab; sleep 0.6; tmux send-keys -t mon Enter; sleep 5     # accept default name (cosmos-pg-restore) -> Restore -> execute
echo "=== result ==="; tmux capture-pane -t mon -p | grep -iE 'restore-clone|created|postgrescluster' | head -3
tmux send-keys -t mon Enter; sleep 1.5; tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
echo "### new cluster created?: $(kubectl get postgrescluster cosmos-pg-restore -n cosmos --no-headers 2>/dev/null)"
echo "### restore job / pods coming up:"; kubectl get pods -n cosmos --no-headers 2>/dev/null | grep 'cosmos-pg-restore' | head
echo "=== audit (restore-clone, both sinks) ==="; grep restore-clone ~/.local/state/srectl/platform-actions.jsonl 2>/dev/null | tail -1
EOF
```
Expected: the cluster menu now offers BOTH **Trigger backup** AND **Restore to new cluster**; the input modal's name field stays INSIDE the box borders (default `cosmos-pg-restore`); Restore → "✓ restore-clone … postgrescluster … created"; PGO provisions a NEW `cosmos-pg-restore` PostgresCluster (restore/instance pods appear) WITHOUT touching `cosmos-pg`; audited (both sinks).

- [ ] **Step 3: Verify the restore actually recovered data, then CLEAN UP the clone**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
echo "### waiting for the clone to come up (up to ~3min)…"
for i in $(seq 18); do
  ready=$(kubectl get postgrescluster cosmos-pg-restore -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}' 2>/dev/null)
  [ "$ready" = "1" ] && break; sleep 10
done
echo "### clone status: readyReplicas=$(kubectl get postgrescluster cosmos-pg-restore -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}' 2>/dev/null)"
echo "### source cosmos-pg UNTOUCHED?: readyReplicas=$(kubectl get postgrescluster cosmos-pg -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}' 2>/dev/null) (expect still healthy)"
echo "### data recovered (table count in the restored DB):"
restorePod=$(kubectl get pods -n cosmos -l postgres-operator.crunchydata.com/cluster=cosmos-pg-restore,postgres-operator.crunchydata.com/role=master --no-headers 2>/dev/null | awk '{print $1}' | head -1)
[ -n "$restorePod" ] && kubectl exec -n cosmos "$restorePod" -c database -- psql -U postgres -d cosmos -tAc "select count(*) from information_schema.tables where table_schema='public';" 2>/dev/null
echo "### CLEANUP — delete the clone (keep the lab tidy):"
kubectl delete postgrescluster cosmos-pg-restore -n cosmos --ignore-not-found 2>&1 | tail -1
EOF
```
Expected: the clone reaches `readyReplicas=1` and its `cosmos` DB has the restored tables (>0); `cosmos-pg` stays healthy throughout (non-destructive proven); the clone is then deleted. (Record the smoke outcome in the ledger; no user ping — the user will watch the merge-time verification.)

---

## Self-Review

**1. Spec coverage (P4 §7 restore):** restore-to-a-copy (clone) → Tasks 1+2; in-place destructive restore explicitly DEFERRED. PITR plumbed through `cloneManifest`/`CloneCluster` `options` (slice-1 modal passes none = latest). The "restore onto a copy" hard constraint is the whole design.

**2. Placeholder scan:** Task 1 ships the full pure transform + the exec-wrapper with a real-shape fixture (the recon'd cosmos-pg shape) + 3 tests (happy/PITR/no-repo). Task 2 wires the action + a contained modal reusing the reviewed `executePending`. Task 3 proves it end-to-end incl. data recovery + non-destructiveness + cleanup. No TODO/"handle later"/"similar to".

**3. Type consistency:** `cloneManifest(sourceJSON,newName,options)` (T1) consumed by `CloneCluster` (T1) consumed by the `Restore to new cluster` action's `exec` (T2). `Resources.CloneCluster` added to the interface (T1) — `execResources` is the sole implementer. The action carries `needsRestoreName` (T2) → `openActions` routes to `showRestoreInput` (T2) → `executePending` (audited, off-UI). Clone is NON-destructive (creates a new cluster; strips the source's restore/manual; never mutates the source) ⇒ simple input gate, not typed-name. The input field is box-contained (short label + fitting width), honoring the 96699ce dialog-overflow fix.
