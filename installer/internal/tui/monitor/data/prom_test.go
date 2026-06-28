package data

import (
	"errors"
	"reflect"
	"strings"
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

// TestProm_Query_MalformedRef: Ref without "/" must return an error, not panic.
func TestProm_Query_MalformedRef(t *testing.T) {
	p := Prom{Raw: &fakeRaw{}, Ref: "noslash"}
	_, err := p.Query("up")
	if err == nil {
		t.Fatal("expected error for malformed Ref, got nil")
	}
}

// TestProm_Query_RawError: Raw.Get error must be propagated by Query.
func TestProm_Query_RawError(t *testing.T) {
	p := Prom{Raw: &fakeRaw{err: errors.New("boom")}, Ref: "monitoring/prometheus:9090"}
	_, err := p.Query("up")
	if err == nil {
		t.Fatal("expected error from Raw.Get, got nil")
	}
}

func TestPodPhaseCounts(t *testing.T) {
	samples := []Sample{
		{Labels: map[string]string{"phase": "Running"}, Value: 44},
		{Labels: map[string]string{"phase": "Succeeded"}, Value: 12},
		{Labels: map[string]string{"phase": "Pending"}, Value: 0},
		{Labels: map[string]string{"phase": ""}, Value: 7}, // no phase label → skipped
	}
	got := PodPhaseCounts(samples)
	if got["Running"] != 44 || got["Succeeded"] != 12 || got["Pending"] != 0 {
		t.Fatalf("counts wrong: %+v", got)
	}
	if _, ok := got[""]; ok {
		t.Fatalf("empty-phase sample must be skipped: %+v", got)
	}
}

func TestAlertRows(t *testing.T) {
	samples := []Sample{
		{Labels: map[string]string{"alertname": "UDSProbeEndpointDown", "severity": "warning", "namespace": "grafana"}},
		{Labels: map[string]string{"alertname": "etcdInsufficientMembers", "severity": "critical", "namespace": "kube-system"}},
		{Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}}, // synthetic → skipped
	}
	got := AlertRows(samples)
	if len(got) != 2 {
		t.Fatalf("want 2 rows (Watchdog skipped), got %d: %+v", len(got), got)
	}
	if got[0].Name != "etcdInsufficientMembers" || got[0].Severity != "critical" {
		t.Fatalf("critical must sort first: %+v", got[0])
	}
	if got[1].Name != "UDSProbeEndpointDown" || got[1].Namespace != "grafana" {
		t.Fatalf("row2 wrong: %+v", got[1])
	}
}
