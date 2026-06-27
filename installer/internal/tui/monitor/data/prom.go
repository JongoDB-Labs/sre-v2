// Package data is the monitor's cluster data layer: Prometheus (this file) and
// kubectl resource fetch, behind fake-backed exec-wrappers so the parsing/query
// logic is unit-tested with no cluster.
package data

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
