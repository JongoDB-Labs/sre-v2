package appcatalog

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// fakeCosign is a hand-written test double for the Cosign wrapper.
type fakeCosign struct {
	err       error
	gotRef    string
	gotIDRe   string
	gotIssuer string
}

func (f *fakeCosign) Verify(ref, idRe, issuer string) error {
	f.gotRef, f.gotIDRe, f.gotIssuer = ref, idRe, issuer
	return f.err
}

func entryWithVerify() Entry {
	return Entry{
		Name: "cosmos",
		Verify: Verify{
			IdentityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/",
			Issuer:         "https://token.actions.githubusercontent.com",
		},
	}
}

func TestVerify_PassesThroughToCosign(t *testing.T) {
	fc := &fakeCosign{}
	if err := CheckSignature(fc, entryWithVerify(), "ghcr.io/x/cosmos@sha256:abc"); err != nil {
		t.Fatalf("CheckSignature: %v", err)
	}
	if fc.gotRef != "ghcr.io/x/cosmos@sha256:abc" {
		t.Errorf("ref = %q, want the pinned ref", fc.gotRef)
	}
	if fc.gotIDRe != entryWithVerify().Verify.IdentityRegexp || fc.gotIssuer != entryWithVerify().Verify.Issuer {
		t.Errorf("identity/issuer not forwarded: %+v", fc)
	}
}

func TestVerify_FailClosedOnBadSignature(t *testing.T) {
	err := CheckSignature(&fakeCosign{err: errors.New("no matching signatures")}, entryWithVerify(), "ref@sha256:abc")
	if err == nil {
		t.Fatal("CheckSignature must fail closed when cosign errors")
	}
	if !strings.Contains(err.Error(), entryWithVerify().Verify.IdentityRegexp) {
		t.Errorf("error should name the expected identity, got: %v", err)
	}
}

func TestVerify_FailClosedOnMissingPolicy(t *testing.T) {
	e := entryWithVerify()
	e.Verify.IdentityRegexp = "" // no expected identity configured
	if err := CheckSignature(&fakeCosign{}, e, "ref@sha256:abc"); err == nil {
		t.Error("CheckSignature must refuse to run with no expected signer identity (fail-closed)")
	}
	e2 := entryWithVerify()
	e2.Verify.IdentityRegexp = "([" // invalid regexp
	if err := CheckSignature(&fakeCosign{}, e2, "ref@sha256:abc"); err == nil {
		t.Error("CheckSignature must refuse an invalid identity regexp (fail-closed)")
	}
}

func TestExecCosign_BuildsVerifyCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	orig := commandContext
	commandContext = func(name string, args ...string) *exec.Cmd {
		gotName, gotArgs = name, args
		return exec.Command("true")
	}
	defer func() { commandContext = orig }()

	_ = (execCosign{}).Verify("ref@sha256:abc", "^https://github.com/JongoDB-Labs/", "https://token.actions.githubusercontent.com")
	if gotName != "cosign" {
		t.Errorf("binary = %q, want cosign", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"verify", "ref@sha256:abc", "--certificate-identity-regexp", "--certificate-oidc-issuer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, gotArgs)
		}
	}
}
