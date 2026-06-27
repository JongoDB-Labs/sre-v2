package monitor

import (
	"reflect"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
)

const packagesJSON = `{"items":[
 {"metadata":{"name":"cosmos","namespace":"cosmos"},"status":{"phase":"Ready","endpoints":["cosmos.uds.dev"]}},
 {"metadata":{"name":"authservice","namespace":"authservice"},"status":{"phase":"Ready","endpoints":[]}},
 {"metadata":{"name":"keycloak","namespace":"keycloak"},"status":{"phase":"Pending","endpoints":["sso.uds.dev","keycloak.admin.uds.dev"]}}
]}`

func TestBuildPackageRows(t *testing.T) {
	got, err := buildPackageRows([]byte(packagesJSON))
	if err != nil {
		t.Fatalf("buildPackageRows: %v", err)
	}
	want := []PackageRow{
		{Namespace: "authservice", Name: "authservice", Phase: "Ready", Endpoints: 0},
		{Namespace: "cosmos", Name: "cosmos", Phase: "Ready", Endpoints: 1},
		{Namespace: "keycloak", Name: "keycloak", Phase: "Pending", Endpoints: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

func TestBuildPackageRows_Empty(t *testing.T) {
	got, err := buildPackageRows([]byte(`{"items":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 rows, got %d", len(got))
	}
}

func TestBuildPackageRows_Malformed(t *testing.T) {
	if _, err := buildPackageRows([]byte(`not json`)); err == nil {
		t.Fatal("expected an error for malformed JSON")
	}
}

func TestBuildAppRows(t *testing.T) {
	recs := map[string]appcatalog.Record{
		"cosmos":  {Version: "2.102.0", Source: "oci:ghcr.io/jongodb-labs/bundles/cosmos"},
		"orphan":  {Version: "0.1.0", Source: "oci:example/orphan"},
	}
	live := map[string]bool{"cosmos": true} // orphan recorded but not live (drift)
	got := buildAppRows(recs, live)
	want := []AppRow{
		{Name: "cosmos", Version: "2.102.0", Source: "oci:ghcr.io/jongodb-labs/bundles/cosmos", Live: true},
		{Name: "orphan", Version: "0.1.0", Source: "oci:example/orphan", Live: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}
