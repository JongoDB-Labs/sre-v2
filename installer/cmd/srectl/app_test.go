package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"gopkg.in/yaml.v3"
)

// sentinels used only in tests.
var errSigForTest = errors.New("no matching signatures")

// --- fakes (mirror the package-level interfaces) ---

type fakeCosign struct{ err error }

func (f fakeCosign) Verify(ref, idRe, issuer string) error { return f.err }

type fakeUDS struct {
	deployErr error
	deployed  string
	removed   string
}

func (f *fakeUDS) Deploy(ref string) error  { f.deployed = ref; return f.deployErr }
func (f *fakeUDS) Remove(name string) error { f.removed = name; return nil }

type fakeInspect struct {
	out []byte
	err error
}

func (f fakeInspect) Inspect(string) ([]byte, error) { return f.out, f.err }

type fakeKube struct {
	cm           map[string]string
	packagesJSON []byte
}

func (f *fakeKube) EnsureNamespace(string) error { return nil }
func (f *fakeKube) GetConfigMap(ns, name string) ([]byte, error) {
	if f.cm == nil {
		// Return an empty map so State.Load treats it as zero records.
		return []byte("{}"), nil
	}
	return marshalCMForTest(f.cm), nil
}
func (f *fakeKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	f.cm = data
	return nil
}
func (f *fakeKube) ListPackages() ([]byte, error) {
	if f.packagesJSON == nil {
		return []byte(`{"items":[]}`), nil
	}
	return f.packagesJSON, nil
}

func marshalCMForTest(data map[string]string) []byte {
	b, err := yaml.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}

func testCatalog() *appcatalog.Catalog {
	return &appcatalog.Catalog{
		APIVersion: "sre/v1",
		Apps: []appcatalog.Entry{{
			Name:    "cosmos",
			Version: "2.102.0",
			Source:  appcatalog.Source{Type: appcatalog.SourceOCI, Ref: "ghcr.io/x/cosmos"},
			Verify: appcatalog.Verify{
				IdentityRegexp: "^https://github.com/JongoDB-Labs/",
				Issuer:         "https://token.actions.githubusercontent.com",
			},
		}},
	}
}

const inspectWithCRAndDigest = "kind: Package\nmanifestDigest: sha256:" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"

func testDeps(kube *fakeKube, uds *fakeUDS) appDeps {
	z := fakeInspect{out: []byte(inspectWithCRAndDigest)}
	return appDeps{
		Cat:     testCatalog(),
		Cosign:  fakeCosign{},
		UDS:     uds,
		Inspect: z,
		State:   appcatalog.State{Kube: kube},
		Zarf:    z,
		Now:     func() string { return "2026-06-26T00:00:00Z" },
		Actor:   "tester",
	}
}

func TestRunAppList_ShowsCatalog(t *testing.T) {
	var out bytes.Buffer
	if err := runAppList(&out, testDeps(&fakeKube{}, &fakeUDS{}), false); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "cosmos") || !strings.Contains(out.String(), "2.102.0") {
		t.Errorf("list output missing cosmos/version:\n%s", out.String())
	}
}

