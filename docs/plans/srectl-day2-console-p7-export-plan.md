# srectl Day-2 console — Phase 7 slice 2 (ConMon export artifact) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a **ConMon export** to the Day-2 console (spec §7 P7): on the `compliance` view, press **`e`** to gather the live security/integrity posture and write a structured **JSON RMF posture artifact** to the host (alongside the audit log). This is the exportable continuous-monitoring artifact — slice 1 captures the posture checks + metadata + summary; **signing / hash-chaining / the exact RMF schema are a deferred follow-on** (a user decision).

**Architecture:** A PURE `BuildPostureReport(checks, ctx, tool, generatedAt)` that marshals an indented JSON `PostureReport` (schema/tool/time/context + a computed PASS/WARN/FAIL/NA summary + the checks) — fully unit-testable. `ConmonExportPath`/`WriteReport` put it under the same XDG state dir as the audit log. The compliance gather is DRY-extracted into `gatherPostureChecks()` (shared by `fetchCompliance` and the export). `exportPosture()` runs the gather+build+write OFF the UI goroutine and shows the result path via the existing `showResult` modal. Non-destructive (writes a new artifact file; reads only).

**Tech Stack:** Go 1.25, `encoding/json`, `os` (MkdirAll/WriteFile), the P7-compliance posture checks.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-export`** (off main, which has the merged compliance view). Do NOT switch branches. (The controller creates the branch before Task 1.)
- **Non-destructive:** the export only READS (gathers posture) and WRITES A NEW artifact file under the user's XDG state dir. It must not touch the cluster's state, overwrite unrelated files, or mutate anything. Each export is a fresh timestamped file (no clobber).
- **Anti-freeze:** `exportPosture` does its cluster-I/O gather + file write in a BACKGROUND goroutine; only the result modal is marshalled back via `QueueUpdateDraw`. `BuildPostureReport`/`ConmonExportPath` are pure; `WriteReport` does the file I/O.
- **DRY:** the posture gather is extracted ONCE (`gatherPostureChecks`) and shared by `fetchCompliance` + `exportPosture` — do not duplicate the audit-chain/alerts/falco gather.
- **Timestamps:** the monitor (real runtime) supplies `time.Now()`; the pure builder/path take the time as a STRING param (testable).
- **Reuse:** `ConmonExportPath` reuses the SAME XDG state base dir as `data.AuditPath()` (`~/.local/state/srectl/…`, honoring `XDG_STATE_HOME`) — factor/share the dir helper in `audit.go`, do not re-derive a different base.
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/conmon.go` (package `data`) — `PostureReport`/`PostureSummary` + `BuildPostureReport` + `ConmonExportPath` + `WriteReport`.
- `installer/internal/tui/monitor/data/conmon_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/audit.go` — expose the XDG state-dir helper (if `AuditPath` inlines it) so `ConmonExportPath` shares it.
- `installer/internal/tui/monitor/monitor.go` — `gatherPostureChecks` (extract from `fetchCompliance`); `exportPosture`; the `e` key on the compliance view; footer hint.

---

## Task 1: data/conmon.go — the posture report artifact

**Files:**
- Create: `installer/internal/tui/monitor/data/conmon.go`, `data/conmon_test.go`
- Modify (only if needed to share the dir helper): `installer/internal/tui/monitor/data/audit.go`

**Interfaces:**
- Produces: `type PostureSummary struct{...}`; `type PostureReport struct{...}`; `func BuildPostureReport(checks []PostureCheck, kubeContext, tool, generatedAt string) ([]byte, error)`; `func ConmonExportPath(stamp string) string`; `func WriteReport(path string, data []byte) error`.

- [ ] **Step 1: Write the failing test** — `data/conmon_test.go`:

