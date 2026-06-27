# srectl Day-2 console — Phase 1.1 (metrics + dashboard) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn `srectl monitor` from two tables into an observability console by adding the Prometheus metrics layer, dashboard widgets (gauge / sparkline / stat-tile), and an OVERVIEW dashboard view — the first increment of spec Phase 1.

**Architecture:** A new `data/` metrics layer wraps Prometheus via the kube-API proxy (`kubectl get --raw …/proxy/api/v1/query[_range]`) behind a fake-backed interface, with a pure JSON parser. New pure `widgets/` string-builders (bar gauge, sparkline, stat tile) render metric values as terminal graphics. A pure `views/overview` panel-builder assembles them into a dashboard, wired into the existing dark-console monitor as the default view (alongside the shipped packages/apps tables) via `tview.Pages`.

**Tech Stack:** Go 1.25, tview/tcell, the app-catalog `kubectl` exec-wrapper pattern, Prometheus HTTP API (vector/matrix JSON).

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which already has the dark-console monitor + the spec). Do NOT switch branches.
- **Exec-wrapper rule (binding):** orchestrate `kubectl` — never embed a Kubernetes client. New cluster I/O goes through a fake-backed interface so the data layer is unit-tested with no cluster.
- **Read-only:** Phase 1.1 adds no mutations. Observability only.
- **Prometheus access (verified):** proxy path `/api/v1/namespaces/monitoring/services/<name>:9090/proxy/api/v1/query[_range]`; the lab service is `kube-prometheus-stack-prometheus`. Response is `{"status":"success","data":{"resultType":"vector|matrix","result":[…]}}`. Graceful-degrade: never crash on a missing/failed metrics source — show a dim placeholder.
- **Look/branding:** the shipped dark console (canvas `consoleBg`, shared `tui` accent/selection/status); title `SRE Monitor — <version>`; never "Security Onion".
- **Testing:** every parser, widget string-builder, discovery, and panel-builder is pure + unit-tested; tview rendering + the live Prometheus round-trip = manual smoke on the lab.
- **Commits:** noreply email. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/prom.go` (package `data`) — Prometheus client: the `Raw` exec-wrapper, `Query`/`QueryRange`, the vector/matrix parser, the named PromQL catalog, service discovery.
- `installer/internal/tui/monitor/data/prom_test.go`
- `installer/internal/tui/monitor/widgets/gauge.go` (package `widgets`) — `Bar(pct, width)`.
- `installer/internal/tui/monitor/widgets/sparkline.go` — `Spark(values)`.
- `installer/internal/tui/monitor/widgets/stattile.go` — `Tile(n, label)`, `Health(ok, warn, fail)`.
- `installer/internal/tui/monitor/widgets/*_test.go`
- `installer/internal/tui/monitor/views/overview.go` (package `views`) — `BuildOverview(in Inputs) string` (the dashboard text).
- `installer/internal/tui/monitor/views/overview_test.go`

**Modify:**
- `installer/internal/tui/monitor/monitor.go` — add the OVERVIEW view (default): a `tview.Pages` main area swapping an overview `TextView` and the existing table; fetch metrics on refresh (degrade-safe); nav key `0`/`o`; footer.

> Note: Phase 1.2 (next plan) adds `data/kube.go` (broader resource fetch), the per-resource `views/{host,cluster,core,apps}.go`, the `:` command bar, and drill-in. This plan deliberately stops at the dashboard.

---

## Task 1: Prometheus response parser

**Files:**
- Create: `installer/internal/tui/monitor/data/prom.go`, `data/prom_test.go`

**Interfaces:**
- Produces (consumed by Task 3, 7): `type Sample struct { Labels map[string]string; Value float64 }`; `type Series struct { Labels map[string]string; Values []float64 }`; `func ParseVector(raw []byte) ([]Sample, error)`; `func ParseMatrix(raw []byte) ([]Series, error)`.

- [ ] **Step 1: Write the failing tests** — `data/prom_test.go` (fixtures are the real lab shapes):

```go
package data

import (
	"reflect"
	"testing"
)

const vectorJSON = `{"status":"success","data":{"resultType":"vector","result":[
 {"metric":{"__name__":"up","job":"keycloak-http","namespace":"keycloak"},"value":[1782564385.383,"1"]},
 {"metric":{"__name__":"up","job":"falco"},"value":[1782564385.383,"0"]}]}}`

const matrixJSON = `{"status":"success","data":{"resultType":"matrix","result":[
 {"metric":{},"values":[[1782564025,"33"],[1782564145,"34.5"],[1782564265,"33"]]}]}}`

func TestParseVector(t *testing.T) {
	got, err := ParseVector([]byte(vectorJSON))
	if err != nil {
		t.Fatalf("ParseVector: %v", err)
	}
	want := []Sample{
		{Labels: map[string]string{"__name__": "up", "job": "keycloak-http", "namespace": "keycloak"}, Value: 1},
		{Labels: map[string]string{"__name__": "up", "job": "falco"}, Value: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseMatrix(t *testing.T) {
	got, err := ParseMatrix([]byte(matrixJSON))
	if err != nil {
		t.Fatalf("ParseMatrix: %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Values, []float64{33, 34.5, 33}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseVector_Error(t *testing.T) {
	if _, err := ParseVector([]byte(`{"status":"error","errorType":"bad"}`)); err == nil {
		t.Fatal("expected an error for a non-success status")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -v`
Expected: FAIL — `undefined: ParseVector` (package does not compile).

- [ ] **Step 3: Write the parser** — create `data/prom.go`:

```go
// Package data is the monitor's cluster data layer: Prometheus (this file) and
// kubectl resource fetch, behind fake-backed exec-wrappers so the parsing/query
// logic is unit-tested with no cluster.
package data

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Sample is one instant (vector) result: its labels and parsed float value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Series is one range (matrix) result: its labels and the parsed value series.
type Series struct {
	Labels map[string]string
	Values []float64
}

// promResp is the Prometheus HTTP API envelope (vector or matrix).
type promResp struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`  // [ts, "val"] — vector
			Values [][]any           `json:"values"` // [[ts,"val"],…] — matrix
		} `json:"result"`
	} `json:"data"`
}

// decode parses the envelope and fails on a non-success status.
func decode(raw []byte) (*promResp, error) {
	var r promResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("prom: parse json: %w", err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prom: query status %q", r.Status)
	}
	return &r, nil
}

// sampleValue parses the "val" string from a [ts, "val"] pair.
func sampleValue(pair []any) (float64, error) {
	if len(pair) != 2 {
		return 0, fmt.Errorf("prom: malformed value pair %v", pair)
	}
	s, ok := pair[1].(string)
	if !ok {
		return 0, fmt.Errorf("prom: value is not a string: %v", pair[1])
	}
	return strconv.ParseFloat(s, 64)
}

// ParseVector parses an instant-query (vector) response into samples.
func ParseVector(raw []byte) ([]Sample, error) {
	r, err := decode(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(r.Data.Result))
	for _, item := range r.Data.Result {
		v, err := sampleValue(item.Value)
		if err != nil {
			return nil, err
		}
		out = append(out, Sample{Labels: item.Metric, Value: v})
	}
	return out, nil
}

// ParseMatrix parses a range-query (matrix) response into series.
func ParseMatrix(raw []byte) ([]Series, error) {
	r, err := decode(raw)
	if err != nil {
		return nil, err
	}
	out := make([]Series, 0, len(r.Data.Result))
	for _, item := range r.Data.Result {
		vals := make([]float64, 0, len(item.Values))
		for _, pair := range item.Values {
			v, err := sampleValue(pair)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
		out = append(out, Series{Labels: item.Metric, Values: vals})
	}
	return out, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/prom.go installer/internal/tui/monitor/data/prom_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Prometheus vector/matrix response parser"
```

---

## Task 2: Prometheus service discovery

**Files:**
- Modify: `installer/internal/tui/monitor/data/prom.go`, `data/prom_test.go`

**Interfaces:**
- Produces (consumed by Task 3): `func DiscoverPromRef(svcListJSON []byte) (string, error)` — returns `"<ns>/<name>:<port>"` for the Prometheus service, or an error. Picks the `monitoring`-namespace service whose name contains `prometheus` but not `alertmanager`/`operator`/`node-exporter`/`kube-state`, port 9090.

- [ ] **Step 1: Write the failing test** — append to `data/prom_test.go`:

```go
const svcListJSON = `{"items":[
 {"metadata":{"name":"kube-prometheus-stack-alertmanager","namespace":"monitoring"},"spec":{"ports":[{"port":9093}]}},
 {"metadata":{"name":"kube-prometheus-stack-prometheus","namespace":"monitoring"},"spec":{"ports":[{"port":9090},{"port":8080}]}},
 {"metadata":{"name":"kube-prometheus-stack-operator","namespace":"monitoring"},"spec":{"ports":[{"port":443}]}}]}`

func TestDiscoverPromRef(t *testing.T) {
	got, err := DiscoverPromRef([]byte(svcListJSON))
	if err != nil || got != "monitoring/kube-prometheus-stack-prometheus:9090" {
		t.Fatalf("DiscoverPromRef = %q, %v", got, err)
	}
}

func TestDiscoverPromRef_NotFound(t *testing.T) {
	if _, err := DiscoverPromRef([]byte(`{"items":[]}`)); err == nil {
		t.Fatal("expected error when no prometheus service exists")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run TestDiscoverPromRef -v`
Expected: FAIL — `undefined: DiscoverPromRef`.

- [ ] **Step 3: Implement** — append to `data/prom.go`:

```go
import "strings" // add to the existing import block

// DiscoverPromRef finds the Prometheus service in a `kubectl get svc -n monitoring
// -o json` payload and returns "<ns>/<name>:9090". It skips the alertmanager,
// operator, node-exporter, and kube-state-metrics services.
func DiscoverPromRef(svcListJSON []byte) (string, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(svcListJSON, &list); err != nil {
		return "", fmt.Errorf("prom: parse svc list: %w", err)
	}
	skip := []string{"alertmanager", "operator", "node-exporter", "kube-state"}
	for _, it := range list.Items {
		name := it.Metadata.Name
		if !strings.Contains(name, "prometheus") {
			continue
		}
		skipped := false
		for _, s := range skip {
			if strings.Contains(name, s) {
				skipped = true
				break
			}
		}
		if skipped {
			continue
		}
		for _, p := range it.Spec.Ports {
			if p.Port == 9090 {
				return fmt.Sprintf("%s/%s:9090", it.Metadata.Namespace, name), nil
			}
		}
	}
	return "", fmt.Errorf("prom: no prometheus service (port 9090) found in svc list")
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/prom.go installer/internal/tui/monitor/data/prom_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): discover the Prometheus service from the svc list"
```

---

## Task 3: Prometheus client (Raw wrapper + Query/QueryRange + query catalog)

**Files:**
- Modify: `installer/internal/tui/monitor/data/prom.go`, `data/prom_test.go`

**Interfaces:**
- Consumes: `ParseVector`/`ParseMatrix` (Task 1), the discovered ref (Task 2).
- Produces (consumed by Task 7): `type Raw interface { Get(path string) ([]byte, error) }`; `func NewRaw() Raw`; `type Prom struct { Raw Raw; Ref string }`; `func (p Prom) Query(promql string) ([]Sample, error)`; `func (p Prom) QueryRange(promql string, start, end, step int64) ([]Series, error)`; the catalog constants `QNodeCPUPct`, `QNodeMemPct`, `QFiringAlerts`, `QNodeCPUSeries`, `QNodeMemSeries`.

- [ ] **Step 1: Write the failing test** — append to `data/prom_test.go`:

```go
// fakeRaw records the requested path and returns canned bytes.
type fakeRaw struct {
	lastPath string
	out      []byte
	err      error
}

func (f *fakeRaw) Get(path string) ([]byte, error) { f.lastPath = path; return f.out, f.err }

func TestProm_Query_BuildsProxyPathAndParses(t *testing.T) {
	fr := &fakeRaw{out: []byte(vectorJSON)}
	p := Prom{Raw: fr, Ref: "monitoring/kube-prometheus-stack-prometheus:9090"}
	got, err := p.Query("up")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 samples, got %d", len(got))
	}
	wantPath := "/api/v1/namespaces/monitoring/services/kube-prometheus-stack-prometheus:9090/proxy/api/v1/query?query=up"
	if fr.lastPath != wantPath {
		t.Fatalf("path = %q, want %q", fr.lastPath, wantPath)
	}
}

func TestProm_QueryRange_EncodesAndParses(t *testing.T) {
	fr := &fakeRaw{out: []byte(matrixJSON)}
	p := Prom{Raw: fr, Ref: "monitoring/kube-prometheus-stack-prometheus:9090"}
	got, err := p.QueryRange("count(up)", 1782564025, 1782564265, 120)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(got) != 1 || len(got[0].Values) != 3 {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(fr.lastPath, "/query_range?query=count%28up%29&start=1782564025&end=1782564265&step=120") {
		t.Fatalf("range path = %q", fr.lastPath)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run TestProm -v`
Expected: FAIL — `undefined: Prom`.

- [ ] **Step 3: Implement** — append to `data/prom.go` (add `net/url`, `os/exec` to imports):

```go
// Named PromQL the dashboard uses (kept as constants so each is reviewable).
const (
	QNodeCPUPct    = `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
	QNodeMemPct    = `100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))`
	QFiringAlerts  = `ALERTS{alertstate="firing"}`
	QNodeCPUSeries = `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
	QNodeMemSeries = `100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))`
)

// Raw runs `kubectl get --raw <path>` and returns the body. Tests inject a fake.
type Raw interface {
	Get(path string) ([]byte, error)
}

// commandContext is the command builder (swappable in tests).
var commandContext = exec.Command

type execRaw struct{}

// NewRaw returns the production Raw wrapper.
func NewRaw() Raw { return execRaw{} }

// Get runs `kubectl get --raw <path>`.
func (execRaw) Get(path string) ([]byte, error) {
	out, err := commandContext("kubectl", "get", "--raw", path).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get --raw %s: %w", path, err)
	}
	return out, nil
}

// Prom queries Prometheus through the kube-API proxy at Ref ("<ns>/<name>:<port>").
type Prom struct {
	Raw Raw
	Ref string
}

// proxyBase is the kube-API proxy prefix for the Prometheus HTTP API.
func (p Prom) proxyBase() string {
	parts := strings.SplitN(p.Ref, "/", 2) // ns / name:port
	return fmt.Sprintf("/api/v1/namespaces/%s/services/%s/proxy/api/v1", parts[0], parts[1])
}

// Query runs an instant PromQL query and returns the vector samples.
func (p Prom) Query(promql string) ([]Sample, error) {
	path := p.proxyBase() + "/query?query=" + url.QueryEscape(promql)
	raw, err := p.Raw.Get(path)
	if err != nil {
		return nil, err
	}
	return ParseVector(raw)
}

// QueryRange runs a range PromQL query (for sparklines) and returns the series.
func (p Prom) QueryRange(promql string, start, end, step int64) ([]Series, error) {
	path := fmt.Sprintf("%s/query_range?query=%s&start=%d&end=%d&step=%d",
		p.proxyBase(), url.QueryEscape(promql), start, end, step)
	raw, err := p.Raw.Get(path)
	if err != nil {
		return nil, err
	}
	return ParseMatrix(raw)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS (7 tests). `url.QueryEscape("up")` → `up`; `url.QueryEscape("count(up)")` → `count%28up%29`.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/prom.go installer/internal/tui/monitor/data/prom_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): Prometheus client (kube-proxy Query/QueryRange + catalog)"
```

---

## Task 4: Gauge widget

**Files:**
- Create: `installer/internal/tui/monitor/widgets/gauge.go`, `widgets/gauge_test.go`

**Interfaces:**
- Produces (consumed by Task 7): `func Bar(pct float64, width int) string` — a `width`-cell bar of `█`/`░` with a tview colour tag by threshold (green <70, amber 70–89, red ≥90) and a trailing ` NN%`. Clamps pct to 0–100.

- [ ] **Step 1: Write the failing test** — `widgets/gauge_test.go`:

```go
package widgets

import (
	"strings"
	"testing"
)

func TestBar_FillAndColour(t *testing.T) {
	got := Bar(80, 10) // 80% of 10 = 8 filled
	if !strings.Contains(got, strings.Repeat("█", 8)) || !strings.Contains(got, strings.Repeat("░", 2)) {
		t.Fatalf("fill wrong: %q", got)
	}
	if !strings.Contains(got, "80%") {
		t.Fatalf("missing label: %q", got)
	}
	if !strings.Contains(got, "#d29922") { // amber for 70–89
		t.Fatalf("expected amber tag for 80%%: %q", got)
	}
}

func TestBar_Thresholds(t *testing.T) {
	if !strings.Contains(Bar(50, 10), "#3fb950") { // green
		t.Error("50% should be green")
	}
	if !strings.Contains(Bar(95, 10), "#f85149") { // red
		t.Error("95% should be red")
	}
}

func TestBar_Clamps(t *testing.T) {
	if strings.Count(Bar(250, 10), "█") != 10 {
		t.Error("over-100 should clamp to full")
	}
	if strings.Count(Bar(-5, 10), "█") != 0 {
		t.Error("negative should clamp to empty")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/widgets/ -v`
Expected: FAIL — `undefined: Bar`.

- [ ] **Step 3: Implement** — `widgets/gauge.go`:

```go
// Package widgets holds the monitor's reusable terminal visual primitives — pure
// string-builders that turn metric values into tview-markup graphics. The
// builders are unit-tested; the tview rendering that displays them is smoke.
package widgets

import (
	"fmt"
	"strings"
)

// Bar renders pct (0–100) as a width-cell bar with a threshold colour and a
// trailing percent label, e.g. "[#d29922]████████░░[-] 80%". Out-of-range
// values clamp to [0,100].
func Bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	colour := "#3fb950" // green
	switch {
	case pct >= 90:
		colour = "#f85149" // red
	case pct >= 70:
		colour = "#d29922" // amber
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("[%s]%s[-] %d%%", colour, bar, int(pct+0.5))
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/widgets/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/widgets/gauge.go installer/internal/tui/monitor/widgets/gauge_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): bar-gauge widget"
```

---

## Task 5: Sparkline widget

**Files:**
- Create: `installer/internal/tui/monitor/widgets/sparkline.go`, `widgets/sparkline_test.go`

**Interfaces:**
- Produces (consumed by Task 7): `func Spark(vals []float64) string` — maps each value to one of `▁▂▃▄▅▆▇█` scaled between the series min and max. Empty input → `""`. A flat series → all the lowest tick.

- [ ] **Step 1: Write the failing test** — `widgets/sparkline_test.go`:

```go
package widgets

import "testing"

func TestSpark_ScalesMinToMax(t *testing.T) {
	got := Spark([]float64{0, 50, 100})
	want := "▁▄█" // min→lowest, max→highest
	if got != want {
		t.Fatalf("Spark = %q, want %q", got, want)
	}
}

func TestSpark_Flat(t *testing.T) {
	if got := Spark([]float64{5, 5, 5}); got != "▁▁▁" {
		t.Fatalf("flat series should be all-low, got %q", got)
	}
}

func TestSpark_Empty(t *testing.T) {
	if Spark(nil) != "" {
		t.Fatal("empty input should produce empty string")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/widgets/ -run TestSpark -v`
Expected: FAIL — `undefined: Spark`.

- [ ] **Step 3: Implement** — `widgets/sparkline.go`:

```go
package widgets

// sparkTicks are the eight ramp glyphs, low to high.
var sparkTicks = []rune("▁▂▃▄▅▆▇█")

// Spark renders vals as a sparkline scaled between the series min and max. A
// flat or empty series renders at the lowest tick (or "" when empty).
func Spark(vals []float64) string {
	if len(vals) == 0 {
		return ""
	}
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	span := max - min
	out := make([]rune, len(vals))
	for i, v := range vals {
		idx := 0
		if span > 0 {
			idx = int((v - min) / span * float64(len(sparkTicks)-1))
		}
		out[i] = sparkTicks[idx]
	}
	return string(out)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/widgets/ -run TestSpark -v`
Expected: PASS. (0→idx0 `▁`; 50→(50/100*7)=3.5→3 `▄`; 100→7 `█`.)

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/widgets/sparkline.go installer/internal/tui/monitor/widgets/sparkline_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): sparkline widget"
```

---

## Task 6: Stat-tile + health widgets

**Files:**
- Create: `installer/internal/tui/monitor/widgets/stattile.go`, `widgets/stattile_test.go`

**Interfaces:**
- Produces (consumed by Task 7): `func Tile(n int, label string) string` → `"[#FFFFFF::b]<n>[-:-:-] [#7C8694]<label>[-]"`; `func Health(ok, warn, fail int) string` → `"[#3fb950]✓ <ok>[-]  [#d29922]⚠ <warn>[-]  [#f85149]✗ <fail>[-]"`.

- [ ] **Step 1: Write the failing test** — `widgets/stattile_test.go`:

```go
package widgets

import (
	"strings"
	"testing"
)

func TestTile(t *testing.T) {
	got := Tile(56, "pods")
	if !strings.Contains(got, "56") || !strings.Contains(got, "pods") || !strings.Contains(got, "::b]") {
		t.Fatalf("Tile = %q", got)
	}
}

func TestHealth(t *testing.T) {
	got := Health(5, 1, 0)
	for _, frag := range []string{"✓ 5", "⚠ 1", "✗ 0", "#3fb950", "#f85149"} {
		if !strings.Contains(got, frag) {
			t.Fatalf("Health missing %q: %q", frag, got)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/widgets/ -run 'TestTile|TestHealth' -v`
Expected: FAIL — `undefined: Tile`.

- [ ] **Step 3: Implement** — `widgets/stattile.go`:

```go
package widgets

import "fmt"

// Tile renders a big-number stat with a dim label, e.g. "56 pods".
func Tile(n int, label string) string {
	return fmt.Sprintf("[#FFFFFF::b]%d[-:-:-] [#7C8694]%s[-]", n, label)
}

// Health renders a coloured ✓/⚠/✗ rollup.
func Health(ok, warn, fail int) string {
	return fmt.Sprintf("[#3fb950]✓ %d[-]  [#d29922]⚠ %d[-]  [#f85149]✗ %d[-]", ok, warn, fail)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/widgets/ -v`
Expected: PASS (all widget tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/widgets/stattile.go installer/internal/tui/monitor/widgets/stattile_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): stat-tile + health-rollup widgets"
```

---

## Task 7: OVERVIEW panel-builder

**Files:**
- Create: `installer/internal/tui/monitor/views/overview.go`, `views/overview_test.go`

**Interfaces:**
- Consumes: `widgets.Bar/Spark/Tile/Health` (Tasks 4–6).
- Produces (consumed by Task 8): `type Inputs struct { Nodes, Pods, Namespaces, Packages, FiringAlerts int; CPUPct, MemPct float64; CPUSeries, MemSeries []float64; LayerHealth [3]int; AlertNames []string; MetricsOK bool }`; `func BuildOverview(in Inputs) string` — the full dashboard as one tview-markup string.

- [ ] **Step 1: Write the failing test** — `views/overview_test.go`:

```go
package views

import (
	"strings"
	"testing"
)

func TestBuildOverview_RendersPanels(t *testing.T) {
	out := BuildOverview(Inputs{
		Nodes: 1, Pods: 56, Namespaces: 22, Packages: 6, FiringAlerts: 1,
		CPUPct: 3, MemPct: 12, CPUSeries: []float64{3, 4, 3}, MemSeries: []float64{12, 12, 13},
		LayerHealth: [3]int{6, 0, 0}, AlertNames: []string{"Watchdog"}, MetricsOK: true,
	})
	for _, frag := range []string{"56", "pods", "CPU", "MEM", "3%", "12%", "✓ 6", "Watchdog"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("overview missing %q\n---\n%s", frag, out)
		}
	}
}

func TestBuildOverview_MetricsDegraded(t *testing.T) {
	out := BuildOverview(Inputs{Nodes: 1, Pods: 56, MetricsOK: false})
	if !strings.Contains(out, "metrics unavailable") {
		t.Fatalf("degraded overview should note unavailable metrics:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/views/ -v`
Expected: FAIL — `undefined: BuildOverview`.

- [ ] **Step 3: Implement** — `views/overview.go`:

```go
// Package views holds the monitor's per-screen panel/row builders — pure
// functions (cluster data → renderable text/rows) that are unit-tested; the
// tview rendering that displays them is smoke.
package views

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor/widgets"
)

// Inputs is everything the OVERVIEW dashboard needs, already fetched + degraded
// (MetricsOK=false ⇒ Prometheus was unreachable; gauges/sparklines are omitted).
type Inputs struct {
	Nodes, Pods, Namespaces, Packages, FiringAlerts int
	CPUPct, MemPct                                   float64
	CPUSeries, MemSeries                             []float64
	LayerHealth                                      [3]int // ok, warn, fail
	AlertNames                                       []string
	MetricsOK                                        bool
}

// BuildOverview renders the cross-layer dashboard as one tview-markup string.
func BuildOverview(in Inputs) string {
	var b strings.Builder
	// Stat tiles row.
	fmt.Fprintf(&b, "  %s    %s    %s    %s    %s\n\n",
		widgets.Tile(in.Nodes, "nodes"), widgets.Tile(in.Pods, "pods"),
		widgets.Tile(in.Namespaces, "namespaces"), widgets.Tile(in.Packages, "packages"),
		widgets.Tile(in.FiringAlerts, "alerts"))

	// Cluster CPU/MEM gauges + sparklines (or a degraded note).
	b.WriteString("  [#9FB4D8::b]Cluster[-:-:-]\n")
	if in.MetricsOK {
		fmt.Fprintf(&b, "    CPU  %s   %s\n", widgets.Bar(in.CPUPct, 24), widgets.Spark(in.CPUSeries))
		fmt.Fprintf(&b, "    MEM  %s   %s\n", widgets.Bar(in.MemPct, 24), widgets.Spark(in.MemSeries))
	} else {
		b.WriteString("    [#7C8694]metrics unavailable (Prometheus unreachable)[-]\n")
	}

	// Per-layer health rollup.
	fmt.Fprintf(&b, "\n  [#9FB4D8::b]Health[-:-:-]   %s\n",
		widgets.Health(in.LayerHealth[0], in.LayerHealth[1], in.LayerHealth[2]))

	// Firing alerts.
	b.WriteString("\n  [#9FB4D8::b]Alerts[-:-:-]\n")
	if len(in.AlertNames) == 0 {
		b.WriteString("    [#3fb950]none firing[-]\n")
	} else {
		for _, a := range in.AlertNames {
			fmt.Fprintf(&b, "    [#f85149]●[-] %s\n", a)
		}
	}
	return b.String()
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/views/ -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/views/overview.go installer/internal/tui/monitor/views/overview_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): OVERVIEW dashboard panel-builder"
```

---

## Task 8: Wire OVERVIEW into the monitor (default view + metrics fetch + degrade)

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `views.BuildOverview`/`views.Inputs`; `data.Prom`, `data.NewRaw`, `data.DiscoverPromRef`, the catalog constants; the existing `monitor` struct + `state` (packages/apps).

- [ ] **Step 1: Restructure the main area to a Pages (overview ⊕ table)**

In `monitor.go`'s `Run`, replace the single-`table` main item with a `tview.Pages` holding an `overview` `TextView` and the existing `table`. Add the prom client + an `overviewTV` + `promRef` to the `monitor` struct. The dark canvas/colours are unchanged. Concretely:

```go
// in Run(), after building `table`:
overviewTV := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
overviewTV.SetTextColor(consoleText).SetBackgroundColor(consoleBg)

main := tview.NewPages().
	AddPage("overview", overviewTV, true, true).
	AddPage("table", table, true, false)

// discover Prometheus best-effort (degrade-safe)
prom := data.Prom{Raw: data.NewRaw()}
if svcs, err := data.NewRaw().Get("/api/v1/namespaces/monitoring/services?limit=500"); err == nil {
	if ref, derr := data.DiscoverPromRef(svcs); derr == nil {
		prom.Ref = ref
	}
}

layout := tview.NewFlex().SetDirection(tview.FlexRow).
	AddItem(header, 2, 0, false).
	AddItem(main, 0, 1, true).
	AddItem(footer, 1, 0, false)
layout.SetBackgroundColor(consoleBg)
```

Add to the `monitor` struct: `main *tview.Pages`, `overviewTV *tview.TextView`, `prom data.Prom`. Set `view: viewOverview` as the initial view and store `main/overviewTV/prom` on `m`. Add `viewOverview` to the `viewKind` const block as the first value (so it is the default):

```go
const (
	viewOverview viewKind = iota
	viewPackages
	viewApps
)
```

- [ ] **Step 2: Add the overview paint + metrics fetch (degrade-safe)**

Add to `monitor.go`:

```go
// paintOverview fetches the cross-layer signals and renders the dashboard. Any
// metrics failure degrades to MetricsOK=false rather than erroring.
func (m *monitor) paintOverview() {
	in := views.Inputs{MetricsOK: true}

	// Counts from kubectl (reuse the existing wrappers; best-effort).
	if raw, err := m.state.Kube.ListPackages(); err == nil {
		if rows, perr := buildPackageRows(raw); perr == nil {
			in.Packages = len(rows)
			ok := 0
			for _, r := range rows {
				if r.Phase == "Ready" {
					ok++
				}
			}
			in.LayerHealth = [3]int{ok, 0, len(rows) - ok}
		}
	}
	in.Nodes, in.Pods, in.Namespaces = m.counts() // helper below

	// Metrics from Prometheus (degrade on any failure).
	if m.prom.Ref == "" {
		in.MetricsOK = false
	} else {
		cpu, e1 := m.prom.Query(data.QNodeCPUPct)
		mem, e2 := m.prom.Query(data.QNodeMemPct)
		alerts, e3 := m.prom.Query(data.QFiringAlerts)
		if e1 != nil || e2 != nil || e3 != nil {
			in.MetricsOK = false
		} else {
			in.CPUPct = firstValue(cpu)
			in.MemPct = firstValue(mem)
			for _, a := range alerts {
				name := a.Labels["alertname"]
				if a.Labels["alertstate"] == "firing" && name != "" {
					in.AlertNames = append(in.AlertNames, name)
				}
			}
			in.FiringAlerts = len(in.AlertNames)
		}
	}
	m.overviewTV.SetText(views.BuildOverview(in))
}

// firstValue returns the value of the first sample, or 0.
func firstValue(s []data.Sample) float64 {
	if len(s) == 0 {
		return 0
	}
	return s[0].Value
}

// counts returns node/pod/namespace counts via kubectl (best-effort, 0 on error).
func (m *monitor) counts() (nodes, pods, namespaces int) {
	count := func(args ...string) int {
		out, err := commandContext("kubectl", args...).Output()
		if err != nil {
			return 0
		}
		n := 0
		for _, line := range splitNonEmpty(string(out)) {
			_ = line
			n++
		}
		return n
	}
	nodes = count("get", "nodes", "--no-headers")
	pods = count("get", "pods", "-A", "--no-headers")
	namespaces = count("get", "ns", "--no-headers")
	return
}
```

Add the small helpers `splitNonEmpty` (strings.Split on "\n", drop empties) and the `commandContext = exec.Command` var if not already present in this file. (`paintOverview` is wired into `refresh()` for `viewOverview`, and `setView`/footer/keys updated to include overview as `0`/`o`, default.) Sparklines (range queries) are added in Phase 1.2; P1.1 ships the instant gauges + counts + alerts.

- [ ] **Step 3: Update refresh/nav/footer for the overview view**

In `refresh()` add the `viewOverview` case → `m.main.SwitchToPage("overview"); m.paintOverview()`; the `viewPackages`/`viewApps` cases call `m.main.SwitchToPage("table")` then the existing paint. In `SetInputCapture`, add `case '0', 'o': m.setView(viewOverview)`. Update `footerText()` to lead with `[#FFFFFF::b]0[-:-:-] [#7C8694]overview[-]`. Update the `Tab` toggle to cycle overview→packages→apps.

- [ ] **Step 4: Build + full test suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && go test ./... -count=1 2>&1 | tail -8`
Expected: build clean; full suite green (the new data/widgets/views tests + the existing monitor row-builder + wizard Flow tests). If a tview Pages/TextView API mismatch surfaces, adapt against the installed tview (rendering-layer only).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): OVERVIEW dashboard as the default view (metrics + degrade)"
```

---

## Task 9: Lab smoke (manual)

- [ ] **Step 1: Cross-compile + deliver to the bastion (has cluster + Prometheus)**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
```

- [ ] **Step 2: Drive it in tmux on the bastion + capture the OVERVIEW**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-session -t mon 2>/dev/null || true
tmux new-session -d -s mon -x 120 -y 34; sleep 0.4
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 2.5
tmux capture-pane -t mon -p | sed -n '1,20p'
tmux send-keys -t mon q
EOF
```
Expected (manual): the OVERVIEW dashboard — stat tiles (1 nodes · 56 pods · 22 namespaces · 6 packages · N alerts), a Cluster CPU gauge (~3%) + MEM gauge (~12%), a Health rollup (✓ 6), and the firing alerts (Watchdog). `0`/`1`/`2` switch overview/packages/apps. Confirms Prometheus reads through the kube-proxy on the live cluster.

- [ ] **Step 3: PING the user** to drive it interactively (`ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor`) and confirm the dashboard reads well before the Phase-1.2 plan (host/cluster/core/apps views + command bar + drill-in).

---

## Self-Review

**1. Spec coverage (this is Phase 1.1 of the spec):** metrics layer §8 → Tasks 1–3 (parser, discovery, client, catalog); widgets §5 (gauge/sparkline/stat-tile/health) → Tasks 4–6; OVERVIEW §4 → Task 7; wired into the dark console §3.1/§3.4 → Task 8; graceful degradation §3.2 → Task 8 (`MetricsOK`). Deferred to Phase 1.2 (noted): `data/kube.go` broad fetch, the host/cluster/core/apps resource views, the `:` command bar, drill-in, sparkline range-queries on the overview.

**2. Placeholder scan:** every code step ships complete code; every run step has the exact command + expected output. No TODO/"handle errors"/"similar to". Task 8's tview wiring is described as concrete edits with code; the build step catches any tview API drift (rendering-only).

**3. Type consistency:** `data.Sample{Labels,Value}` / `data.Series{Labels,Values}` (Task 1) used by `Prom.Query/QueryRange` (Task 3) and `paintOverview` (Task 8). `widgets.Bar/Spark/Tile/Health` (Tasks 4–6) signatures match their uses in `views.BuildOverview` (Task 7). `views.Inputs` fields (Task 7) match what Task 8 populates. `viewOverview` added as the first `viewKind` so it is the default. The `commandContext`/`exec.Command` var name matches the appcatalog exec-wrapper convention.
