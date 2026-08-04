package source

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExecZarf_BuildsRegistryDigestCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		// Return a harmless command so .Output() doesn't hit a real binary.
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	if _, err := (execZarf{}).RegistryDigest("ghcr.io/x/bundles/gitea:1.0.0"); err != nil {
		t.Fatalf("RegistryDigest: %v", err)
	}
	if gotName != "zarf" {
		t.Errorf("binary = %q, want zarf", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "tools registry digest") || !strings.Contains(joined, "ghcr.io/x/bundles/gitea:1.0.0") {
		t.Errorf("args = %v, want a `tools registry digest <ref>` invocation", gotArgs)
	}
}
