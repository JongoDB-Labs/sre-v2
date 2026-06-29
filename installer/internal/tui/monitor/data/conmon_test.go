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
