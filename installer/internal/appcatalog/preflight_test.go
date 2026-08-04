package appcatalog

import (
	"errors"
	"testing"
)

// fakeInspector is a hand-written double for the inspect dependency.
type fakeInspector struct {
	out []byte
	err error
}

func (f *fakeInspector) Inspect(string) ([]byte, error) { return f.out, f.err }

const manifestWithCR = `kind: ZarfPackageConfig
components:
  - name: cosmos
    manifests:
      - name: cosmos-uds-package
        # ...
kind: Package
apiVersion: uds.dev/v1alpha1
`

const manifestNoCR = `kind: ZarfPackageConfig
components:
  - name: cosmos
    charts:
      - name: cosmos
`

func TestPreflight_WarnsOnMissingPackageCR(t *testing.T) {
	warns, err := Preflight(&fakeInspector{out: []byte(manifestNoCR)},
		Entry{Name: "cosmos"}, "ref", map[string]bool{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !hasWarning(warns, "no-package-cr") {
		t.Errorf("expected a no-package-cr warning, got %+v", warns)
	}
}

func TestPreflight_WarnsOnMissingRequire(t *testing.T) {
	// service id from the substrate catalog, e.g. "pgo" not "postgres"
	warns, err := Preflight(&fakeInspector{out: []byte(manifestWithCR)},
		Entry{Name: "cosmos", Requires: []string{"pgo"}}, "ref",
		map[string]bool{}) // pgo NOT installed
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !hasWarning(warns, "missing-require") {
		t.Errorf("expected a missing-require warning, got %+v", warns)
	}
}

func TestPreflight_CleanWhenCohesionPresent(t *testing.T) {
	// service id from the substrate catalog, e.g. "pgo" not "postgres"
	warns, err := Preflight(&fakeInspector{out: []byte(manifestWithCR)},
		Entry{Name: "cosmos", Requires: []string{"pgo"}}, "ref",
		map[string]bool{"pgo": true})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings, got %+v", warns)
	}
}

func TestPreflight_InspectErrorIsAdvisoryButReturned(t *testing.T) {
	_, err := Preflight(&fakeInspector{err: errors.New("cannot read package")},
		Entry{Name: "cosmos"}, "ref", map[string]bool{})
	if err == nil {
		t.Error("an inspect I/O failure should surface as an error to the caller")
	}
}

// TestPreflight_InspectErrorStillWarnsMissingRequire pins the decoupling fix:
// the missing-require check only needs the live installed-packages set (passed
// in directly), not zarf inspect output — so it must still run and warn even
// when inspect fails. Before this fix, an inspect error short-circuited
// Preflight before the requires loop ever ran, so missing-require warnings
// were silently skipped on every install (see the gitea onboarding
// acceptance report's "Secondary finding").
func TestPreflight_InspectErrorStillWarnsMissingRequire(t *testing.T) {
	warns, err := Preflight(&fakeInspector{err: errors.New("requires a subcommand")},
		Entry{Name: "cosmos", Requires: []string{"pgo"}}, "ref",
		map[string]bool{}) // pgo NOT installed
	if err == nil {
		t.Error("an inspect I/O failure should still surface as an error to the caller")
	}
	if !hasWarning(warns, "missing-require") {
		t.Errorf("expected a missing-require warning despite the inspect failure, got %+v", warns)
	}
}

// TestPreflight_InspectErrorWithSatisfiedRequiresHasNoWarning is the
// counterpart: when inspect fails but all requires ARE satisfied, no
// missing-require warning should fire (and no no-package-cr warning either,
// since that check genuinely can't run without inspect output).
func TestPreflight_InspectErrorWithSatisfiedRequiresHasNoWarning(t *testing.T) {
	warns, err := Preflight(&fakeInspector{err: errors.New("requires a subcommand")},
		Entry{Name: "cosmos", Requires: []string{"pgo"}}, "ref",
		map[string]bool{"pgo": true}) // pgo installed
	if err == nil {
		t.Error("an inspect I/O failure should still surface as an error to the caller")
	}
	if len(warns) != 0 {
		t.Errorf("expected no warnings when requires are satisfied, got %+v", warns)
	}
}

// hasWarning is a small test helper.
func hasWarning(ws []Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}
