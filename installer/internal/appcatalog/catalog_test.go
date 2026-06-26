package appcatalog

import (
	"path/filepath"
	"testing"
)

func writeCatalog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := writeFile(path, body); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

const validCatalog = `apiVersion: sre/v1
apps:
  - name: cosmos
    version: "2.102.0"
    description: "COSMOS — mission app"
    source:
      type: oci
      ref: ghcr.io/jongodb-labs/bundles/cosmos
    verify:
      identityRegexp: "^https://github.com/JongoDB-Labs/cosmos-v2/"
      issuer: "https://token.actions.githubusercontent.com"
    requires: [postgres]
`

func TestLoad_ValidCatalog(t *testing.T) {
	c, err := Load(writeCatalog(t, validCatalog))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.APIVersion != "sre/v1" {
		t.Errorf("apiVersion = %q, want sre/v1", c.APIVersion)
	}
	got, ok := c.Find("cosmos")
	if !ok {
		t.Fatal("cosmos should be in the catalog")
	}
	if got.Source.Type != SourceOCI || got.Source.Ref == "" {
		t.Errorf("cosmos source = %+v, want oci with a ref", got.Source)
	}
	if got.Verify.IdentityRegexp == "" || got.Verify.Issuer == "" {
		t.Errorf("cosmos verify should be populated, got %+v", got.Verify)
	}
	if _, ok := c.Find("nope"); ok {
		t.Error("Find should miss unknown names")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("Load should error on a missing file")
	}
}

func TestValidate_RejectsBadEntries(t *testing.T) {
	const bad = `apiVersion: sre/v1
apps:
  - name: ""
    source:
      type: smoke-signals
  - name: cosmos
    version: "2.102.0"
    source:
      type: oci
      ref: ""
`
	if _, err := Load(writeCatalog(t, bad)); err == nil {
		t.Error("Load should reject empty name, unknown source type, and empty oci ref")
	}
}
