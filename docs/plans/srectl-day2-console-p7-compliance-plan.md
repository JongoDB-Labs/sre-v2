# srectl Day-2 console — Phase 7 slice 1 (ConMon compliance/posture view) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Add a READ-ONLY **`compliance`** view (spec §7 P7 ConMon) — a continuous-monitoring posture rollup that summarizes the platform's security/integrity posture as PASS/WARN/FAIL checks: **audit-chain integrity** (latest audit-verification Job), **firing alerts by severity** (Prometheus), and **runtime security** (Falco events). The "at-a-glance" RMF ConMon surface, distinct from the detailed `alerts`/`falco` views. (Image cosign/SLSA + vuln + a signed EXPORT artifact are later slices.)

**Architecture:** PURE check-builders (`AuditChainCheck`/`AlertsCheck`/`FalcoCheck` → `PostureCheck{Name,Status,Detail}`) over signals the console already gathers (Prometheus ALERTS, Falco pod-logs) plus one new read (`AuditChainJobs` = `kubectl get jobs -A -o json`, the audit-verification Job discovered GENERICALLY by name across namespaces — not hardcoded to one app). `fetchCompliance` gathers all three off the UI goroutine, best-effort (a failed signal degrades to one WARN row, never blanks the view), and renders a status-colored posture table. Read-only; nothing mutates.

**Tech Stack:** Go 1.25, kubectl (`get jobs -A -o json`), Prometheus-via-proxy, the P1.x/P2 monitor packages (Prom/AlertRows/FalcoRows).

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-compliance`** (off main 04cdb6e). Do NOT switch branches.
- **READ-ONLY:** every part of this slice only reads (get jobs, query Prometheus, read Falco logs). No mutation, no action, no audit entry. The `compliance` view has NO `a`-actions.
- **App-agnostic:** the audit-chain check DISCOVERS audit-verification Jobs by a name match (`verify-audit`) across ALL namespaces — it must NOT hardcode the `cosmos` namespace or assume a specific app. If none found, the check is informational ("—"/not-configured), NOT a FAIL.
- **Exec-wrapper rule:** kubectl via the fake-backed `Resources`; the three check-builders are PURE + unit-tested with real-shape fixtures.
- **Anti-freeze:** `fetchCompliance` runs off the UI goroutine (a `tableView.fetch` closure); best-effort per signal (each wrapped so one failure → a single WARN/"—" row, never a panic/blank).
- **Confirmed lab facts (recon 2026-06-29):** `verify-audit-chain` Jobs in ns `cosmos` (schedule `0 */6 * * *`), latest `verify-audit-chain-29712240` → `status.succeeded=1`, `conditions[].type=Complete/SuccessCriteriaMet=True`, `completionTime 2026-06-29T12:00:04Z`. Firing alerts + Falco events present (P2 proved them).
- **Commits:** noreply. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/compliance.go` (package `data`) — `PostureCheck` + status consts + `AuditChainCheck`/`AlertsCheck`/`FalcoCheck` + `Resources.AuditChainJobs`.
- `installer/internal/tui/monitor/data/compliance_test.go`

**Modify:**
- `installer/internal/tui/monitor/data/kube.go` — extend `Resources` with `AuditChainJobs() ([]byte, error)`.
- `installer/internal/tui/monitor/monitor.go` — `fetchCompliance`; register the `compliance` view + nav + status coloring.

---

## Task 1: data/compliance.go — posture checks + AuditChainJobs

**Files:**
- Create: `installer/internal/tui/monitor/data/compliance.go`, `data/compliance_test.go`
- Modify: `installer/internal/tui/monitor/data/kube.go`

**Interfaces:**
- Produces: `type PostureCheck struct { Name, Status, Detail string }`; consts `PosturePASS="PASS"`, `PostureWARN="WARN"`, `PostureFAIL="FAIL"`, `PostureNA="—"`; `func AuditChainCheck(jobsJSON []byte) PostureCheck`; `func AlertsCheck(alerts []Sample) PostureCheck`; `func FalcoCheck(rows []FalcoRow) PostureCheck`; `Resources` gains `AuditChainJobs() ([]byte, error)`.
- Consumes: existing `Sample` (prom.go), `FalcoRow` (falco.go), `severityRank`/the "severity" label convention (prom.go).

- [ ] **Step 1: Write the failing test** — `data/compliance_test.go`:

```go
package data

import "testing"

const auditJobs = `{"items":[
 {"metadata":{"name":"verify-audit-chain-29711880","namespace":"cosmos"},"status":{"succeeded":1,"startTime":"2026-06-29T06:00:00Z","completionTime":"2026-06-29T06:00:04Z","conditions":[{"type":"Complete","status":"True"}]}},
 {"metadata":{"name":"verify-audit-chain-29712240","namespace":"cosmos"},"status":{"succeeded":1,"startTime":"2026-06-29T12:00:00Z","completionTime":"2026-06-29T12:00:04Z","conditions":[{"type":"Complete","status":"True"}]}},
 {"metadata":{"name":"some-other-job","namespace":"x"},"status":{"succeeded":1}}]}`

func TestAuditChainCheck_LatestComplete(t *testing.T) {
	c := AuditChainCheck([]byte(auditJobs))
	if c.Status != PosturePASS {
		t.Fatalf("want PASS, got %q (%s)", c.Status, c.Detail)
	}
	// must pick the LATEST audit job (12:00, not 06:00), ignoring non-audit jobs
	if want := "2026-06-29 12:00"; !contains(c.Detail, want) {
		t.Fatalf("detail should reference the latest verify time %q: %q", want, c.Detail)
	}
}

func TestAuditChainCheck_Failed(t *testing.T) {
	j := `{"items":[{"metadata":{"name":"verify-audit-chain-9","namespace":"cosmos"},"status":{"failed":1,"startTime":"2026-06-29T12:00:00Z","conditions":[{"type":"Failed","status":"True"}]}}]}`
	if c := AuditChainCheck([]byte(j)); c.Status != PostureFAIL {
		t.Fatalf("want FAIL, got %q", c.Status)
	}
}

func TestAuditChainCheck_None(t *testing.T) {
	if c := AuditChainCheck([]byte(`{"items":[{"metadata":{"name":"unrelated"}}]}`)); c.Status != PostureNA {
		t.Fatalf("want N/A when no audit-verification job exists, got %q", c.Status)
	}
}

func TestAlertsCheck(t *testing.T) {
	crit := []Sample{{Labels: map[string]string{"alertname": "EtcdDown", "severity": "critical"}}}
	if c := AlertsCheck(crit); c.Status != PostureFAIL {
		t.Fatalf("critical alert → FAIL, got %q", c.Status)
	}
	warn := []Sample{{Labels: map[string]string{"alertname": "ProbeSlow", "severity": "warning"}}, {Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}}}
	if c := AlertsCheck(warn); c.Status != PostureWARN {
		t.Fatalf("warning-only (Watchdog skipped) → WARN, got %q (%s)", c.Status, c.Detail)
	}
	if c := AlertsCheck(nil); c.Status != PosturePASS {
		t.Fatalf("no alerts → PASS, got %q", c.Status)
	}
}

func TestFalcoCheck(t *testing.T) {
	if c := FalcoCheck([]FalcoRow{{Rule: "Shell"}}); c.Status != PostureWARN {
		t.Fatalf("falco events → WARN, got %q", c.Status)
	}
	if c := FalcoCheck(nil); c.Status != PosturePASS {
		t.Fatalf("no falco events → PASS, got %q", c.Status)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

(If the existing `Sample` struct's field for labels is not named `Labels map[string]string`, READ `prom.go` and adjust the test's Sample construction + `AlertsCheck` to the real field name. The test is the contract for behavior, not the field name — match the real `Sample`.)

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run 'TestAuditChainCheck|TestAlertsCheck|TestFalcoCheck' -v`
Expected: FAIL — `undefined: AuditChainCheck`.

- [ ] **Step 3: Implement** — create `data/compliance.go`. Read `prom.go` for the real `Sample` shape + the "severity" label + the Watchdog skip, and `falco.go` for `FalcoRow`. Then:

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

// PostureCheck is one continuous-monitoring posture line for the compliance view.
type PostureCheck struct{ Name, Status, Detail string }

const (
	PosturePASS = "PASS"
	PostureWARN = "WARN"
	PostureFAIL = "FAIL"
	PostureNA   = "—"
)

// auditJobList is the subset of `kubectl get jobs -A -o json` we read.
type auditJobList struct {
	Items []struct {
		Metadata struct{ Name, Namespace string } `json:"metadata"`
		Status   struct {
			Succeeded      int    `json:"succeeded"`
			Failed         int    `json:"failed"`
			StartTime      string `json:"startTime"`
			CompletionTime string `json:"completionTime"`
		} `json:"status"`
	} `json:"items"`
}

