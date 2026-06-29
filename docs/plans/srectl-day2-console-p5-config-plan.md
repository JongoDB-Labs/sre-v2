# srectl Day-2 console — Phase 5 slice 1 (config: persist + read-only view) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Make the platform's install configuration visible in the Day-2 console (spec §7 P5). (1) The installer **persists** the `Answers` as a `srectl-config` ConfigMap (emitted by `render` so it deploys with everything). (2) The console gains a READ-ONLY **`config`** view that reads it and shows Posture / Sizing / Services / SSO / Domain / Secrets. **Config MUTATIONS (toggles, esp. sensitive SSO) are DEFERRED** to a later gated slice — this slice is persist + show only.

**Architecture:** A pure `renderPlatformConfig(Answers) → ConfigMap YAML` appended to `render.Render`'s `[]File`. A pure `ConfigRows(cmJSON) → []ConfigRow` that pulls `data["answers.yaml"]` out of the ConfigMap, unmarshals `config.Answers`, and yields KEY/VALUE rows. A thin `Resources.PlatformConfig()` read + a `config` view. Read-only in the console; nothing mutates.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, kubectl (`get cm`), the installer `config`/`render` packages + the monitor.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. From `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` on branch **`feat/srectl-day2-config`** (off main, which has all of #16–#22). The controller creates the branch before Task 1.
- **READ-ONLY in the console:** the `config` view only reads (`kubectl get cm srectl-config -n sre-system`). No mutation, no action, no toggle. (Persist is in the INSTALLER's render path, not a console action.)
- **Persist via render:** the ConfigMap is a `render.File` so it ships in the deploy artifacts (srectl's live deploy is still stubbed — the ConfigMap lands when the artifacts are applied; the lab was deployed out-of-band so the smoke SEEDS a representative ConfigMap to prove the console view).
- **`config` import:** the monitor may import `internal/config` for the `Answers` type (config is a leaf package — no cycle). Reuse `Answers` + its yaml tags; do NOT redefine.
- **Exec-wrapper rule; pure logic unit-tested; Go 1.25.** Noreply commits.
- **Lab fact:** `Answers{Posture, Sizing, Services []string, SSO, OIDCIssuer, Domain, Secrets, AgePublicKey}` (config/answers.go). The cm = `sre-system/srectl-config`, `data["answers.yaml"] = <Answers YAML>`.

---

## File Structure

**Create:**
- `installer/internal/render/platformconfig.go` (package `render`) — `PlatformConfigFile` const + `renderPlatformConfig`.
- `installer/internal/render/platformconfig_test.go`
- `installer/internal/tui/monitor/data/platformconfig.go` (package `data`) — `ConfigRow` + `ConfigRows` + `Resources.PlatformConfig`.
- `installer/internal/tui/monitor/data/platformconfig_test.go`

**Modify:**
- `installer/internal/render/render.go` — append the config ConfigMap to `Render`'s `[]File`.
- `installer/internal/tui/monitor/data/kube.go` — extend `Resources` with `PlatformConfig`.
- `installer/internal/tui/monitor/monitor.go` — `fetchConfig`; register the `config` view + nav.

---

## Task 1: render — persist the Answers as a srectl-config ConfigMap

**Files:**
- Create: `installer/internal/render/platformconfig.go`, `platformconfig_test.go`
- Modify: `installer/internal/render/render.go`

**Interfaces:**
- Produces: `const PlatformConfigFile = "srectl-config-configmap.yaml"`; `func renderPlatformConfig(a config.Answers) (string, error)`; `Render` returns a 3rd `File`.

- [ ] **Step 1: Write the failing test** — `platformconfig_test.go`:

```go
package render

import (
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

func sampleAnswers() config.Answers {
	return config.Answers{
		Posture: config.PostureDoD, Sizing: config.SizingMedium,
		Services: []string{"cosmos", "falco"}, SSO: config.SSOKeycloak,
		Domain: "uds.dev", Secrets: config.SecretsSOPSAge, AgePublicKey: "age1xyz",
	}
}

func TestRenderPlatformConfig_RoundTrips(t *testing.T) {
	out, err := renderPlatformConfig(sampleAnswers())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// It's a k8s ConfigMap in sre-system/srectl-config with the answers under data.
	var cm struct {
		Kind     string `yaml:"kind"`
		Metadata struct{ Name, Namespace string } `yaml:"metadata"`
		Data     map[string]string                `yaml:"data"`
	}
	if err := yaml.Unmarshal([]byte(out), &cm); err != nil {
		t.Fatalf("output not valid yaml: %v", err)
	}
	if cm.Kind != "ConfigMap" || cm.Metadata.Name != "srectl-config" || cm.Metadata.Namespace != "sre-system" {
		t.Fatalf("configmap meta wrong: %+v", cm.Metadata)
	}
	// the embedded answers.yaml unmarshals back to the original Answers
	var got config.Answers
	if err := yaml.Unmarshal([]byte(cm.Data["answers.yaml"]), &got); err != nil {
		t.Fatalf("answers.yaml not valid: %v", err)
	}
	if got.Posture != config.PostureDoD || got.SSO != config.SSOKeycloak || got.Domain != "uds.dev" || len(got.Services) != 2 {
		t.Fatalf("round-trip lost data: %+v", got)
	}
}

func TestRender_IncludesPlatformConfig(t *testing.T) {
	files, err := Render(sampleAnswers(), testCatalog(t)) // testCatalog: reuse the existing render-test helper (see render_test.go)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Name == PlatformConfigFile && strings.Contains(f.Content, "kind: ConfigMap") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Render output must include the %s config ConfigMap", PlatformConfigFile)
	}
}
```

(If `render_test.go` has no reusable catalog helper, build a minimal `*catalog.Catalog` inline the same way the existing Render tests do — read `render_test.go` and match. If the `config.Posture*`/`Sizing*`/`SSO*`/`Secrets*` const names differ, use the real ones from `config/answers.go`.)

- [ ] **Step 2: Run to verify it fails** — `cd …/installer && go test ./internal/render/ -run 'TestRenderPlatformConfig|TestRender_IncludesPlatformConfig' -v` → FAIL (undefined: renderPlatformConfig).

- [ ] **Step 3: Implement** — `platformconfig.go`:

```go
package render

import (
	"fmt"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

// PlatformConfigFile holds the persisted install Answers as a k8s ConfigMap, so
// the Day-2 console can show what the platform was configured as.
const PlatformConfigFile = "srectl-config-configmap.yaml"

// renderPlatformConfig serializes the answers into a sre-system/srectl-config
// ConfigMap (data.answers.yaml). yaml.v3 block-scalars the embedded YAML.
func renderPlatformConfig(a config.Answers) (string, error) {
	answersYAML, err := yaml.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("marshal answers: %w", err)
	}
	cm := map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name":      "srectl-config",
			"namespace": "sre-system",
			"labels":    map[string]string{"app.kubernetes.io/managed-by": "srectl"},
		},
		"data": map[string]any{"answers.yaml": string(answersYAML)},
	}
	out, err := yaml.Marshal(cm)
	if err != nil {
		return "", fmt.Errorf("marshal configmap: %w", err)
	}
	return "# srectl platform config — the install Answers, for the Day-2 console.\n" + string(out), nil
}
```

Then in `render.go`'s `Render`, append the 3rd file:

```go
	platformCfg, err := renderPlatformConfig(a)
	if err != nil {
		return nil, err
	}
	return []File{
		{Name: UDSConfigFile, Content: udsCfg},
		{Name: ValuesOverlayFile, Content: overlay},
		{Name: PlatformConfigFile, Content: platformCfg},
	}, nil
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/render/ -v && gofmt -l internal/render/platformconfig.go internal/render/platformconfig_test.go internal/render/render.go` → PASS; gofmt clean. (If pre-existing Render tests assert an exact file COUNT of 2, update them to 3 — the new file is intentional.)

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/render/platformconfig.go installer/internal/render/platformconfig_test.go installer/internal/render/render.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(installer): render persists install Answers as a srectl-config ConfigMap"
```

---

## Task 2: monitor — the read-only config view

**Files:**
- Create: `installer/internal/tui/monitor/data/platformconfig.go`, `platformconfig_test.go`
- Modify: `installer/internal/tui/monitor/data/kube.go`, `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Produces: `type ConfigRow struct{ Key, Value string }`; `func ConfigRows(cmJSON []byte) []ConfigRow`; `Resources.PlatformConfig() ([]byte, error)`; `fetchConfig` + the `config` view.

- [ ] **Step 1: Write the failing test** — `data/platformconfig_test.go`:

```go
package data

import "testing"

const cmJSON = `{"data":{"answers.yaml":"posture: DoD\nsizing: Medium\nservices:\n  - cosmos\n  - falco\nsso: Keycloak\ndomain: uds.dev\nsecrets: SOPSAge\nagePublicKey: age1xyz\n"}}`

func TestConfigRows(t *testing.T) {
	rows := ConfigRows([]byte(cmJSON))
	get := func(k string) string {
		for _, r := range rows {
			if r.Key == k {
				return r.Value
			}
		}
		return "<missing>"
	}
	if get("Posture") != "DoD" {
		t.Fatalf("posture: %q", get("Posture"))
	}
	if get("SSO") != "Keycloak" {
		t.Fatalf("sso: %q", get("SSO"))
	}
	if get("Domain") != "uds.dev" {
		t.Fatalf("domain: %q", get("Domain"))
	}
	if get("Services") == "<missing>" || get("Services") == "" {
		t.Fatalf("services row missing")
	}
}

func TestConfigRows_BadJSON(t *testing.T) {
	if rows := ConfigRows([]byte("not json")); rows != nil {
		t.Fatalf("bad json → nil, got %v", rows)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/tui/monitor/data/ -run TestConfigRows -v` → FAIL.

- [ ] **Step 3: Implement** — `data/platformconfig.go` (import `internal/config` for `Answers`):

```go
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"gopkg.in/yaml.v3"
)

// ConfigRow is one KEY/VALUE line of the platform config view.
type ConfigRow struct{ Key, Value string }

// ConfigRows parses the srectl-config ConfigMap JSON (kubectl get cm -o json),
// pulls data["answers.yaml"], and renders the install config as rows. nil on error.
func ConfigRows(cmJSON []byte) []ConfigRow {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(cmJSON, &cm); err != nil {
		return nil
	}
	var a config.Answers
	if err := yaml.Unmarshal([]byte(cm.Data["answers.yaml"]), &a); err != nil {
		return nil
	}
	rows := []ConfigRow{
		{"Posture", string(a.Posture)},
		{"Sizing", string(a.Sizing)},
		{"Services", strings.Join(a.Services, ", ")},
		{"SSO", string(a.SSO)},
		{"Domain", a.Domain},
		{"Secrets", string(a.Secrets)},
	}
	if a.OIDCIssuer != "" {
		rows = append(rows, ConfigRow{"OIDC issuer", a.OIDCIssuer})
	}
	return rows
}

// PlatformConfig returns the srectl-config ConfigMap (sre-system) JSON.
func (execResources) PlatformConfig() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "kubectl", "get", "configmap", "srectl-config", "-n", "sre-system", "-o", "json").Output()
}
```

(Cast `a.Posture`/`a.Sizing`/etc. to `string` per their underlying types — read `config/answers.go` for whether they're `string`-kind named types [they are] and adjust if a field needs `fmt.Sprint`. Add `fmt` only if used.)

Extend `Resources` in `kube.go`: `PlatformConfig() ([]byte, error)`.

Add `fetchConfig` to `monitor.go`:

```go
func (m *monitor) fetchConfig() tableResult {
	raw, err := m.res.PlatformConfig()
	if err != nil {
		return tableResult{title: "CONFIG", notice: "no srectl-config found (platform not deployed via srectl, or pre-persist install)"}
	}
	rows := data.ConfigRows(raw)
	if len(rows) == 0 {
		return tableResult{title: "CONFIG", notice: "srectl-config present but unreadable"}
	}
	res := tableResult{title: "CONFIG", cols: []string{"SETTING", "VALUE"}}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Key), cell(r.Value)})
	}
	return res
}
```

Register `"config": {fetch: m.fetchConfig}` in `m.tableViews`; append `"config"` to `m.viewOrder` (after `"compliance"`); reachable via `:config`; add a footer hint if space.

- [ ] **Step 4: Run to verify pass** — `go test ./... -count=1 2>&1 | tail -4 && go build ./... && go vet ./internal/tui/monitor/... && gofmt -l … && go run ./cmd/srectl monitor --help >/dev/null && echo HELP_OK` → green.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/platformconfig.go installer/internal/tui/monitor/data/platformconfig_test.go installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): read-only config view (reads the srectl-config ConfigMap)"
```

---

## Task 3: Lab smoke (controller-driven) — SEED the config (lab wasn't deployed via srectl), then view

- [ ] **Step 1: Cross-compile + deliver, seed a representative srectl-config**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
kubectl create namespace sre-system --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1
kubectl create configmap srectl-config -n sre-system --from-literal=answers.yaml='posture: DoD
sizing: Medium
services:
  - cosmos
  - falco
  - keycloak
  - prometheus-stack
sso: Keycloak
domain: uds.dev
secrets: SOPSAge
agePublicKey: age1examplexyz
' --dry-run=client -o yaml | kubectl apply -f - 2>&1 | tail -1
EOF
```

- [ ] **Step 2: Drive the config view**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-server 2>/dev/null || true; sleep 0.5; tmux new-session -d -s mon -x 170 -y 48; sleep 0.5
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 4
tmux send-keys -t mon ':'; sleep 0.5; tmux send-keys -t mon 'config' Enter; sleep 3
echo "=== CONFIG view ==="; tmux capture-pane -t mon -p | sed -n '2,12p'
tmux send-keys -t mon q; sleep 1; tmux kill-server 2>/dev/null || true
EOF
```
Expected: the CONFIG view shows SETTING / VALUE rows — Posture DoD, Sizing Medium, Services "cosmos, falco, keycloak, prometheus-stack", SSO Keycloak, Domain uds.dev, Secrets SOPSAge — read from the seeded ConfigMap. (Record the outcome in the ledger.)

---

## Self-Review

**1. Spec coverage (P5 config):** persist the install config (render → ConfigMap) + show it read-only → Tasks 1+2. Config MUTATIONS (toggles, sensitive SSO) explicitly DEFERRED to a gated follow-up.

**2. Placeholder scan:** Task 1 ships the pure ConfigMap renderer + wires it into Render (round-trip tested). Task 2 ships the pure parser + the read-only view. Task 3 proves it on a seeded ConfigMap. No TODO/"similar to".

**3. Type consistency:** `renderPlatformConfig`/`PlatformConfigFile` (T1) extend `Render`'s output. `ConfigRows`/`ConfigRow`/`Resources.PlatformConfig` (T2) consumed by `fetchConfig` (T2). Both the render-side and the console-side serialize via the SAME `config.Answers` + yaml tags (round-trip). READ-ONLY console (no mutation/action/toggle); persist lives in render. `config` joins `m.tableViews`+`m.viewOrder`; `:config` reaches it. Degrade: notice when the ConfigMap is absent (un-persisted install) or unreadable.
