# srectl Day-2 console — Phase 1.2 (resource browser) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the monitor into a k9s-style cluster browser — add live **nodes / pods / workloads / services** resource views and a **`:` command bar** to jump between every view, on top of P1.1's async-fetch architecture.

**Architecture:** A new `data/kube.go` wraps `kubectl get <args> -o json` (timeout-bounded, fake-backed) with pure row-builders (one per resource). The monitor's view system generalizes from a 3-value enum to a **string-keyed registry of table views** (each = title + columns + an off-UI fetch closure); the overview stays a special dashboard. A `:` command bar (tview InputField overlay) switches to any registered view by name. Every fetch runs off the UI goroutine (the P1.1 rule).

**Tech Stack:** Go 1.25, tview/tcell, kubectl `-o json`, the P1.1 `data`/`widgets`/`views` packages.

## Global Constraints

- **Module / cwd:** `github.com/JongoDB-Labs/sre-v2/installer`, go 1.25. All `go`/`git` from `/Users/JonWFH/jondev/sre-v2-wt-mon/installer` (branch `feat/srectl-monitor-ux`, which has P1.1). Do NOT switch branches.
- **Exec-wrapper rule (binding):** orchestrate `kubectl` via a fake-backed interface; never embed a Kubernetes client. Row-builders are pure + unit-tested with fixtures.
- **Never block the UI goroutine (binding — this is the P1.1 freeze fix):** all cluster I/O runs in a background goroutine; only the draw (`SetCell`/`SetText`) is marshalled through `QueueUpdateDraw`. Resource fetches use the same `refresh()`→fetch-off-UI→draw pattern P1.1 established. Bound every kubectl call with a timeout (`exec.CommandContext`, 4s — match `kubectlTimeout`).
- **Read-only:** Phase 1.2 adds no mutations.
- **Look/branding:** the shipped dark console (`consoleBg`/`consoleText`/`consoleDim`/`status*`, the `tui` accent/selection); title `SRE Monitor — <version>`; never "Security Onion".
- **Testing:** every row-builder is pure + unit-tested with fixtures (real lab JSON shapes); tview rendering + the command bar + live refresh = manual smoke on the lab.
- **Commits:** noreply email. `git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "…"`.

---

## File Structure

**Create:**
- `installer/internal/tui/monitor/data/kube.go` (package `data`) — `Resources` exec-wrapper (`Get(args…) → kubectl get … -o json`, timeout-bounded) + the row types (`NodeRow`/`PodRow`/`WorkloadRow`/`ServiceRow`) + parsers (`NodeRows`/`PodRows`/`WorkloadRows`/`ServiceRows`).
- `installer/internal/tui/monitor/data/kube_test.go`

**Modify:**
- `installer/internal/tui/monitor/monitor.go` — generalize the view system to a string-keyed `tableView` registry (Task 5); register nodes/pods/workloads/services (Task 6); add the `:` command bar (Task 7).

> Deferred to a later P1.2 slice (noted, not in this plan): the **events** view (its JSON is finicky — `lastTimestamp` can be null), the **host/OS** node-exporter panel, **drill-in** (describe/YAML/logs), and overview **sparklines** (range-queries).

---

## Task 1: data/kube.go — Resources wrapper + node rows

**Files:**
- Create: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces (consumed by Tasks 2–6): `type Resources interface { Get(args ...string) ([]byte, error) }`; `func NewResources() Resources`; `type NodeRow struct { Name, Roles, Status, Version string }`; `func NodeRows(raw []byte) ([]NodeRow, error)`.

- [ ] **Step 1: Write the failing test** — `data/kube_test.go` (fixture = the real lab node shape):

