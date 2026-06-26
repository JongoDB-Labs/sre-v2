package appcatalog

import (
	"path/filepath"
	"testing"
)

// shippedCatalogPath locates the repo-root catalog.yaml relative to this test
// file (installer/internal/appcatalog/ → ../../../catalog.yaml).
func shippedCatalogPath() string {
	return filepath.Join("..", "..", "..", "catalog.yaml")
}

func TestShippedCatalog_CosmosIsEntryOne(t *testing.T) {
	c, err := Load(shippedCatalogPath())
	if err != nil {
		t.Fatalf("the shipped catalog.yaml must load + validate: %v", err)
	}
	if len(c.Apps) == 0 || c.Apps[0].Name != "cosmos" {
		t.Fatalf("cosmos must be catalog entry #1, got %+v", c.Apps)
	}
	cosmos := c.Apps[0]
	if cosmos.Source.Type != SourceOCI || cosmos.Source.Ref == "" {
		t.Errorf("cosmos must declare an oci source with a ref, got %+v", cosmos.Source)
	}
	if cosmos.Verify.IdentityRegexp == "" || cosmos.Verify.Issuer == "" {
		t.Errorf("cosmos must declare an expected signer identity (fail-closed), got %+v", cosmos.Verify)
	}
	if len(cosmos.Requires) != 1 || cosmos.Requires[0] != "pgo" {
		t.Errorf("cosmos must require the substrate service-id pgo (decision #5), got %v", cosmos.Requires)
	}
}
