package source

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecZarf_BuildsInspectCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		// Return a harmless command so .Output() doesn't hit a real binary.
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	if _, err := (execZarf{}).Inspect("ghcr.io/x/cosmos"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if gotName != "zarf" {
		t.Errorf("binary = %q, want zarf", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "package inspect") || !strings.Contains(joined, "ghcr.io/x/cosmos") {
		t.Errorf("args = %v, want a `package inspect <ref>` invocation", gotArgs)
	}
}