```go
package data

import (
	"reflect"
	"testing"
)

const nodesJSON = `{"items":[
 {"metadata":{"name":"cosmos-k8s","labels":{"node-role.kubernetes.io/control-plane":"true","node-role.kubernetes.io/etcd":"true","kubernetes.io/hostname":"cosmos-k8s"}},
  "status":{"conditions":[{"type":"MemoryPressure","status":"False"},{"type":"Ready","status":"True"}],"nodeInfo":{"kubeletVersion":"v1.35.5+rke2r2"}}}]}`

func TestNodeRows(t *testing.T) {
	got, err := NodeRows([]byte(nodesJSON))
	if err != nil {
		t.Fatalf("NodeRows: %v", err)
	}
	want := []NodeRow{{Name: "cosmos-k8s", Roles: "control-plane,etcd", Status: "Ready", Version: "v1.35.5+rke2r2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestNodeRows_NotReady(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"n2","labels":{}},"status":{"conditions":[{"type":"Ready","status":"False"}],"nodeInfo":{"kubeletVersion":"v1"}}}]}`
	got, _ := NodeRows([]byte(raw))
	if len(got) != 1 || got[0].Status != "NotReady" || got[0].Roles != "" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go test ./internal/tui/monitor/data/ -run TestNodeRows -v`
Expected: FAIL — `undefined: NodeRows`.

- [ ] **Step 3: Implement** — create `data/kube.go`:

```go
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// resourcesTimeout bounds each `kubectl get` so a stalled API call cannot hang
// the (background) fetch indefinitely.
const resourcesTimeout = 4 * time.Second

// Resources runs `kubectl get <args...> -o json`. Tests inject a fake.
type Resources interface {
	Get(args ...string) ([]byte, error)
}

type execResources struct{}

// NewResources returns the production Resources wrapper.
func NewResources() Resources { return execResources{} }

// Get runs `kubectl get <args...> -o json`, bounded by resourcesTimeout.
func (execResources) Get(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), resourcesTimeout)
	defer cancel()
	full := append([]string{"get"}, args...)
	full = append(full, "-o", "json")
	out, err := exec.CommandContext(ctx, "kubectl", full...).Output()
	if err != nil {
		return nil, fmt.Errorf("kubectl get %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// NodeRow is one row of the nodes view.
type NodeRow struct {
	Name, Roles, Status, Version string
}

// NodeRows parses `kubectl get nodes -o json`.
func NodeRows(raw []byte) ([]NodeRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse nodes json: %w", err)
	}
	rows := make([]NodeRow, 0, len(list.Items))
	for _, it := range list.Items {
		var roles []string
		for k := range it.Metadata.Labels {
			if r, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && r != "" {
				roles = append(roles, r)
			}
		}
		sort.Strings(roles)
		status := "NotReady"
		for _, c := range it.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				status = "Ready"
			}
		}
		rows = append(rows, NodeRow{
			Name: it.Metadata.Name, Roles: strings.Join(roles, ","),
			Status: status, Version: it.Status.NodeInfo.KubeletVersion,
		})
	}
	return rows, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -run TestNodeRows -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): kube Resources wrapper + node row-builder"
```

---

## Task 2: Pod rows

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `type PodRow struct { Namespace, Name, Ready, Status string; Restarts int; Node string }`; `func PodRows(raw []byte) ([]PodRow, error)`. `Ready` is `"<readyContainers>/<totalContainers>"`; `Restarts` is the summed container restart count.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
const podsJSON = `{"items":[
 {"metadata":{"namespace":"authservice","name":"authservice-74b485b56-g2xdw"},"spec":{"nodeName":"cosmos-k8s"},
  "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":0}]}},
 {"metadata":{"namespace":"cosmos","name":"cosmos-pg-0"},"spec":{"nodeName":"cosmos-k8s"},
  "status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":2},{"ready":false,"restartCount":1}]}}]}`

func TestPodRows(t *testing.T) {
	got, err := PodRows([]byte(podsJSON))
	if err != nil {
		t.Fatalf("PodRows: %v", err)
	}
	want := []PodRow{
		{Namespace: "authservice", Name: "authservice-74b485b56-g2xdw", Ready: "1/1", Status: "Running", Restarts: 0, Node: "cosmos-k8s"},
		{Namespace: "cosmos", Name: "cosmos-pg-0", Ready: "1/2", Status: "Running", Restarts: 3, Node: "cosmos-k8s"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run TestPodRows -v`
Expected: FAIL — `undefined: PodRows`.

- [ ] **Step 3: Implement** — append to `data/kube.go`:

```go
// PodRow is one row of the pods view.
type PodRow struct {
	Namespace, Name, Ready, Status string
	Restarts                       int
	Node                           string
}

// PodRows parses `kubectl get pods -A -o json`.
func PodRows(raw []byte) ([]PodRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName string `json:"nodeName"`
			} `json:"spec"`
			Status struct {
				Phase            string `json:"phase"`
				ContainerStatuses []struct {
					Ready        bool `json:"ready"`
					RestartCount int  `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse pods json: %w", err)
	}
	rows := make([]PodRow, 0, len(list.Items))
	for _, it := range list.Items {
		ready, restarts := 0, 0
		for _, cs := range it.Status.ContainerStatuses {
			if cs.Ready {
				ready++
			}
			restarts += cs.RestartCount
		}
		rows = append(rows, PodRow{
			Namespace: it.Metadata.Namespace, Name: it.Metadata.Name,
			Ready:    fmt.Sprintf("%d/%d", ready, len(it.Status.ContainerStatuses)),
			Status:   it.Status.Phase, Restarts: restarts, Node: it.Spec.NodeName,
		})
	}
	return rows, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -run TestPodRows -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): pod row-builder"
```

---

## Task 3: Workload rows (deploy / statefulset / daemonset)

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `type WorkloadRow struct { Namespace, Kind, Name, Ready string }`; `func WorkloadRows(raw []byte, kind string) ([]WorkloadRow, error)`. `Ready` is `"<ready>/<desired>"`. For `kind` "Deployment"/"StatefulSet" it reads `.status.readyReplicas`/`.spec.replicas`; for "DaemonSet" it reads `.status.numberReady`/`.status.desiredNumberScheduled`.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
const deployJSON = `{"items":[{"metadata":{"namespace":"authservice","name":"authservice"},"spec":{"replicas":2},"status":{"readyReplicas":1}}]}`
const dsJSON = `{"items":[{"metadata":{"namespace":"kube-system","name":"rke2-canal"},"status":{"numberReady":1,"desiredNumberScheduled":1}}]}`

func TestWorkloadRows_Deployment(t *testing.T) {
	got, err := WorkloadRows([]byte(deployJSON), "Deployment")
	if err != nil {
		t.Fatalf("WorkloadRows: %v", err)
	}
	want := []WorkloadRow{{Namespace: "authservice", Kind: "Deployment", Name: "authservice", Ready: "1/2"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestWorkloadRows_DaemonSet(t *testing.T) {
	got, _ := WorkloadRows([]byte(dsJSON), "DaemonSet")
	want := []WorkloadRow{{Namespace: "kube-system", Kind: "DaemonSet", Name: "rke2-canal", Ready: "1/1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run TestWorkloadRows -v`
Expected: FAIL — `undefined: WorkloadRows`.

- [ ] **Step 3: Implement** — append to `data/kube.go`:

```go
// WorkloadRow is one row of the workloads view (a deploy/sts/ds).
type WorkloadRow struct {
	Namespace, Kind, Name, Ready string
}

// WorkloadRows parses a deployment/statefulset/daemonset list. DaemonSets report
// readiness under different status fields than deployments/statefulsets.
func WorkloadRows(raw []byte, kind string) ([]WorkloadRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Replicas int `json:"replicas"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas          int `json:"readyReplicas"`
				NumberReady            int `json:"numberReady"`
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse %s json: %w", kind, err)
	}
	rows := make([]WorkloadRow, 0, len(list.Items))
	for _, it := range list.Items {
		ready, desired := it.Status.ReadyReplicas, it.Spec.Replicas
		if kind == "DaemonSet" {
			ready, desired = it.Status.NumberReady, it.Status.DesiredNumberScheduled
		}
		rows = append(rows, WorkloadRow{
			Namespace: it.Metadata.Namespace, Kind: kind, Name: it.Metadata.Name,
			Ready: fmt.Sprintf("%d/%d", ready, desired),
		})
	}
	return rows, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -run TestWorkloadRows -v`
Expected: PASS (2 tests).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): workload (deploy/sts/ds) row-builder"
```

---

## Task 4: Service rows

**Files:**
- Modify: `installer/internal/tui/monitor/data/kube.go`, `data/kube_test.go`

**Interfaces:**
- Produces: `type ServiceRow struct { Namespace, Name, Type, Ports string }`; `func ServiceRows(raw []byte) ([]ServiceRow, error)`. `Ports` joins `.spec.ports[].port` with `,`.

- [ ] **Step 1: Write the failing test** — append to `data/kube_test.go`:

```go
const svcJSON = `{"items":[
 {"metadata":{"namespace":"authservice","name":"authservice"},"spec":{"type":"ClusterIP","ports":[{"port":10003}]}},
 {"metadata":{"namespace":"istio","name":"gw"},"spec":{"type":"LoadBalancer","ports":[{"port":80},{"port":443}]}}]}`

func TestServiceRows(t *testing.T) {
	got, err := ServiceRows([]byte(svcJSON))
	if err != nil {
		t.Fatalf("ServiceRows: %v", err)
	}
	want := []ServiceRow{
		{Namespace: "authservice", Name: "authservice", Type: "ClusterIP", Ports: "10003"},
		{Namespace: "istio", Name: "gw", Type: "LoadBalancer", Ports: "80,443"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/monitor/data/ -run TestServiceRows -v`
Expected: FAIL — `undefined: ServiceRows`.

- [ ] **Step 3: Implement** — append to `data/kube.go`:

```go
// ServiceRow is one row of the services view.
type ServiceRow struct {
	Namespace, Name, Type, Ports string
}

// ServiceRows parses `kubectl get services -A -o json`.
func ServiceRows(raw []byte) ([]ServiceRow, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Namespace string `json:"namespace"`
				Name      string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Type  string `json:"type"`
				Ports []struct {
					Port int `json:"port"`
				} `json:"ports"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("data: parse services json: %w", err)
	}
	rows := make([]ServiceRow, 0, len(list.Items))
	for _, it := range list.Items {
		ports := make([]string, 0, len(it.Spec.Ports))
		for _, p := range it.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d", p.Port))
		}
		rows = append(rows, ServiceRow{
			Namespace: it.Metadata.Namespace, Name: it.Metadata.Name,
			Type: it.Spec.Type, Ports: strings.Join(ports, ","),
		})
	}
	return rows, nil
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/tui/monitor/data/ -v`
Expected: PASS (all data tests, incl. the P1.1 prom tests + the 4 new kube row-builders).

- [ ] **Step 5: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/data/kube.go installer/internal/tui/monitor/data/kube_test.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): service row-builder"
```

---

## Task 5: Generalize the monitor's table views to a string-keyed registry

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: the existing `tableResult`, `fetchPackages`, `fetchApps`, `drawTable`, `refresh`, `setView` (P1.1).
- Produces (consumed by Tasks 6–7): a `tableView` type `{ title string; cols []string; fetch func() tableResult }`; an `m.tableViews map[string]tableView` registry + an ordered `m.viewOrder []string`; `m.view` becomes a `string` ("overview" is the special dashboard, the rest are table views).

This is a refactor that **preserves P1.1 behaviour** (overview default; packages/apps via `1`/`2`; Tab cycle; the async off-UI fetch). It replaces the `viewKind` int enum + the `switch m.view` in `refresh()` with a registry lookup, so Task 6 can add views by registering them.

- [ ] **Step 1: Replace the `viewKind` enum + `m.view` type + the registry**

Change `m.view` from `viewKind` to `string`. Remove the `viewKind`/`viewOverview`/`viewPackages`/`viewApps` const block. Add to the `monitor` struct: `tableViews map[string]tableView` and `viewOrder []string`. Define:

```go
// tableView is a registered table screen: its header title, columns, and an
// off-UI fetch that returns the rows to draw.
type tableView struct {
	title string
	cols  []string
	fetch func() tableResult
}
```

In `Run`, after building `m`, register the two existing table views and set the order (overview first, then the tables):

```go
m.tableViews = map[string]tableView{
	"packages": {title: "PACKAGES", fetch: m.fetchPackages},
	"apps":     {title: "APPS", fetch: m.fetchApps},
}
m.viewOrder = []string{"overview", "packages", "apps"}
m.view = "overview"
```

