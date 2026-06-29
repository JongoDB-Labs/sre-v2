# srectl Day-2 console — Phase 4 slice 1 (backups view + trigger-backup) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the BACKUP layer (spec §7 P4): a read-only **`backups`** view listing each PostgresCluster's pgBackRest backups, plus a confirm-gated, audited **Trigger backup** action (on-demand backup via the PGO annotation). **Restore is DEFERRED** (destructive — needs the typed-cluster-name gate + the restore-on-a-copy rule; its own slice).

**Architecture:** Pure `BackupRows` parser over `pgbackrest info --output=json`; thin `Resources` exec-wrappers to discover PostgresClusters, find each cluster's pgBackRest repo-host pod, run `pgbackrest info`, and trigger a backup (annotate). The `backups` view discovers clusters, lists their backups (each row tagged with a `drillTarget{kind:"postgrescluster"}` so `a` offers the cluster action). Trigger-backup reuses the P3 action framework (`a` → confirm modal → `executePending` → audit) — a SIMPLE confirm (triggering a backup is non-destructive). Read-only fetch off the UI goroutine.

**Tech Stack:** Go 1.25, kubectl (`get postgrescluster`, `exec … pgbackrest info`, `annotate`), CrunchyData PGO/pgBackRest, the P1.x/P3.x monitor packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-day2-more`, which has the console + more-actions). Do NOT switch branches.
- **Read-only view + non-destructive action:** the `backups` view is reads only. Trigger-backup is a mutation but **non-destructive** (it starts a backup; destroys nothing) → a SIMPLE confirm gate (NOT typed-name); confirm-gated + audited + off-UI, like the other actions. Lab-only e2e.
- **Restore is OUT OF SCOPE** for this slice (destructive; deferred to its own typed-cluster-name-gated slice). Do NOT add any restore path here.
- **Exec-wrapper rule:** kubectl via the fake-backed `Resources`; `BackupRows` + the arg-builders are PURE + unit-tested.
- **Anti-freeze:** `fetchBackups` runs off the UI goroutine (a `tableView.fetch` closure); only the draw is marshalled. Bound the exec calls with a timeout.
- **Graceful degrade:** if no PostgresCluster exists, or the repo-host/`pgbackrest info` call fails, the view shows a notice (never blanks/panics).
- **Confirmed lab data (recon 2026-06-28):** PostgresCluster `cosmos-pg` in ns `cosmos`; repo-host pod label `postgres-operator.crunchydata.com/data=pgbackrest` (+ `…/cluster=<name>`); `kubectl exec -n <ns> <repo-host> -c pgbackrest -- pgbackrest info --output=json` → `[{"name":"db","status":{"code":0,"message":"ok"},"backup":[{"label":"20260626-025441F","type":"full","timestamp":{"start":1782442481,"stop":1782442587},"info":{"size":30955260,"repository":{"size":4105845}}}]}]`. Trigger: `kubectl annotate postgrescluster <name> -n <ns> postgres-operator.crunchydata.com/pgbackrest-backup=<ts> --overwrite`.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/pgbackrest.go` (package `data`) — `BackupRow` + `BackupRows` + `humanBytes` + pure arg-builders + the `Resources` exec methods.
- `installer/internal/tui/monitor/data/pgbackrest_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — extend the `Resources` interface with the 4 new methods.
- `installer/internal/tui/monitor/monitor.go` — `fetchBackups`; register the `backups` view + nav; a `postgrescluster` case in `actionsFor` (Trigger backup).

---

## Task 1: data/pgbackrest.go — BackupRows + arg-builders + Resources methods

**Files:**
- Create: `installer/internal/tui/monitor/data/pgbackrest.go`, `data/pgbackrest_test.go`
- Modify: `installer/internal/tui/monitor/data/kube.go`

**Interfaces:**
- Produces: `type BackupRow struct { Cluster, Label, Type, Started, Size string }`; `func BackupRows(infoJSON []byte, cluster string) []BackupRow`; `func humanBytes(n int64) string`; pure `pgbackrestInfoArgs`/`triggerBackupArgs`/`repoHostSelector`; `Resources` gains `PostgresClusters() ([]byte, error)`, `RepoHostPod(namespace, cluster string) (string, error)`, `PgBackrestInfo(namespace, pod string) ([]byte, error)`, `TriggerBackup(namespace, cluster, stamp string) (string, int, error)`.

- [ ] **Step 1: Write the failing test** — `data/pgbackrest_test.go`:

```go
package data

import (
	"reflect"
	"testing"
)

