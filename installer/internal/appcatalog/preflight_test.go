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

// hasWarning is a small test helper.
func hasWarning(ws []Warning, code string) bool {
	for _, w := range ws {
		if w.Code == code {
			return true
		}
	}
	return false
}