(`fetchPackages`/`fetchApps` already return a `tableResult` with their own title/cols, so `tableView.title`/`cols` are unused for them — Task 6's resource views use them. Keep `fetchPackages`/`fetchApps` as-is.)

- [ ] **Step 2: Rewrite `refresh`, `setView`, and the nav to use the registry**

`refresh()` (keep the off-UI pattern):

```go
func (m *monitor) refresh() {
	view := m.view
	prom := m.prom
	go func() {
		if view == "overview" {
			in := m.fetchOverview(prom)
			m.app.QueueUpdateDraw(func() {
				if m.view != view {
					return
				}
				m.overviewTV.SetText(views.BuildOverview(in))
				m.setHeader("OVERVIEW", in.Packages)
			})
			return
		}
		tv, ok := m.tableViews[view]
		if !ok {
			return
		}
		res := tv.fetch()
		m.app.QueueUpdateDraw(func() {
			if m.view != view {
				return
			}
			m.drawTable(res)
		})
	}()
}
```

`setView(name string)` switches the page + refreshes:

```go
func (m *monitor) setView(name string) {
	if name != "overview" {
		if _, ok := m.tableViews[name]; !ok {
			return
		}
	}
	m.view = name
	if name == "overview" {
		m.main.SwitchToPage("overview")
	} else {
		m.main.SwitchToPage("table")
		m.table.Select(1, 0)
	}
	m.refresh()
}
```

In the input capture, replace the `0/1/2` + Tab handlers: `'0','o'` → `m.setView("overview")`; `'1'` → `m.setView("packages")`; `'2'` → `m.setView("apps")`. Tab cycles `m.viewOrder` (find the current index, advance with wrap). The startup goroutine's `m.refresh()` and `fetchOverview` are unchanged.

- [ ] **Step 3: Build + full suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && go test ./... -count=1 2>&1 | tail -6`
Expected: build clean; full suite green (the refactor is behaviour-preserving; the data/widgets/views + wizard tests still pass). Adapt any tview API detail against the installed version (rendering-only).

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "refactor(monitor): string-keyed table-view registry (behaviour-preserving)"
```

---

## Task 6: Register the nodes / pods / workloads / services resource views

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `data.NewResources`, `data.NodeRows`/`PodRows`/`WorkloadRows`/`ServiceRows` (Tasks 1–4); the `tableView` registry (Task 5); `tableResult`, `cell`, `phaseCell`, `consoleText`.

- [ ] **Step 1: Add a `res data.Resources` field + the resource fetchers**

Add `res data.Resources` to the `monitor` struct and set `res: data.NewResources()` in the `m := &monitor{…}` literal. Add the four fetchers (each builds a `tableResult` off the UI goroutine; status colour: Ready/Running green, else amber):

```go
// statusCell colours a status string (Ready/Running green, else amber).
func statusCell(s string) *tview.TableCell {
	c := tview.NewTableCell(s + "  ")
	if s == "Ready" || s == "Running" {
		return c.SetTextColor(statusGreen)
	}
	return c.SetTextColor(statusAmber)
}

func (m *monitor) fetchNodes() tableResult {
	raw, err := m.res.Get("nodes")
	if err != nil {
		return tableResult{title: "NODES", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.NodeRows(raw)
	if err != nil {
		return tableResult{title: "NODES", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "NODES"}
	if len(rows) == 0 {
		res.notice = "no nodes"
		return res
	}
	res.cols = []string{"NAME", "ROLES", "STATUS", "VERSION"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Name), cell(r.Roles), statusCell(r.Status), cell(r.Version)})
	}
	return res
}

func (m *monitor) fetchPods() tableResult {
	raw, err := m.res.Get("pods", "-A")
	if err != nil {
		return tableResult{title: "PODS", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.PodRows(raw)
	if err != nil {
		return tableResult{title: "PODS", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "PODS"}
	if len(rows) == 0 {
		res.notice = "no pods"
		return res
	}
	res.cols = []string{"NAMESPACE", "NAME", "READY", "STATUS", "RESTARTS", "NODE"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{
			cell(r.Namespace), cell(r.Name), cell(r.Ready), statusCell(r.Status), cell(fmt.Sprintf("%d", r.Restarts)), cell(r.Node),
		})
	}
	return res
}

func (m *monitor) fetchWorkloads() tableResult {
	res := tableResult{title: "WORKLOADS"}
	specs := []struct{ arg, kind string }{{"deployments", "Deployment"}, {"statefulsets", "StatefulSet"}, {"daemonsets", "DaemonSet"}}
	var all []data.WorkloadRow
	for _, s := range specs {
		raw, err := m.res.Get(s.arg, "-A")
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		rows, err := data.WorkloadRows(raw, s.kind)
		if err != nil {
			return tableResult{title: "WORKLOADS", notice: "error: " + err.Error(), isError: true}
		}
		all = append(all, rows...)
	}
	if len(all) == 0 {
		res.notice = "no workloads"
		return res
	}
	res.cols = []string{"NAMESPACE", "KIND", "NAME", "READY"}
	for _, r := range all {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Namespace), cell(r.Kind), cell(r.Name), cell(r.Ready)})
	}
	return res
}

func (m *monitor) fetchServices() tableResult {
	raw, err := m.res.Get("services", "-A")
	if err != nil {
		return tableResult{title: "SERVICES", notice: "error: " + err.Error(), isError: true}
	}
	rows, err := data.ServiceRows(raw)
	if err != nil {
		return tableResult{title: "SERVICES", notice: "error: " + err.Error(), isError: true}
	}
	res := tableResult{title: "SERVICES"}
	if len(rows) == 0 {
		res.notice = "no services"
		return res
	}
	res.cols = []string{"NAMESPACE", "NAME", "TYPE", "PORTS"}
	for _, r := range rows {
		res.rows = append(res.rows, []*tview.TableCell{cell(r.Namespace), cell(r.Name), cell(r.Type), cell(r.Ports)})
	}
	return res
}
```

- [ ] **Step 2: Register them + update the order + footer**

In `Run`, extend the registry + order:

```go
m.tableViews = map[string]tableView{
	"packages":  {fetch: m.fetchPackages},
	"apps":      {fetch: m.fetchApps},
	"nodes":     {fetch: m.fetchNodes},
	"pods":      {fetch: m.fetchPods},
	"workloads": {fetch: m.fetchWorkloads},
	"services":  {fetch: m.fetchServices},
}
m.viewOrder = []string{"overview", "nodes", "pods", "workloads", "services", "packages", "apps"}
```

Update `footerText()` to advertise the command bar (Tab + `:`): `… [#FFFFFF::b]Tab[-:-:-] [#7C8694]cycle[-]   [#FFFFFF::b]:[-:-:-] [#7C8694]view[-]   [#FFFFFF::b]q[-:-:-] [#7C8694]quit[-]` (keep `0 overview` / `1 packages` / `2 apps`). (`tableView.title`/`cols` fields can be dropped if unused — each `fetch` sets the title/cols on the `tableResult` directly.)

- [ ] **Step 3: Build + smoke-compile**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; suite green. `go run ./cmd/srectl monitor --help` still works.

- [ ] **Step 4: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): nodes/pods/workloads/services resource views"
```

---

## Task 7: The `:` command bar

**Files:**
- Modify: `installer/internal/tui/monitor/monitor.go`

**Interfaces:**
- Consumes: `m.setView`, `m.viewOrder`, the `app`/`main`/`layout` from `Run`, the `tableViews` registry.

- [ ] **Step 1: Add a command-bar InputField + show/hide + dispatch**

Add `cmdBar *tview.InputField` to the `monitor` struct. In `Run`, build it and put it in the layout as a hidden bottom row (replacing the footer height when visible). Concretely, build a command bar that, on `:` , focuses an input; on Enter, switches to the typed view; on Esc, hides:

```go
cmdBar := tview.NewInputField().SetLabel(" : ").SetFieldWidth(0)
cmdBar.SetLabelColor(tui.ColorSelectText).SetFieldTextColor(consoleText).
	SetFieldBackgroundColor(consoleBg).SetBackgroundColor(consoleBg)
