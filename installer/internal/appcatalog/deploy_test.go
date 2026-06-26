package appcatalog

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeUDS is a hand-written double for the UDS wrapper.
type fakeUDS struct {
	deployErr   error
	removeErr   error
	deployed    string
	removed     string
	removeCount int
}

func (f *fakeUDS) Deploy(ref string) error  { f.deployed = ref; return f.deployErr }
func (f *fakeUDS) Remove(name string) error { f.removed = name; f.removeCount++; return f.removeErr }

func TestDeploy_HappyPath(t *testing.T) {
	fu := &fakeUDS{}
	if err := Deploy(fu, "ghcr.io/x/cosmos@sha256:abc", "cosmos"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if fu.deployed != "ghcr.io/x/cosmos@sha256:abc" {
		t.Errorf("deployed ref = %q", fu.deployed)
	}
	if fu.removeCount != 0 {
		t.Error("a successful deploy must not trigger a rollback remove")
	}
}

func TestDeploy_RollsBackOnFailure(t *testing.T) {
	fu := &fakeUDS{deployErr: errors.New("reconcile timeout")}
	err := Deploy(fu, "ghcr.io/x/cosmos@sha256:abc", "cosmos")
	if err == nil {
		t.Fatal("Deploy should return the deploy error")
	}
	if fu.removeCount != 1 {
		t.Errorf("a failed deploy should best-effort remove once, got %d", fu.removeCount)
	}
	// rollback must use the package name, not the OCI ref
	if fu.removed != "cosmos" {
		t.Errorf("rollback removed %q, want package name %q", fu.removed, "cosmos")
	}
	if !strings.Contains(err.Error(), "reconcile timeout") {
		t.Errorf("error should wrap the deploy failure, got %v", err)
	}
}

func TestRemove_Wraps(t *testing.T) {
	fu := &fakeUDS{}
	if err := Remove(fu, "cosmos"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if fu.removed != "cosmos" {
		t.Errorf("removed = %q, want cosmos", fu.removed)
	}
}

func TestExecUDS_BuildsCommands(t *testing.T) {
	var calls [][]string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		calls = append(calls, append([]string{name}, args...))
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	_ = (execUDS{}).Deploy("ref@sha256:abc")
	_ = (execUDS{}).Remove("cosmos")
	if len(calls) != 2 {
		t.Fatalf("want 2 commands, got %d", len(calls))
	}
	if strings.Join(calls[0], " ") != "uds deploy ref@sha256:abc --confirm" {
		t.Errorf("deploy argv = %v", calls[0])
	}
	if strings.Join(calls[1], " ") != "uds remove cosmos --confirm" {
		t.Errorf("remove argv = %v", calls[1])
	}
}