func TestRunAppInstall_HappyPathDeploysAndRecords(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	var out bytes.Buffer
	if err := runAppInstall(&out, testDeps(kube, uds), "cosmos"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !strings.Contains(uds.deployed, "ghcr.io/x/cosmos@sha256:") {
		t.Errorf("expected a digest-pinned deploy, got %q", uds.deployed)
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if recs["cosmos"].Version != "2.102.0" {
		t.Errorf("install should write the record, got %+v", recs["cosmos"])
	}
}

func TestRunAppInstall_FailClosedDoesNotDeploy(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	d := testDeps(kube, uds)
	d.Cosign = fakeCosign{err: errSigForTest} // signature fails
	var out bytes.Buffer
	if err := runAppInstall(&out, d, "cosmos"); err == nil {
		t.Fatal("install must abort when verify fails (fail-closed)")
	}
	if uds.deployed != "" {
		t.Error("a fail-closed verify must NOT reach uds deploy")
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if _, ok := recs["cosmos"]; ok {
		t.Error("no record should be written on a failed install")
	}
}

func TestRunAppInstall_UnknownApp(t *testing.T) {
	if err := runAppInstall(&bytes.Buffer{}, testDeps(&fakeKube{}, &fakeUDS{}), "ghost"); err == nil {
		t.Error("install of an unknown app should error")
	}
}

func TestRunAppRemove_RemovesAndPrunes(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	_ = (appcatalog.State{Kube: kube}).Put("cosmos", appcatalog.Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"})
	var out bytes.Buffer
	if err := runAppRemove(&out, testDeps(kube, uds), "cosmos"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if uds.removed != "cosmos" {
		t.Errorf("remove should uds-remove cosmos, got %q", uds.removed)
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if _, ok := recs["cosmos"]; ok {
		t.Error("remove should prune the record")
	}
}

func TestRunAppStatus_ReportsRecordAndLive(t *testing.T) {
	kube := &fakeKube{packagesJSON: []byte(`{"items":[{"metadata":{"name":"cosmos"}}]}`)}
	_ = (appcatalog.State{Kube: kube}).Put("cosmos", appcatalog.Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"})
	var out bytes.Buffer
	if err := runAppStatus(&out, testDeps(kube, &fakeUDS{}), "cosmos"); err != nil {
		t.Fatalf("status: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "cosmos") || !strings.Contains(s, "installed") {
		t.Errorf("status output missing cosmos/installed:\n%s", s)
	}
}

func TestAdapterFor_GitHubDeferred(t *testing.T) {
	_, err := adapterFor(appcatalog.Entry{Source: appcatalog.Source{Type: appcatalog.SourceGitHub}}, fakeInspect{})
	if err == nil {
		t.Error("github adapter is deferred and should error")
	}
}

// TestRunAppInstall_AllowUnsignedDoesNotBypassSignedSource pins decision #2:
// --allow-unsigned MUST NOT weaken verification for signed (OCI / digest-bearing)
// sources. When the Cosign fake fails, the install must abort before deploy and
// before writing a state record — even if AllowUnsigned is true.
func TestRunAppInstall_AllowUnsignedDoesNotBypassSignedSource(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}
	d := testDeps(kube, uds)
	d.AllowUnsigned = true
	d.Cosign = fakeCosign{err: errors.New("no matching signatures")}

	var out bytes.Buffer
	if err := runAppInstall(&out, d, "cosmos"); err == nil {
		t.Fatal("install must abort when cosign verify fails for a signed OCI source, even with AllowUnsigned=true")
	}
	if uds.deployed != "" {
		t.Errorf("AllowUnsigned must NOT reach uds deploy when the signed source fails verify; got deployed=%q", uds.deployed)
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if _, ok := recs["cosmos"]; ok {
		t.Error("no state record should be written when verify fails (fail-closed)")
	}
}

// TestRunAppInstall_PreflightErrorDoesNotAbort pins decision #1: preflight is
// advisory. An Inspect I/O failure from the Inspector (preflight) must be logged
// but never abort the install — deploy and record must still succeed.
func TestRunAppInstall_PreflightErrorDoesNotAbort(t *testing.T) {
	kube := &fakeKube{}
	uds := &fakeUDS{}

	// Zarf (OCI resolver) succeeds so we get a digest-pinned ref; Cosign passes.
	zarfOK := fakeInspect{out: []byte(inspectWithCRAndDigest)}
	// Inspect (preflight) fails — simulates a transient I/O error in the inspector.
	inspectFail := fakeInspect{err: errors.New("inspect failed")}

	d := appDeps{
		Cat:     testCatalog(),
		Cosign:  fakeCosign{},
		UDS:     uds,
		Inspect: inspectFail,
		State:   appcatalog.State{Kube: kube},
		Zarf:    zarfOK,
		Now:     func() string { return "2026-06-26T00:00:00Z" },
		Actor:   "tester",
	}

	var out bytes.Buffer
	if err := runAppInstall(&out, d, "cosmos"); err != nil {
		t.Fatalf("preflight error is advisory — install must NOT abort, got: %v", err)
	}
	if uds.deployed == "" {
		t.Error("deploy must proceed even when preflight Inspect fails")
	}
	recs, _ := (appcatalog.State{Kube: kube}).Load()
	if recs["cosmos"].Version != "2.102.0" {
		t.Errorf("state record must be written after a successful deploy, got: %+v", recs["cosmos"])
	}
}
