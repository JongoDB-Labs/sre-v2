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

// TestExecZarf_BuildsInspectCommand pins Inspect's argv to the modern zarf CLI
// syntax. zarf >= ~v0.79 requires a subcommand after `package inspect`
// (`definition`, `sbom`, ...) — `zarf package inspect <ref>` alone errors
// ("requires a subcommand") on every current zarf, which silently broke the
// missing-require preflight check on every install. See
// installer/internal/appcatalog/preflight.go and the gitea onboarding
// acceptance report's "Secondary finding".
func TestExecZarf_BuildsInspectCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	if _, err := (execZarf{}).Inspect("ghcr.io/x/bundles/gitea:1.0.0"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if gotName != "zarf" {
		t.Errorf("binary = %q, want zarf", gotName)
	}
	want := []string{"package", "inspect", "definition", "ghcr.io/x/bundles/gitea:1.0.0"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args = %v, want %v", gotArgs, want)
		}
	}
}
