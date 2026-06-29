# srectl Day-2 console — Phase 4 slice 3 (in-place DESTRUCTIVE restore) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add the **destructive in-place restore** (spec §7 P4, the deferred sibling of clone-to-new): "Restore in place" overwrites an existing PostgresCluster's data from its pgBackRest backups (PGO `spec.backups.pgbackrest.restore` + the restore annotation). DESTRUCTIVE → **typed-cluster-name gate** (the operator types the cluster's exact name) + an explicit OVERWRITE warning + audited + off-UI. The smoke runs on a **throwaway clone**, never a live cluster ("test on a restored copy").

**Architecture:** A PURE `inPlaceRestorePatch(repo, stamp, options)` (the merge-patch enabling the in-place restore + the trigger annotation) + a thin `RestoreInPlace` exec-wrapper (discover the cluster's first repo → patch). The action reuses the **typed-name gate** (`showTypedConfirm`), generalized so it runs the action's OWN `exec` (today it hardcodes Delete) — backward-compatible: Delete keeps its nil-exec legacy path, the restore action supplies a RestoreInPlace exec. Routed through the audited `executePending`.

**Tech Stack:** Go 1.25, kubectl (`patch postgrescluster --type merge`), CrunchyData PGO 6.0.2 / pgBackRest, the P4-backups + P3 monitor packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-inplace-restore`** (off main, which has clone-restore + trigger-backup + the typed-name delete). The controller creates the branch before Task 1.
- **DESTRUCTIVE — typed-cluster-name gated:** the action overwrites the cluster's data. It MUST route through the typed-name gate (operator types the exact cluster name) — NOT a simple confirm. Confirm-gated + audited (`auditAction:"restore-in-place"`) + off-UI. The preview MUST state the data is OVERWRITTEN.
- **Smoke safety — NEVER a live cluster:** the lab smoke creates a THROWAWAY clone (via the merged clone-restore action) + a backup on it, then in-place-restores THE CLONE, then deletes it. `cosmos-pg` is NEVER the target. (Honors the "restore onto a copy" / no-data-loss constraint — defcon/prod is out of scope entirely.)
- **Backward compatibility:** generalizing `showTypedConfirm` must NOT change the existing Delete behavior — verify delete still works (its exec stays nil → the legacy delete path).
- **Exec-wrapper rule; pure patch unit-tested; Go 1.25.** Reuse `runAction`/`pgbTimeout`. Noreply commits.
- **Lab facts (recon 2026-06-29):** PGO 6.0.2, CRD v1. `spec.backups.pgbackrest.restore`: `enabled <bool> required`, `repoName <string> required`, `options <[]string>` (PITR etc.). Trigger annotation `postgres-operator.crunchydata.com/pgbackrest-restore=<unique>`. cosmos-pg repo = `repo1`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/pgrestore_inplace.go` (package `data`) — `inPlaceRestorePatch` + `Resources.RestoreInPlace`.
- `installer/internal/tui/monitor/data/pgrestore_inplace_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — extend `Resources` with `RestoreInPlace`.
- `installer/internal/tui/monitor/monitor.go` — generalize `showTypedConfirm` (run the action's exec); add a `confirmLabel` action field; add the "Restore in place" action to the `postgrescluster` case.

---

## Task 1: data — inPlaceRestorePatch + RestoreInPlace (PURE + wrapper)

**Files:**
- Create: `data/pgrestore_inplace.go`, `data/pgrestore_inplace_test.go`
- Modify: `data/kube.go`

**Interfaces:**
- Produces: `func inPlaceRestorePatch(repo, stamp string, options []string) string`; `Resources` gains `RestoreInPlace(namespace, cluster string, options []string) (string, int, error)`.

- [ ] **Step 1: Write the failing test** — `data/pgrestore_inplace_test.go`:

```go
package data

import (
	"encoding/json"
	"testing"
)

func TestInPlaceRestorePatch(t *testing.T) {
	var p struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Backups struct {
				Pgbackrest struct {
					Restore struct {
						Enabled  bool     `json:"enabled"`
						RepoName string   `json:"repoName"`
						Options  []string `json:"options"`
					} `json:"restore"`
				} `json:"pgbackrest"`
			} `json:"backups"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(inPlaceRestorePatch("repo1", "STAMP", nil)), &p); err != nil {
		t.Fatalf("patch not valid json: %v", err)
	}
	if !p.Spec.Backups.Pgbackrest.Restore.Enabled {
		t.Fatal("restore.enabled must be true")
	}
	if p.Spec.Backups.Pgbackrest.Restore.RepoName != "repo1" {
		t.Fatalf("repoName wrong: %q", p.Spec.Backups.Pgbackrest.Restore.RepoName)
	}
	if p.Metadata.Annotations["postgres-operator.crunchydata.com/pgbackrest-restore"] != "STAMP" {
		t.Fatalf("restore annotation wrong: %+v", p.Metadata.Annotations)
	}
}

func TestInPlaceRestorePatch_PITROptions(t *testing.T) {
	out := inPlaceRestorePatch("repo1", "S", []string{"--type=time", "--target=2026-06-29 12:00:00"})
	if !contains(out, "--type=time") { // contains() lives in pgbackrest_test.go (same package)
		t.Fatalf("PITR options not threaded: %s", out)
	}
}
```

(If a `contains` helper isn't already in the `data` test package, use `strings.Contains(out, "--type=time")` directly with a `strings` import.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestInPlaceRestorePatch -v`
Expected: FAIL — `undefined: inPlaceRestorePatch`.

- [ ] **Step 3: Implement** — create `data/pgrestore_inplace.go`. Read `pgbackrest.go` for `pgbRun`/`pgbTimeout` + how `TriggerBackup` discovers the first repo + `runAction`. Then:

```go
package data

import (
	"encoding/json"
	"strings"
)

// inPlaceRestorePatch is the merge-patch that triggers a DESTRUCTIVE in-place
// pgBackRest restore: it enables in-place restore for the given repo AND sets the
// restore trigger annotation. PGO then wipes + restores the cluster from the backup.
func inPlaceRestorePatch(repo, stamp string, options []string) string {
	restore := map[string]any{"enabled": true, "repoName": repo}
	if len(options) > 0 {
		restore["options"] = options
	}
	b, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": map[string]string{
			"postgres-operator.crunchydata.com/pgbackrest-restore": stamp}},
		"spec": map[string]any{"backups": map[string]any{"pgbackrest": map[string]any{
			"restore": restore}}},
	})
	return string(b)
}

// RestoreInPlace performs a DESTRUCTIVE in-place restore of the cluster from its
// first pgBackRest repo (default repo1) — overwrites the cluster's data. The
// caller MUST gate this behind the typed-cluster-name confirm.
func (execResources) RestoreInPlace(namespace, cluster string, options []string) (string, int, error) {
	repo := "repo1"
	if out, err := pgbRun("get", "postgrescluster", cluster, "-n", namespace,
		"-o", "jsonpath={.spec.backups.pgbackrest.repos[0].name}"); err == nil {
		if r := strings.TrimSpace(string(out)); r != "" {
			repo = r
		}
	}
	stamp := nowStamp() // see below
	return runAction([]string{"patch", "postgrescluster", cluster, "-n", namespace,
		"--type", "merge", "-p", inPlaceRestorePatch(repo, stamp, options)})
}
```

The annotation needs a unique value per trigger. `data` has no clock helper and must stay testable — pass the stamp from the monitor instead: change the signature to `RestoreInPlace(namespace, cluster, stamp string, options []string)` and DROP the `nowStamp()` placeholder; the monitor supplies `time.Now()`-derived stamp (mirror how the trigger-backup action stamps once per menu-open). Update the interface + the test call sites accordingly. (i.e. `inPlaceRestorePatch(repo, stamp, options)` with the caller-supplied stamp.)

Then extend `Resources` in `kube.go`:

```go
	RestoreInPlace(namespace, cluster, stamp string, options []string) (string, int, error)
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/pgrestore_inplace.go internal/tui/monitor/data/pgrestore_inplace_test.go internal/tui/monitor/data/kube.go`
Expected: PASS; gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/pgrestore_inplace.go installer/internal/tui/monitor/data/pgrestore_inplace_test.go installer/internal/tui/monitor/data/kube.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): in-place pgBackRest restore patch + RestoreInPlace wrapper (destructive)"
```

---

## Task 2: monitor — generalize the typed-name gate + the in-place restore action

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `showTypedConfirm`, the `action` struct, `actionsFor` (postgrescluster case), `m.res.RestoreInPlace`, `executePending`.

- [ ] **Step 1: Add a `confirmLabel` field + generalize `showTypedConfirm`**

Add `confirmLabel string` to the `action` struct (the typed-name modal's confirm-button text; default "Delete" when empty — back-compat). In `showTypedConfirm`, change the confirm button so it (a) labels with `a.confirmLabel` (or "Delete" if empty), and (b) runs the action's OWN exec when set, falling back to the legacy delete build when `a.exec == nil`:

```go
	label := a.confirmLabel
	if label == "" {
		label = "Delete"
	}
	form.AddButton(label, func() {
		typed := strings.TrimSpace(form.GetFormItem(0).(*tview.InputField).GetText())
		if typed != a.name {
			return // name mismatch (incl. empty) → no-op; operator can correct or Cancel
		}
		act := a
		if act.exec == nil {
			// legacy Delete path (unchanged): build the delete exec here.
			act.command = fmt.Sprintf("kubectl delete %s -n %s %s", a.kind, a.namespace, a.name)
			act.preview = fmt.Sprintf("Delete %s %s/%s", a.kind, a.namespace, a.name)
			act.exec = func() (string, int, error) { return m.res.Delete(a.kind, a.namespace, a.name) }
		}
		m.pending = act
		m.executePending()
	})
```

(The Delete action sets NO `exec`/`confirmLabel` → identical behavior. Any typed-name action that DOES set `exec` runs it. Keep the rest of `showTypedConfirm` — the red ⚠ title, the box-contained field — as-is.)

- [ ] **Step 2: Add the "Restore in place" action to the postgrescluster case**

In `actionsFor`'s `case "postgrescluster":`, append a THIRD action (after Trigger backup + Restore to new cluster):

```go
		{
			label: "Restore in place (⚠ overwrites data)", auditAction: "restore-in-place",
			needsTypedName: true, confirmLabel: "Restore in place",
			kind: dt.kind, namespace: dt.namespace, name: dt.name,
			command: fmt.Sprintf("kubectl patch postgrescluster %s -n %s (in-place restore)", dt.name, dt.namespace),
			preview: fmt.Sprintf("⚠ DESTRUCTIVE: restore %s/%s IN PLACE from its latest backup.\n\nThis OVERWRITES the current database — data written since the backup is LOST.\nType the cluster name to confirm.", dt.namespace, dt.name),
			exec: func() (string, int, error) {
				return m.res.RestoreInPlace(dt.namespace, dt.name, time.Now().UTC().Format(time.RFC3339), nil)
			},
		},
```

(Destructive → `needsTypedName:true` routes to the generalized `showTypedConfirm`, which now runs THIS action's `exec` [RestoreInPlace] after the operator types the cluster name. Audited `restore-in-place`. The `stamp` is captured at menu-open via `time.Now()` in the exec closure — fine for a per-trigger unique annotation.)

- [ ] **Step 3: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Restore-in-place action (destructive, typed-name gated) + generalize typed-name modal"
```

---

## Task 3: Lab smoke (controller-driven) — on a THROWAWAY CLONE, never cosmos-pg

- [ ] **Step 1: Cross-compile + deliver**, then create + prepare a throwaway clone.

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
# Create a throwaway clone of cosmos-pg + a backup in ITS repo so it has something to restore from.
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
kubectl get postgrescluster cosmos-pg-iptest -n cosmos >/dev/null 2>&1 || \
  kubectl get postgrescluster cosmos-pg -n cosmos -o json | jq '.metadata={name:"cosmos-pg-iptest",namespace:"cosmos"} | del(.status) | .spec.dataSource={postgresCluster:{clusterName:"cosmos-pg",repoName:"repo1"}} | (.spec.backups.pgbackrest|=del(.restore,.manual))' | kubectl create -f -
echo "waiting for the clone to be ready…"; for i in $(seq 24); do [ "$(kubectl get postgrescluster cosmos-pg-iptest -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}' 2>/dev/null)" = "1" ] && break; sleep 10; done
echo "clone ready=$(kubectl get postgrescluster cosmos-pg-iptest -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}')"
# Give the clone its own backup so in-place restore has a target.
kubectl patch postgrescluster cosmos-pg-iptest -n cosmos --type merge -p '{"spec":{"backups":{"pgbackrest":{"manual":{"repoName":"repo1"}}}},"metadata":{"annotations":{"postgres-operator.crunchydata.com/pgbackrest-backup":"ip-smoke-1"}}}'
echo "waiting for the clone backup…"; for i in $(seq 18); do kubectl get pods -n cosmos --no-headers 2>/dev/null | grep -q 'cosmos-pg-iptest-backup.*Completed' && break; sleep 10; done
kubectl get pods -n cosmos --no-headers 2>/dev/null | grep cosmos-pg-iptest-backup | tail -1
EOF
```

- [ ] **Step 2: Drive the in-place restore ON THE CLONE via the TUI (typed-name gate)**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
echo "### clone generation BEFORE: $(kubectl get postgrescluster cosmos-pg-iptest -n cosmos -o jsonpath='{.metadata.generation}')"
tmux kill-server 2>/dev/null || true; sleep 0.5; tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon '9'; sleep 3                      # backups view
# select the iptest cluster's row (the backups view lists per-cluster rows; navigate to a cosmos-pg-iptest row)
tmux send-keys -t mon 'j'; sleep 0.5
echo "=== rows (find a cosmos-pg-iptest row to target) ==="; tmux capture-pane -t mon -p | sed -n '3,12p'
EOF
```
(Controller: from the captured rows, `j`-navigate to a `cosmos-pg-iptest` row, then drive `a` → "Restore in place" → Tab to it → Enter → type `cosmos-pg-iptest` in the gate → confirm. Capture the menu title to CONFIRM the target is `cosmos-pg-iptest` BEFORE confirming — never the wrong cluster. Then:)

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
sleep 5
echo "### restore.enabled set on the clone?: $(kubectl get postgrescluster cosmos-pg-iptest -n cosmos -o jsonpath='{.spec.backups.pgbackrest.restore.enabled}')"
echo "### restore annotation set?: $(kubectl get postgrescluster cosmos-pg-iptest -n cosmos -o jsonpath='{.metadata.annotations.postgres-operator\.crunchydata\.com/pgbackrest-restore}')"
echo "### a restore Job kicked off (PGO accepted the in-place restore)?:"; kubectl get jobs -n cosmos 2>/dev/null | grep -iE 'iptest.*(restore|pgbackrest)' | tail -2
echo "### source cosmos-pg UNTOUCHED?: ready=$(kubectl get postgrescluster cosmos-pg -n cosmos -o jsonpath='{.status.instances[0].readyReplicas}') (must stay healthy)"
echo "=== audit (restore-in-place) ==="; grep restore-in-place ~/.local/state/srectl/platform-actions.jsonl 2>/dev/null | tail -1
EOF
```
Expected (controller-verified): the action menu offers **Trigger backup / Restore to new cluster / Restore in place (⚠)**; selecting Restore-in-place opens the RED typed-name gate; a wrong name is BLOCKED (re-verify on a throwaway), the EXACT name `cosmos-pg-iptest` triggers it; `spec.backups.pgbackrest.restore.enabled=true` + repoName + the restore annotation are set on the CLONE; PGO starts an in-place restore Job for the clone; `cosmos-pg` stays healthy throughout; audited `restore-in-place`.

- [ ] **Step 3: CLEAN UP the throwaway clone**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'kubectl delete postgrescluster cosmos-pg-iptest -n cosmos --ignore-not-found; sleep 3; echo "clusters now: $(kubectl get postgrescluster -n cosmos --no-headers | awk "{print \$1}" | tr "\n" " ")"'
```
(Record the smoke outcome in the ledger. This is a DESTRUCTIVE action — the user watches; loop them in for the live drive before merge.)

---

## Self-Review

**1. Spec coverage (P4 in-place restore):** the destructive in-place restore (overwrite from backup, typed-name gated, PITR via options) → Tasks 1+2. Both restore modes now exist: clone-to-new (non-destructive, merged) + in-place (destructive, this slice).

**2. Placeholder scan:** Task 1 ships the pure patch + the wrapper (TDD, real PGO shape). Task 2 generalizes the typed-name gate (back-compat for Delete) + the destructive action. Task 3 proves it on a THROWAWAY clone (never cosmos-pg) + verifies source-untouched + cleans up. No TODO/"similar to".

**3. Type consistency:** `inPlaceRestorePatch`/`RestoreInPlace` (T1) → the `Restore in place` action's `exec` (T2). `RestoreInPlace(ns,cluster,stamp,options)` added to the interface (T1). The action carries `needsTypedName:true` + `confirmLabel:"Restore in place"` → the generalized `showTypedConfirm` (T2) runs `a.exec` (RestoreInPlace) after the typed-name match → `executePending` (audited `restore-in-place`, off-UI). DESTRUCTIVE: typed-cluster-name gate + OVERWRITE warning. Delete unchanged (nil exec → legacy path). Smoke targets ONLY a throwaway clone.