const pgbInfo = `[{"name":"db","status":{"code":0,"message":"ok"},"backup":[
 {"label":"20260626-025441F","type":"full","timestamp":{"start":1782442481,"stop":1782442587},"info":{"size":30955260,"repository":{"size":4105845}}},
 {"label":"20260627-010000F_20260627-013000I","type":"incr","timestamp":{"start":1782522000,"stop":1782522030},"info":{"size":31000000,"repository":{"size":120000}}}]}]`

func TestBackupRows(t *testing.T) {
	got := BackupRows([]byte(pgbInfo), "cosmos-pg")
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	if got[0].Cluster != "cosmos-pg" || got[0].Label != "20260626-025441F" || got[0].Type != "full" {
		t.Fatalf("row0 wrong: %+v", got[0])
	}
	if got[0].Size != "29.5 MB" { // repository.size 4105845 → "3.9 MB"? NO: use info.size 30955260 → "29.5 MB"
		t.Fatalf("row0 size wrong (want backup size human): %q", got[0].Size)
	}
	if got[1].Type != "incr" {
		t.Fatalf("row1 type wrong: %+v", got[1])
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1536: "1.5 KB", 30955260: "29.5 MB", 5368709120: "5.0 GB"}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Fatalf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPgBackrestArgs(t *testing.T) {
	if got := pgbackrestInfoArgs("cosmos", "cosmos-pg-repo-host-0"); !reflect.DeepEqual(got,
		[]string{"exec", "-n", "cosmos", "cosmos-pg-repo-host-0", "-c", "pgbackrest", "--", "pgbackrest", "info", "--output=json"}) {
		t.Fatalf("pgbackrestInfoArgs: %v", got)
	}
	if got := triggerBackupArgs("cosmos", "cosmos-pg", "2026-06-29T11:00:00Z"); !reflect.DeepEqual(got,
		[]string{"annotate", "postgrescluster", "cosmos-pg", "-n", "cosmos", "postgres-operator.crunchydata.com/pgbackrest-backup=2026-06-29T11:00:00Z", "--overwrite"}) {
		t.Fatalf("triggerBackupArgs: %v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestBackupRows|TestHumanBytes|TestPgBackrestArgs' -v`
Expected: FAIL — `undefined: BackupRows`.

- [ ] **Step 3: Implement** — create `data/pgbackrest.go`:

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

const pgbTimeout = 10 * time.Second

// BackupRow is one pgBackRest backup for the backups view.
type BackupRow struct {
	Cluster, Label, Type, Started, Size string
}

// pgbStanza is the subset of `pgbackrest info --output=json` we surface.
type pgbStanza struct {
	Name   string `json:"name"`
	Backup []struct {
		Label     string `json:"label"`
		Type      string `json:"type"`
		Timestamp struct {
			Start int64 `json:"start"`
		} `json:"timestamp"`
		Info struct {
			Size int64 `json:"size"`
		} `json:"info"`
	} `json:"backup"`
}

// BackupRows parses `pgbackrest info --output=json` into rows tagged with the k8s
// cluster name. Newest backups last in pgBackRest output → reverse to newest-first.
func BackupRows(infoJSON []byte, cluster string) []BackupRow {
	var stanzas []pgbStanza
	if err := json.Unmarshal(infoJSON, &stanzas); err != nil {
		return nil
	}
	var rows []BackupRow
	for _, s := range stanzas {
		for _, b := range s.Backup {
			rows = append(rows, BackupRow{
				Cluster: cluster, Label: b.Label, Type: b.Type,
				Started: time.Unix(b.Timestamp.Start, 0).UTC().Format("2006-01-02 15:04"),
				Size:    humanBytes(b.Info.Size),
			})
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// humanBytes renders a byte count as B/KB/MB/GB (1 decimal for KB+).
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func pgbackrestInfoArgs(namespace, pod string) []string {
	return []string{"exec", "-n", namespace, pod, "-c", "pgbackrest", "--", "pgbackrest", "info", "--output=json"}
}

func triggerBackupArgs(namespace, cluster, stamp string) []string {
	return []string{"annotate", "postgrescluster", cluster, "-n", namespace,
		"postgres-operator.crunchydata.com/pgbackrest-backup=" + stamp, "--overwrite"}
}

func repoHostSelector(cluster string) string {
	return "postgres-operator.crunchydata.com/data=pgbackrest,postgres-operator.crunchydata.com/cluster=" + cluster
}

func pgbRun(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgbTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", args...).Output()
}

// PostgresClusters returns `kubectl get postgrescluster -A -o json`.
func (execResources) PostgresClusters() ([]byte, error) {
	return pgbRun("get", "postgrescluster", "-A", "-o", "json")
}

// RepoHostPod returns the pgBackRest repo-host pod name for a cluster (or "" + error).
func (execResources) RepoHostPod(namespace, cluster string) (string, error) {
	out, err := pgbRun("get", "pods", "-n", namespace, "-l", repoHostSelector(cluster),
		"-o", "jsonpath={.items[0].metadata.name}")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return "", fmt.Errorf("no pgBackRest repo-host pod for %s/%s", namespace, cluster)
	}
	return name, nil
}

// PgBackrestInfo runs `pgbackrest info --output=json` in the repo-host pod.
func (execResources) PgBackrestInfo(namespace, pod string) ([]byte, error) {
	return pgbRun(pgbackrestInfoArgs(namespace, pod)...)
}

// TriggerBackup annotates the PostgresCluster to start an on-demand pgBackRest backup.
func (execResources) TriggerBackup(namespace, cluster, stamp string) (string, int, error) {
	return runAction(triggerBackupArgs(namespace, cluster, stamp))
}
```

Then extend the `Resources` interface in `kube.go` with the 4 methods:

```go
	PostgresClusters() ([]byte, error)
	RepoHostPod(namespace, cluster string) (string, error)
	PgBackrestInfo(namespace, pod string) ([]byte, error)
	TriggerBackup(namespace, cluster, stamp string) (string, int, error)
```

(The test's `TestBackupRows` size assertion: `info.size` 30955260 → `humanBytes` → `"29.5 MB"`. Confirm `humanBytes(30955260)` yields `"29.5 MB"`; if the rounding differs, adjust the test's expected string to match the implementation's output — the implementation is the source of truth for the format, the test just pins it.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/pgbackrest.go internal/tui/monitor/data/pgbackrest_test.go internal/tui/monitor/data/kube.go`
Expected: PASS (BackupRows + humanBytes + arg tests + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/pgbackrest.go installer/internal/tui/monitor/data/pgbackrest_test.go installer/internal/tui/monitor/data/kube.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): pgBackRest backup parser + discovery/info/trigger exec-wrappers"
```

---

## Task 2: monitor — the backups view

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.res.PostgresClusters`/`RepoHostPod`/`PgBackrestInfo`, `data.BackupRows`, the `tableView` registry, `cell`, `drillTarget`.
- Produces: `fetchBackups`; the `backups` view registered + nav.

- [ ] **Step 1: Add fetchBackups (discover clusters → per-cluster info → rows)**

```go
func (m *monitor) fetchBackups() tableResult {
	raw, err := m.res.PostgresClusters()
	if err != nil {
		return tableResult{title: "BACKUPS", notice: "error: " + err.Error(), isError: true}
	}
	var list struct {
		Items []struct {
			Metadata struct{ Name, Namespace string } `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil || len(list.Items) == 0 {
		return tableResult{title: "BACKUPS", notice: "no PostgresCluster found"}
	}
	res := tableResult{title: "BACKUPS"}
	for _, it := range list.Items {
		ns, cluster := it.Metadata.Namespace, it.Metadata.Name
		pod, perr := m.res.RepoHostPod(ns, cluster)
		if perr != nil {
			continue
		}
		info, ierr := m.res.PgBackrestInfo(ns, pod)
		if ierr != nil {
			continue
		}
		for _, b := range data.BackupRows(info, cluster) {
			res.rows = append(res.rows, []*tview.TableCell{
				cell(b.Cluster).SetReference(drillTarget{kind: "postgrescluster", namespace: ns, name: cluster}),
				cell(b.Label), cell(b.Type), cell(b.Started), cell(b.Size),
			})
		}
	}
	if len(res.rows) == 0 {
		res.notice = "no backups (or pgBackRest unreachable)"
		return res
	}
	res.cols = []string{"CLUSTER", "BACKUP", "TYPE", "STARTED", "SIZE"}
	return res
}
```

- [ ] **Step 2: Register the view + nav**

Add `"backups": {fetch: m.fetchBackups}` to `m.tableViews`. Append `"backups"` to `m.viewOrder` (after `falco`, before `packages`). Add a rune `case '9': m.setView("backups"); return nil` after `case '8'`. Add `9 backups` to `footerText()` (compact). (`:backups` + Tab also reach it.)

- [ ] **Step 3: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): backups view (pgBackRest backups per PostgresCluster)"
```

---

## Task 3: monitor — the Trigger backup action

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `actionsFor`, `m.res.TriggerBackup`, the `action` struct, `executePending` (audited).

- [ ] **Step 1: Add a `postgrescluster` case to actionsFor**

Each `backups` row carries `drillTarget{kind:"postgrescluster", namespace:ns, name:cluster}`, so `a` on a backup row opens actions for the cluster. Add:

```go
case "postgrescluster":
	stamp := time.Now().UTC().Format(time.RFC3339)
	return []action{{
		label: "Trigger backup", auditAction: "trigger-backup",
		kind: dt.kind, namespace: dt.namespace, name: dt.name,
		command: fmt.Sprintf("kubectl annotate postgrescluster %s -n %s pgbackrest-backup", dt.name, dt.namespace),
		preview: fmt.Sprintf("Trigger an on-demand pgBackRest backup of %s/%s?\n\nPGO starts a backup job; nothing is destroyed.", dt.namespace, dt.name),
		exec:    func() (string, int, error) { return m.res.TriggerBackup(dt.namespace, dt.name, stamp) },
	}}
```

(Trigger-backup is non-destructive → a SIMPLE confirm gate, not typed-name. It routes through `showConfirm` → `executePending` like cordon/rollout, so it's confirm-gated + audited + off-UI. The `stamp` is captured once per menu-open so the annotation value is stable through the confirm.)

- [ ] **Step 2: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Trigger backup action (on-demand pgBackRest via PGO annotation)"
```

---

## Task 4: Lab smoke (controller-driven)

- [ ] **Step 1: Cross-compile + deliver** (`tmux kill-server`, not `pkill`)

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; sleep 1; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive the backups view + a trigger-backup**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
echo "### backup jobs BEFORE: $(kubectl get pods -n cosmos --no-headers 2>/dev/null | grep -c pg-backup)"
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 150 -y 44; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon '9'; sleep 3
echo "=== BACKUPS view (key 9) ==="; tmux capture-pane -t mon -p | sed -n '1,9p'
tmux send-keys -t mon 'a'; sleep 1.2
echo "=== ACTION MENU (cluster) ==="; tmux capture-pane -t mon -p | sed -n '18,24p'
tmux send-keys -t mon Enter; sleep 1.2                # Trigger backup → confirm
tmux send-keys -t mon Enter; sleep 4                  # Confirm → execute
echo "=== RESULT ==="; tmux capture-pane -t mon -p | sed -n '19,24p'
tmux send-keys -t mon Enter; sleep 1.5; tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
sleep 4
echo "### backup annotation set?: $(kubectl get postgrescluster cosmos-pg -n cosmos -o jsonpath='{.metadata.annotations.postgres-operator\.crunchydata\.com/pgbackrest-backup}')"
echo "### a new backup job kicked off?: $(kubectl get pods -n cosmos --no-headers 2>/dev/null | grep pg-backup | tail -2)"
echo "=== audit (trigger-backup) ==="; cat ~/.local/state/srectl/platform-actions.jsonl 2>/dev/null | grep trigger-backup | tail -1
EOF
```
Expected (controller-verified): the BACKUPS view lists `cosmos-pg`'s backups (CLUSTER/BACKUP/TYPE/STARTED/SIZE — e.g. the full `20260626-…F`, 29.5 MB); `a` → "Trigger backup" → confirm → "✓ trigger-backup"; the cluster's `pgbackrest-backup` annotation is set (a new backup job appears shortly); the trigger is audited. Restore is NOT offered (deferred).

- [ ] **Step 3: Record the smoke outcome in the ledger** (note any bug + fix before the final review; no user ping — the user will watch the combined more-actions + P4 verification).

---

## Self-Review

**1. Spec coverage (P4 §7 backup):** trigger an on-demand pgBackRest backup → Task 3; list backups → Tasks 1+2. Restore (restore-to-point, typed-cluster-name, restore-on-a-copy) explicitly DEFERRED to its own slice. Per-digest signature/SLSA, ConMon export → later phases.

**2. Placeholder scan:** Task 1 ships the full parser + arg-builders + exec methods with a real-shape fixture (the recon JSON). Tasks 2–3 wire the view + action (reusing the reviewed `tableView`/`drillTarget`/`showConfirm`/`executePending`), build+suite-gated, smoke-proven (Task 4). No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `Resources.PostgresClusters`/`RepoHostPod`/`PgBackrestInfo`/`TriggerBackup` (Task 1) consumed by `fetchBackups` (Task 2) + the `Trigger backup` action's `exec` (Task 3). `BackupRows`+`humanBytes` (Task 1) → `fetchBackups` rows. Backup rows carry `drillTarget{kind:"postgrescluster",ns,cluster}` (Task 2) → `actionsFor`'s `postgrescluster` case (Task 3) → `showConfirm` → `executePending` (off-UI + audited). Non-destructive trigger = simple confirm (no typed-name). `backups` joins `m.tableViews`+`m.viewOrder`; key `9`/`:`/Tab reach it. Degrade: notice on no-cluster / repo-host-unreachable / no-backups.