```go
package data

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildPostureReport(t *testing.T) {
	checks := []PostureCheck{
		{Name: "Audit-chain integrity", Status: PosturePASS, Detail: "chain intact"},
		{Name: "Firing alerts", Status: PostureFAIL, Detail: "2 critical"},
		{Name: "Runtime security (Falco)", Status: PostureWARN, Detail: "50 events"},
		{Name: "Image signing", Status: PostureNA, Detail: "not checked"},
	}
	raw, err := BuildPostureReport(checks, "cosmos-k8s", "0.0.0-dev", "2026-06-29T13:00:00Z")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var r PostureReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("output not valid json: %v", err)
	}
	if r.GeneratedAt != "2026-06-29T13:00:00Z" || r.Context != "cosmos-k8s" || r.Tool != "0.0.0-dev" {
		t.Fatalf("metadata wrong: %+v", r)
	}
	if len(r.Checks) != 4 {
		t.Fatalf("want 4 checks, got %d", len(r.Checks))
	}
	if r.Summary.Pass != 1 || r.Summary.Fail != 1 || r.Summary.Warn != 1 || r.Summary.NA != 1 {
		t.Fatalf("summary counts wrong: %+v", r.Summary)
	}
	// overall is FAIL when any check FAILs
	if r.Summary.Overall != PostureFAIL {
		t.Fatalf("overall should be FAIL, got %q", r.Summary.Overall)
	}
	// indented (human-readable) JSON
	if !strings.Contains(string(raw), "\n  ") {
		t.Fatalf("expected indented JSON")
	}
}

func TestBuildPostureReport_OverallPrecedence(t *testing.T) {
	warnOnly := []PostureCheck{{Name: "a", Status: PosturePASS}, {Name: "b", Status: PostureWARN}, {Name: "c", Status: PostureNA}}
	if r, _ := buildReport(t, warnOnly); r.Summary.Overall != PostureWARN {
		t.Fatalf("warn (no fail) → overall WARN, got %q", r.Summary.Overall)
	}
	allPass := []PostureCheck{{Name: "a", Status: PosturePASS}, {Name: "b", Status: PosturePASS}}
	if r, _ := buildReport(t, allPass); r.Summary.Overall != PosturePASS {
		t.Fatalf("all pass → overall PASS, got %q", r.Summary.Overall)
	}
}

func buildReport(t *testing.T, checks []PostureCheck) (PostureReport, []byte) {
	t.Helper()
	raw, err := BuildPostureReport(checks, "ctx", "v", "t")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var r PostureReport
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return r, raw
}

func TestConmonExportPath(t *testing.T) {
	p := ConmonExportPath("20260629-130000")
	if !strings.HasSuffix(p, "conmon-posture-20260629-130000.json") {
		t.Fatalf("path suffix wrong: %q", p)
	}
	if !strings.Contains(p, "srectl") {
		t.Fatalf("path should be under the srectl state dir: %q", p)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestBuildPostureReport|TestConmonExportPath' -v`
Expected: FAIL — `undefined: BuildPostureReport`.

- [ ] **Step 3: Implement** — create `data/conmon.go`. First READ `audit.go` to find how `AuditPath()` derives `~/.local/state/srectl/` (XDG_STATE_HOME or `$HOME/.local/state`); reuse that exact base (factor a shared `stateDir()` helper if `AuditPath` inlines it). Then:

```go
package data

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// PostureSummary is the rollup of a posture report's checks.
type PostureSummary struct {
	Pass    int    `json:"pass"`
	Warn    int    `json:"warn"`
	Fail    int    `json:"fail"`
	NA      int    `json:"na"`
	Overall string `json:"overall"` // FAIL if any FAIL, else WARN if any WARN, else PASS
}

// PostureReport is the exportable ConMon artifact.
type PostureReport struct {
	Schema      string         `json:"schema"`
	Tool        string         `json:"tool"`
	GeneratedAt string         `json:"generatedAt"`
	Context     string         `json:"context"`
	Summary     PostureSummary `json:"summary"`
	Checks      []PostureCheck `json:"checks"`
}

const postureSchema = "srectl.conmon.posture/v1"

// BuildPostureReport renders the posture checks into an indented JSON artifact
// with a computed summary. Pure: the caller supplies the timestamp string.
func BuildPostureReport(checks []PostureCheck, kubeContext, tool, generatedAt string) ([]byte, error) {
	sum := PostureSummary{Overall: PosturePASS}
	for _, c := range checks {
		switch c.Status {
		case PosturePASS:
			sum.Pass++
		case PostureWARN:
			sum.Warn++
		case PostureFAIL:
			sum.Fail++
		default:
			sum.NA++
		}
	}
	switch {
	case sum.Fail > 0:
		sum.Overall = PostureFAIL
	case sum.Warn > 0:
		sum.Overall = PostureWARN
	default:
		sum.Overall = PosturePASS
	}
	if checks == nil {
		checks = []PostureCheck{}
	}
	return json.MarshalIndent(PostureReport{
		Schema: postureSchema, Tool: tool, GeneratedAt: generatedAt,
		Context: kubeContext, Summary: sum, Checks: checks,
	}, "", "  ")
}

// ConmonExportPath is the artifact path for a given timestamp, under the srectl
// state dir (same base as the audit log).
func ConmonExportPath(stamp string) string {
	return filepath.Join(stateDir(), "conmon-posture-"+stamp+".json")
}

// WriteReport creates the state dir if needed and writes the artifact (0644).
func WriteReport(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
```