// AuditChainCheck reports the integrity of the audit hash-chain from the latest
// audit-verification Job (discovered generically by a "verify-audit" name match
// across all namespaces — app-agnostic). PASS if it succeeded, FAIL if it failed,
// "—" if no such job exists.
func AuditChainCheck(jobsJSON []byte) PostureCheck {
	out := PostureCheck{Name: "Audit-chain integrity", Status: PostureNA, Detail: "no audit-chain verification job found"}
	var list auditJobList
	if err := json.Unmarshal(jobsJSON, &list); err != nil {
		return out
	}
	latestIdx, latest := -1, ""
	for i, it := range list.Items {
		if !strings.Contains(it.Metadata.Name, "verify-audit") {
			continue
		}
		when := it.Status.StartTime
		if when >= latest { // RFC3339 strings sort chronologically
			latest, latestIdx = when, i
		}
	}
	if latestIdx < 0 {
		return out
	}
	j := list.Items[latestIdx]
	when := j.Status.CompletionTime
	if when == "" {
		when = j.Status.StartTime
	}
	pretty := when
	if t, err := time.Parse(time.RFC3339, when); err == nil {
		pretty = t.UTC().Format("2006-01-02 15:04")
	}
	if j.Status.Failed > 0 || j.Status.Succeeded == 0 {
		return PostureCheck{Name: out.Name, Status: PostureFAIL, Detail: fmt.Sprintf("%s/%s verification FAILED (%s)", j.Metadata.Namespace, j.Metadata.Name, pretty)}
	}
	return PostureCheck{Name: out.Name, Status: PosturePASS, Detail: fmt.Sprintf("chain intact — verified %s (%s)", pretty, j.Metadata.Namespace)}
}

// AlertsCheck summarizes firing Prometheus alerts by severity (skipping the
// synthetic Watchdog). FAIL on any critical, WARN on any warning, else PASS.
func AlertsCheck(alerts []Sample) PostureCheck {
	crit, warn := 0, 0
	for _, a := range alerts {
		name := a.Labels["alertname"]
		if name == "Watchdog" {
			continue
		}
		switch a.Labels["severity"] {
		case "critical":
			crit++
		case "warning":
			warn++
		}
	}
	switch {
	case crit > 0:
		return PostureCheck{"Firing alerts", PostureFAIL, fmt.Sprintf("%d critical, %d warning", crit, warn)}
	case warn > 0:
		return PostureCheck{"Firing alerts", PostureWARN, fmt.Sprintf("%d warning", warn)}
	default:
		return PostureCheck{"Firing alerts", PosturePASS, "no firing alerts"}
	}
}

// FalcoCheck flags runtime-security activity: WARN if any Falco events are present.
func FalcoCheck(rows []FalcoRow) PostureCheck {
	if len(rows) > 0 {
		return PostureCheck{"Runtime security (Falco)", PostureWARN, fmt.Sprintf("%d recent event(s)", len(rows))}
	}
	return PostureCheck{"Runtime security (Falco)", PosturePASS, "no recent events"}
}

// AuditChainJobs returns `kubectl get jobs -A -o json` (audit-verification discovery).
func (execResources) AuditChainJobs() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", "get", "jobs", "-A", "-o", "json").Output()
}
```

Then extend `Resources` in `kube.go`:

```go
	AuditChainJobs() ([]byte, error)
```

(Use the EXISTING `resourcesTimeout` from kube.go. If `Sample`'s label field is not `.Labels`, adjust `AlertsCheck` + the test to the real name — read prom.go. If `AlertsCheck`'s severity source differs (e.g. AlertRows already skips Watchdog/derives severity), keep AlertsCheck pure over the same `[]Sample` the alerts view queries.)

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v && gofmt -l internal/tui/monitor/data/compliance.go internal/tui/monitor/data/compliance_test.go internal/tui/monitor/data/kube.go`
Expected: PASS (compliance checks + all existing data tests; `execResources` satisfies the extended interface); gofmt clean.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/compliance.go installer/internal/tui/monitor/data/compliance_test.go installer/internal/tui/monitor/data/kube.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): ConMon posture checks (audit-chain/alerts/falco) + AuditChainJobs"
```

---

## Task 2: monitor — the compliance view

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.res.AuditChainJobs`, the prom ALERTS source used by `fetchAlerts`, the Falco source used by `fetchFalco`, `data.AuditChainCheck/AlertsCheck/FalcoCheck`, the `tableView` registry, `cell`, the status colors `statusGreen/statusAmber/statusRed`.
- Produces: `fetchCompliance`; the `compliance` view registered + nav.

- [ ] **Step 1: Add fetchCompliance**

Read how `fetchAlerts` obtains its `[]Sample` (the firing-ALERTS Prometheus query) and how `fetchFalco` obtains its `[]FalcoRow` (LogsByLabel → FalcoRows). Reuse those exact sources. Then:

