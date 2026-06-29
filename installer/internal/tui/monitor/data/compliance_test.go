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
