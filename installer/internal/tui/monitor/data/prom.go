// Package data is the monitor's cluster data layer: Prometheus (this file) and
// kubectl resource fetch, behind fake-backed exec-wrappers so the parsing/query
// logic is unit-tested with no cluster.
package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("prom: parse value %q: %w", s, err)
	}
	return v, nil
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

// PodPhaseCounts reduces a `sum by (phase) (kube_pod_status_phase)` vector to a
// phase→count map. Samples without a phase label are skipped.
func PodPhaseCounts(samples []Sample) map[string]int {
	out := make(map[string]int, len(samples))
	for _, s := range samples {
		phase := s.Labels["phase"]
		if phase == "" {
			continue
		}
		out[phase] = int(s.Value)
	}
	return out
}

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

// Named PromQL the dashboard uses (kept as constants so each is reviewable).
const (
	QNodeCPUPct    = `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
	QNodeMemPct    = `100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))`
	QFiringAlerts  = `ALERTS{alertstate="firing"}`
	QNodeCPUSeries = `100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
	QNodeMemSeries = `100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))`
	// QNodeDiskPct is root-filesystem usage %, sum-based across nodes (matches QNodeMemPct).
	QNodeDiskPct = `100 * (1 - sum(node_filesystem_avail_bytes{mountpoint="/"}) / sum(node_filesystem_size_bytes{mountpoint="/"}))`
	// QNodeLoad is the cluster-average 1-minute load.
	QNodeLoad = `avg(node_load1)`
	// QPodPhase is the pod count grouped by lifecycle phase (kube-state-metrics).
	QPodPhase = `sum by (phase) (kube_pod_status_phase)`
)

// Raw runs `kubectl get --raw <path>` and returns the body. Tests inject a fake.
type Raw interface {
	Get(path string) ([]byte, error)
}

// rawTimeout bounds each `kubectl get --raw` so a stalled API call returns an
// error (→ the monitor degrades to MetricsOK=false) instead of hanging the caller.
const rawTimeout = 4 * time.Second

type execRaw struct{}

// NewRaw returns the production Raw wrapper.
func NewRaw() Raw { return execRaw{} }

// Get runs `kubectl get --raw <path>`, bounded by rawTimeout.
func (execRaw) Get(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), rawTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "kubectl", "get", "--raw", path).Output()
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
// Returns "" if Ref is malformed (missing "/").
func (p Prom) proxyBase() string {
	parts := strings.SplitN(p.Ref, "/", 2) // ns / name:port
	if len(parts) != 2 {
		return ""
	}
	return fmt.Sprintf("/api/v1/namespaces/%s/services/%s/proxy/api/v1", parts[0], parts[1])
}

// Query runs an instant PromQL query and returns the vector samples.
func (p Prom) Query(promql string) ([]Sample, error) {
	base := p.proxyBase()
	if base == "" {
		return nil, fmt.Errorf("prom: invalid Ref %q", p.Ref)
	}
	path := base + "/query?query=" + url.QueryEscape(promql)
	raw, err := p.Raw.Get(path)
	if err != nil {
		return nil, err
	}
	return ParseVector(raw)
}

// QueryRange runs a range PromQL query (for sparklines) and returns the series.
func (p Prom) QueryRange(promql string, start, end, step int64) ([]Series, error) {
	base := p.proxyBase()
	if base == "" {
		return nil, fmt.Errorf("prom: invalid Ref %q", p.Ref)
	}
	path := fmt.Sprintf("%s/query_range?query=%s&start=%d&end=%d&step=%d",
		base, url.QueryEscape(promql), start, end, step)
	raw, err := p.Raw.Get(path)
	if err != nil {
		return nil, err
	}
	return ParseMatrix(raw)
}