m.cmdBar = cmdBar
```

Use a `tview.Pages` or a swapped footer row to show the command bar over the footer. Simplest: keep a `bottom := tview.NewPages().AddPage("footer", footer, true, true).AddPage("cmd", cmdBar, true, false)` as the layout's bottom item; `:` shows "cmd" + focuses it, Enter/Esc shows "footer".

```go
cmdBar.SetDoneFunc(func(key tcell.Key) {
	if key == tcell.KeyEnter {
		name := strings.TrimSpace(cmdBar.GetText())
		cmdBar.SetText("")
		bottom.SwitchToPage("footer")
		m.app.SetFocus(m.main)
		if name == "overview" {
			m.setView("overview")
		} else if _, ok := m.tableViews[name]; ok {
			m.setView(name)
		}
		return
	}
	if key == tcell.KeyEscape {
		cmdBar.SetText("")
		bottom.SwitchToPage("footer")
		m.app.SetFocus(m.main)
	}
})
```

In the app input capture, add `case ':'` → `bottom.SwitchToPage("cmd"); m.app.SetFocus(cmdBar); return nil`. (Guard: only when the cmd bar isn't already focused.)

- [ ] **Step 2: Build + full suite**

Run: `cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && go build ./... && go vet ./internal/tui/monitor/... && go test ./... -count=1 2>&1 | tail -4`
Expected: build clean; suite green. Adapt tview InputField/Pages API specifics against the installed version (rendering-only).

- [ ] **Step 3: Commit**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon
git add installer/internal/tui/monitor/monitor.go
git -c user.name=JongoDB -c user.email=198221045+JongoDB@users.noreply.github.com commit -m "feat(monitor): : command bar to jump between views"
```