If `audit.go` does not already have a reusable `stateDir()` (returning `~/.local/state/srectl`), add it there (extract from `AuditPath`) and have BOTH `AuditPath` and `ConmonExportPath` call it — do NOT duplicate the XDG logic. (If `AuditPath`'s base differs, match whatever it uses so both artifacts land in the same dir.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/conmon.go internal/tui/monitor/data/conmon_test.go internal/tui/monitor/data/audit.go`
Expected: PASS (report build + summary precedence + path; all existing data tests); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/conmon.go installer/internal/tui/monitor/data/conmon_test.go installer/internal/tui/monitor/data/audit.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): ConMon posture report artifact builder + export path/writer"
```

---

## Task 2: monitor — gatherPostureChecks + export action

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `fetchCompliance` (refactor target), `data.BuildPostureReport`/`ConmonExportPath`/`WriteReport`, `m.ctx`, `m.version`, `showResult`, `QueueUpdateDraw`, the input-capture rune switch.
- Produces: `gatherPostureChecks() []data.PostureCheck`; `exportPosture()`; the `e` key.

- [ ] **Step 1: Extract gatherPostureChecks**

Pull the 3-check gather (audit-chain best-effort / alerts WARN-on-error / falco WARN-on-error) OUT of `fetchCompliance` into:
```go
// gatherPostureChecks collects the ConMon posture checks (best-effort, off the UI
// goroutine). Shared by the compliance view and the export.
func (m *monitor) gatherPostureChecks() []data.PostureCheck {
	var checks []data.PostureCheck
	if jobs, err := m.res.AuditChainJobs(); err == nil {
		checks = append(checks, data.AuditChainCheck(jobs))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Audit-chain integrity", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}
	if samples, err := m.firingAlertSamples(); err == nil {
		checks = append(checks, data.AlertsCheck(samples))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Firing alerts", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}
	if rows, err := m.falcoRows(); err == nil {
		checks = append(checks, data.FalcoCheck(rows))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Runtime security (Falco)", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}
	return checks
}
```
Then make `fetchCompliance` call it:
```go
func (m *monitor) fetchCompliance() tableResult {
	checks := m.gatherPostureChecks()
	res := tableResult{title: "COMPLIANCE", cols: []string{"CHECK", "STATUS", "DETAIL"}}
	for _, c := range checks {
		res.rows = append(res.rows, []*tview.TableCell{cell(c.Name), postureCell(c.Status), cell(c.Detail)})
	}
	return res
}
```

- [ ] **Step 2: Add exportPosture (off-UI gather+write → result modal)**

```go
// exportPosture gathers the live posture and writes a JSON ConMon artifact to the
// host, then shows the path. Non-destructive; the gather + write run off the UI
// goroutine, only the result modal is marshalled back.
func (m *monitor) exportPosture() {
	go func() {
		checks := m.gatherPostureChecks()
		stamp := time.Now().UTC().Format("20060102-150405")
		path := data.ConmonExportPath(stamp)
		raw, err := data.BuildPostureReport(checks, m.ctx, m.version, time.Now().UTC().Format(time.RFC3339))
		if err == nil {
			err = data.WriteReport(path, raw)
		}
		m.app.QueueUpdateDraw(func() {
			if err != nil {
				m.showResult("ConMon export — error", err.Error())
			} else {
				m.showResult("ConMon export", "wrote "+path)
			}
		})
	}()
}
```

- [ ] **Step 3: Wire the `e` key (compliance view only) + footer hint**

In the input-capture rune switch (the non-modal/non-detail branch), add:
```go
		case 'e':
			if m.view == "compliance" {
				m.exportPosture()
			}
			return nil
```
Add a compact `e export` hint to `footerText()` (or to the compliance-context hint if the footer is view-specific). Keep existing keys unchanged.

- [ ] **Step 4: Build + suite + compile-smoke**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): export ConMon posture artifact ('e' on the compliance view)"
```

---

## Task 3: Lab smoke (controller-driven)

- [ ] **Step 1: Cross-compile + deliver** (`tmux kill-server`, not `pkill`).

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; rm -f /tmp/srectl /tmp/conmon-check'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive the export + verify the artifact**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
rm -f ~/.local/state/srectl/conmon-posture-*.json
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon ':'; sleep 0.5; tmux send-keys -t mon 'compliance' Enter; sleep 3.5
tmux send-keys -t mon 'e'; sleep 3
echo "=== export result modal ==="; tmux capture-pane -t mon -p | grep -iE 'ConMon export|wrote|conmon-posture' | head -3
tmux send-keys -t mon Enter; sleep 1; tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
echo "=== artifact written? ==="; ls -1 ~/.local/state/srectl/conmon-posture-*.json 2>/dev/null | tail -1
f=$(ls -1 ~/.local/state/srectl/conmon-posture-*.json 2>/dev/null | tail -1)
echo "=== artifact content (jq) ==="; jq '{schema, tool, generatedAt, context, summary, checks: [.checks[] | {name, status}]}' "$f" 2>/dev/null
echo "=== valid JSON? ==="; jq empty "$f" 2>/dev/null && echo VALID_JSON
EOF
```
Expected: pressing `e` on the compliance view shows "ConMon export / wrote ~/.local/state/srectl/conmon-posture-<ts>.json"; the file exists and is valid JSON with `schema=srectl.conmon.posture/v1`, the cluster context, a summary (overall FAIL given the firing critical alerts), and the 3 posture checks (audit-chain/alerts/falco) with their statuses matching the live view. (Record the outcome in the ledger.)

---

## Self-Review

**1. Spec coverage (P7 ConMon export):** gather posture → write an exportable JSON artifact → Tasks 1+2. Signing / hash-chaining / the precise RMF schema + richer signals (image SLSA, vuln, patch-level) = explicitly DEFERRED (a user loop-in + the enrich-compliance slice).

**2. Placeholder scan:** Task 1 ships the full pure builder + path + writer with real-shape tests (build/summary-precedence/path). Task 2 wires the DRY gather + the off-UI export + the `e` key. Task 3 proves the artifact on disk is valid + matches the live posture. No TODO/"similar to".

**3. Type consistency:** `BuildPostureReport([]PostureCheck,…)` (T1) consumes the P7 `PostureCheck`/`Posture*` consts. `gatherPostureChecks()` (T2) feeds BOTH `fetchCompliance` (rows) and `exportPosture` (report) — DRY. `exportPosture` runs off-UI (goroutine) + marshals only `showResult` via QueueUpdateDraw (anti-freeze). `ConmonExportPath` shares `audit.go`'s `stateDir()` (same base as the audit log). `e` is gated to `m.view=="compliance"`. Non-destructive: reads + a fresh timestamped artifact file (no clobber, no cluster mutation).
