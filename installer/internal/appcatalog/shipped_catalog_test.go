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

func TestShippedCatalog_GiteaIsEntryTwo(t *testing.T) {
	c, err := Load(shippedCatalogPath())
	if err != nil {
		t.Fatalf("the shipped catalog.yaml must load + validate: %v", err)
	}
	if len(c.Apps) < 2 || c.Apps[1].Name != "gitea" {
		t.Fatalf("gitea must be catalog entry #2 (cosmos stays #1), got %+v", c.Apps)
	}
	gitea := c.Apps[1]
	if gitea.Version == "" {
		t.Error("gitea must pin a version — the resolver uses it as the OCI tag")
	}
	if gitea.Source.Type != SourceOCI || gitea.Source.Ref != "ghcr.io/jongodb-labs/bundles/gitea" {
		t.Errorf("gitea source = %+v, want oci ghcr.io/jongodb-labs/bundles/gitea", gitea.Source)
	}
	// The SECOND signer identity — proves multi-identity verify is generic.
	if gitea.Verify.IdentityRegexp != "^https://github.com/JongoDB-Labs/sre-apps/" ||
		gitea.Verify.Issuer != "https://token.actions.githubusercontent.com" {
		t.Errorf("gitea verify = %+v, want the sre-apps keyless identity", gitea.Verify)
	}
	if len(gitea.Requires) != 2 || gitea.Requires[0] != "pgo" || gitea.Requires[1] != "minio" {
		t.Errorf("gitea must require [pgo minio], got %v", gitea.Requires)
	}
}
