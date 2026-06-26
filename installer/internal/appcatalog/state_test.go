package appcatalog

import (
	"errors"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// fakeKube is a hand-written, in-memory double for the Kube wrapper.
type fakeKube struct {
	cm           map[string]string // current ConfigMap data, nil ⇒ not found
	cmMissing    bool
	nsEnsured    string
	applied      map[string]string
	packagesJSON []byte
	listErr      error
}

func (f *fakeKube) EnsureNamespace(ns string) error { f.nsEnsured = ns; return nil }

func (f *fakeKube) GetConfigMap(ns, name string) ([]byte, error) {
	if f.cmMissing {
		return nil, errMissingConfigMap
	}
	return marshalConfigMapForTest(f.cm), nil
}

func (f *fakeKube) ApplyConfigMap(ns, name string, data map[string]string) error {
	f.applied = data
	f.cm = data
	f.cmMissing = false
	return nil
}

func (f *fakeKube) ListPackages() ([]byte, error) { return f.packagesJSON, f.listErr }

func TestState_PutThenLoad(t *testing.T) {
	fk := &fakeKube{cmMissing: true}
	s := State{Kube: fk}
	rec := Record{Version: "2.102.0", Source: "oci:ghcr.io/x/cosmos", Digest: "sha256:abc", InstalledAt: "2026-06-26T00:00:00Z", InstalledBy: "maggie"}
	if err := s.Put("cosmos", rec); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if fk.nsEnsured != SystemNamespace {
		t.Errorf("Put should ensure %q, ensured %q", SystemNamespace, fk.nsEnsured)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got["cosmos"].Version != "2.102.0" || got["cosmos"].Digest != "sha256:abc" {
		t.Errorf("round-trip mismatch: %+v", got["cosmos"])
	}
}

func TestState_LoadEmptyWhenAbsent(t *testing.T) {
	got, err := State{Kube: &fakeKube{cmMissing: true}}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("absent ConfigMap should load as empty, got %+v", got)
	}
}

func TestState_Delete(t *testing.T) {
	fk := &fakeKube{}
	s := State{Kube: fk}
	if err := s.Put("cosmos", Record{Version: "2.102.0", Source: "oci:x", Digest: "sha256:abc"}); err != nil {
		t.Fatalf("setup Put: %v", err)
	}
	if err := s.Put("other", Record{Version: "1.0.0", Source: "oci:y", Digest: "sha256:def"}); err != nil {
		t.Fatalf("setup Put: %v", err)
	}
	if err := s.Delete("cosmos"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.Load()
	if _, ok := got["cosmos"]; ok {
		t.Error("cosmos should be gone after Delete")
	}
	if _, ok := got["other"]; !ok {
		t.Error("Delete must not drop other records")
	}
}

func TestState_InstalledPackages(t *testing.T) {
	fk := &fakeKube{packagesJSON: []byte(`{"items":[{"metadata":{"name":"cosmos"}},{"metadata":{"name":"keycloak"}}]}`)}
	got, err := State{Kube: fk}.InstalledPackages()
	if err != nil {
		t.Fatalf("InstalledPackages: %v", err)
	}
	if !got["cosmos"] || !got["keycloak"] {
		t.Errorf("expected cosmos+keycloak, got %+v", got)
	}
}

func TestState_InstalledPackagesError(t *testing.T) {
	_, err := State{Kube: &fakeKube{listErr: errors.New("no cluster")}}.InstalledPackages()
	if err == nil {
		t.Error("InstalledPackages should surface a list error")
	}
}

// TestMarshalUnmarshalRoundTrip verifies that marshalRecords/unmarshalRecords
// are pure inverses of each other on all Record fields.
func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	in := map[string]Record{
		"cosmos": {
			Version:     "2.102.0",
			Source:      "oci:ghcr.io/jongodb-labs/bundles/cosmos",
			Digest:      "sha256:abc123",
			InstalledAt: "2026-06-26T00:00:00Z",
			InstalledBy: "maggie",
		},
		"keycloak": {
			Version:     "1.0.0",
			Source:      "oci:ghcr.io/jongodb-labs/bundles/keycloak",
			Digest:      "sha256:def456",
			InstalledAt: "2026-06-25T12:00:00Z",
			InstalledBy: "admin",
		},
	}
	data, err := marshalRecords(in)
	if err != nil {
		t.Fatalf("marshalRecords: %v", err)
	}
	// Re-encode the map as YAML bytes (the format unmarshalRecords expects).
	raw, err := yaml.Marshal(data)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	got, err := unmarshalRecords(raw)
	if err != nil {
		t.Fatalf("unmarshalRecords: %v", err)
	}
	if !reflect.DeepEqual(in, got) {
		t.Errorf("round-trip mismatch\n  want: %+v\n  got:  %+v", in, got)
	}
}

// marshalConfigMapForTest renders the fake's in-memory ConfigMap data (map of
// app name → record YAML blob) into the YAML the production GetConfigMap returns.
func marshalConfigMapForTest(data map[string]string) []byte {
	if data == nil {
		return []byte("{}")
	}
	b, err := yaml.Marshal(data)
	if err != nil {
		panic(err)
	}
	return b
}