---

## Task 8: Lab smoke (manual)

- [ ] **Step 1: Cross-compile + deliver to the bastion**

```bash
cd /Users/JonWFH/jondev/sre-v2-wt-mon/installer && GOOS=linux GOARCH=amd64 go build -o /tmp/srectl ./cmd/srectl
cat /tmp/srectl | ssh cosmos@cosmos-ssh.fightingsmartcyber.com 'cat > /tmp/srectl && chmod +x /tmp/srectl'
```

- [ ] **Step 2: Drive the resource views in tmux**

```bash
ssh cosmos@cosmos-ssh.fightingsmartcyber.com bash -s <<'EOF'
tmux kill-session -t mon 2>/dev/null || true
tmux new-session -d -s mon -x 130 -y 40; sleep 0.4
tmux send-keys -t mon '/tmp/srectl monitor' Enter; sleep 2.5
tmux send-keys -t mon '3'; sleep 1.5; echo "=== NODES ==="; tmux capture-pane -t mon -p | sed -n '1,8p'
tmux send-keys -t mon '4'; sleep 1.5; echo "=== PODS (key 4) ==="; tmux capture-pane -t mon -p | sed -n '1,10p'
tmux send-keys -t mon ':'; sleep 0.3; tmux send-keys -t mon 'services' Enter; sleep 1.5; echo "=== :services ==="; tmux capture-pane -t mon -p | sed -n '1,8p'
tmux send-keys -t mon q
EOF
```
Expected (manual): the **nodes** view (cosmos-k8s · control-plane,etcd · Ready · v1.35.5), the **pods** view (56 pods with READY/STATUS/RESTARTS/NODE), and the `:services` jump (40 services with TYPE/PORTS). The footer advertises `Tab cycle · : view`. (Nodes/pods/services were reached via the number keys + the command bar.)

- [ ] **Step 3: PING the user** to drive it interactively (`ssh cosmos@cosmos-ssh.fightingsmartcyber.com -t /tmp/srectl monitor`) — `3`/`4` for nodes/pods, `Tab` to cycle, `:pods`/`:services` via the command bar, `q` to quit (still prompt). Confirm it reads like a cluster browser before the next P1.2 slice (events, host/node-exporter panel, drill-in, sparklines).

---

## Self-Review

**1. Spec coverage (Phase 1.2 resource-browser slice):** the CLUSTER-layer resource views (nodes/pods/workloads/services) §4 → Tasks 1–4 (row-builders) + Task 6 (views); the `:` command-bar navigation §6 → Task 7; the data layer behind the exec-wrapper §3.2/§3.4 → Task 1 (`Resources`). Deferred + noted: events view, host/node-exporter panel, drill-in, sparkline range-queries (later P1.2 slices).

**2. Placeholder scan:** row-builder tasks (1–4) ship complete code + real-shape fixtures. The monitor tasks (5–7) are integration: concrete edits with code, build+suite-gated; tview API specifics adapt against the installed version (rendering-only, smoke-verified in Task 8). No "TODO"/"handle errors"/"similar to".

**3. Type consistency:** `data.Resources.Get(args…)` (Task 1) used by the fetchers in Task 6. `NodeRow`/`PodRow`/`WorkloadRow`/`ServiceRow` + their `…Rows` parsers (Tasks 1–4) consumed by Task 6's `fetch*`. `tableView{title,cols,fetch}` + `m.tableViews`/`m.viewOrder` + `m.view string` (Task 5) used by Tasks 6–7. `tableResult` (P1.1) returned by every `fetch*`. `statusCell` defined in Task 6, used by fetchNodes/fetchPods. The async off-UI `refresh()` pattern (P1.1) preserved in Task 5.
