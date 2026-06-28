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
