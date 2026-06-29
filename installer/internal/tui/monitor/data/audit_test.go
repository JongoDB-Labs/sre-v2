package data

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAuditEntryJSONL(t *testing.T) {
	e := AuditEntry{
		Time: "2026-06-27T03:00:00Z", Actor: "default", Action: "cordon",
		Kind: "nodes", Name: "cosmos-k8s", Command: "kubectl cordon cosmos-k8s",
		ExitCode: 0, OK: true,
	}
	line := e.JSONL()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("JSONL must end in newline: %q", line)
	}
	var back AuditEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &back); err != nil {
		t.Fatalf("JSONL not valid json: %v", err)
	}
	if back.Action != "cordon" || back.Name != "cosmos-k8s" || !back.OK {
		t.Fatalf("round-trip lost fields: %+v", back)
	}
}

func TestAuditPath_NonEmpty(t *testing.T) {
	if AuditPath() == "" {
		t.Fatal("AuditPath must not be empty")
	}
	if !strings.HasSuffix(AuditPath(), "platform-actions.jsonl") {
		t.Fatalf("unexpected audit path: %s", AuditPath())
	}
}

func TestAuditPatch(t *testing.T) {
	got := auditPatch("a123", `{"action":"delete","ok":true}`+"\n")
	// must be valid JSON of shape {"data":{"a123":"<jsonl>"}} with the jsonl escaped
	var p struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal([]byte(got), &p); err != nil {
		t.Fatalf("auditPatch not valid json: %v (%s)", err, got)
	}
	if p.Data["a123"] != `{"action":"delete","ok":true}`+"\n" {
		t.Fatalf("round-trip lost the jsonl value: %q", p.Data["a123"])
	}
}

type fakeAuditor struct {
	got []AuditEntry
	err error
}

func (f *fakeAuditor) Record(e AuditEntry) error { f.got = append(f.got, e); return f.err }

func TestMultiAuditor_RecordsAllEvenOnError(t *testing.T) {
	a := &fakeAuditor{err: errorString("boom")} // a fails
	b := &fakeAuditor{}                         // b should still get it
	m := NewMultiAuditor(a, b)
	err := m.Record(AuditEntry{Action: "delete"})
	if err == nil {
		t.Fatal("multi must surface the sub-auditor error")
	}
	if len(a.got) != 1 || len(b.got) != 1 {
		t.Fatalf("both sub-auditors must be attempted: a=%d b=%d", len(a.got), len(b.got))
	}
}

type errorString string

func (e errorString) Error() string { return string(e) }