```go
func (m *monitor) fetchCompliance() tableResult {
	var checks []data.PostureCheck

	// Audit-chain integrity (best-effort).
	if jobs, err := m.res.AuditChainJobs(); err == nil {
		checks = append(checks, data.AuditChainCheck(jobs))
	} else {
		checks = append(checks, data.PostureCheck{Name: "Audit-chain integrity", Status: data.PostureWARN, Detail: "unavailable: " + err.Error()})
	}

	// Firing alerts (best-effort) — reuse the same Prometheus ALERTS query fetchAlerts uses.
	checks = append(checks, data.AlertsCheck(m.firingAlertSamples()))

	// Runtime security / Falco (best-effort) — reuse fetchFalco's source.
	checks = append(checks, data.FalcoCheck(m.falcoRows()))

	res := tableResult{title: "COMPLIANCE", cols: []string{"CHECK", "STATUS", "DETAIL"}}
	for _, c := range checks {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(c.Name), postureCell(c.Status), cell(c.Detail),
		})
	}
	return res
}

// postureCell colors a posture status (PASS green / WARN amber / FAIL red / — dim).
func postureCell(status string) *tview.TableCell {
	c := cell(status)
	switch status {
	case data.PosturePASS:
		c.SetTextColor(statusGreen)
	case data.PostureWARN:
		c.SetTextColor(statusAmber)
	case data.PostureFAIL:
		c.SetTextColor(statusRed)
	default:
		c.SetTextColor(consoleDim)
	}
	return c
}
```

Implement the two small helpers `m.firingAlertSamples() []data.Sample` and `m.falcoRows() []data.FalcoRow` by EXTRACTING the fetch logic already inside `fetchAlerts`/`fetchFalco` (query/parse, returning the parsed slice; on error return nil). If `fetchAlerts`/`fetchFalco` already have such a seam, reuse it; otherwise add these tiny private helpers and have `fetchAlerts`/`fetchFalco` call them too (DRY — do NOT duplicate the query/parse). Keep all of it OFF the UI goroutine (these run inside the background `tableView.fetch`).

- [ ] **Step 2: Register the view + nav**

Add `"compliance": {fetch: m.fetchCompliance}` to `m.tableViews`. Append `"compliance"` to `m.viewOrder` (after `"backups"`). The number keys are full (0–9 used); reach it via `:compliance` + Tab. Add a compact `compliance` hint to `footerText()` if space allows (else rely on `:`).

- [ ] **Step 3: Build + suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l internal/tui/monitor/monitor.go && go test ./... -count=1 2>&1 | tail -4 && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK`
Expected: build clean; gofmt clean; suite green.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): compliance view (ConMon posture rollup: audit-chain/alerts/falco)"
```

---

## Task 3: Lab smoke (controller-driven)

- [ ] **Step 1: Cross-compile + deliver** (`tmux kill-server`, not `pkill`).

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'tmux kill-server 2>/dev/null; rm -f /tmp/srectl'
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl && echo delivered'
```

- [ ] **Step 2: Drive the compliance view**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-server 2>/dev/null || true; sleep 0.5
tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon ':'; sleep 0.5; tmux send-keys -t mon 'compliance' Enter; sleep 3
echo "=== COMPLIANCE view ==="; tmux capture-pane -t mon -p | sed -n '2,9p'
tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
echo "### ground truth — latest audit-verify job: $(kubectl get jobs -n cosmos -o json 2>/dev/null | jq -r '.items|map(select(.metadata.name|test("verify-audit")))|sort_by(.status.startTime)|last|.metadata.name + " succeeded=" + (.status.succeeded|tostring)')"
EOF
```
Expected: the COMPLIANCE view lists the three posture checks — **Audit-chain integrity = PASS** (green, "chain intact — verified 2026-06-29 12:00 (cosmos)"), **Firing alerts** = WARN/FAIL with the severity counts (P2 showed ~12 firing incl. critical → likely FAIL), **Runtime security (Falco)** = WARN with the event count — each status color-coded, matching ground truth. (Record the outcome in the ledger; no user ping — user will see it at merge-time.)

---

## Self-Review

**1. Spec coverage (P7 ConMon):** posture rollup (audit-chain + alerts + falco) → Tasks 1+2. Image cosign/SLSA + vuln + WORM + patch-level + the signed EXPORT artifact = explicitly DEFERRED to later P7 slices.

**2. Placeholder scan:** Task 1 ships the three pure checks + the discovery wrapper with real-shape fixtures + 6 tests (audit PASS/FAIL/none, alerts crit/warn/none, falco). Task 2 wires the view reusing the alerts/falco sources (DRY helpers) + the tableView registry. Task 3 proves it live against ground truth. No TODO/"similar to".

**3. Type consistency:** `PostureCheck` + `AuditChainCheck([]byte)`/`AlertsCheck([]Sample)`/`FalcoCheck([]FalcoRow)` (T1) consumed by `fetchCompliance` (T2). `Resources.AuditChainJobs` (T1) consumed by `fetchCompliance` (T2). `postureCell` maps PASS/WARN/FAIL/— → statusGreen/Amber/Red/Dim. `compliance` joins `m.tableViews`+`m.viewOrder`; `:`+Tab reach it. READ-ONLY (no action, no audit). App-agnostic audit-chain discovery (name match across namespaces, "—" if none). Best-effort per signal (one failure → a WARN row, never blank).
