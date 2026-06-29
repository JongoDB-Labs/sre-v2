package data

import (
	"encoding/json"
	"strings"
	"testing"
)

const srcCluster = `{
 "apiVersion":"postgres-operator.crunchydata.com/v1","kind":"PostgresCluster",
 "metadata":{"name":"cosmos-pg","namespace":"cosmos","resourceVersion":"12345","uid":"abc-123",
   "creationTimestamp":"2026-06-26T02:00:00Z","generation":7,"managedFields":[{"manager":"pgo"}],
   "annotations":{"postgres-operator.crunchydata.com/pgbackrest-backup":"2026-06-29T12:00:00Z"}},
 "spec":{"postgresVersion":16,
   "instances":[{"name":"instance1","replicas":1,"dataVolumeClaimSpec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"5Gi"}}}}],
   "users":[{"name":"cosmos","databases":["cosmos"],"options":"SUPERUSER"}],
   "backups":{"pgbackrest":{
     "repos":[{"name":"repo1","volume":{"volumeClaimSpec":{"accessModes":["ReadWriteOnce"],"resources":{"requests":{"storage":"5Gi"}}}}}],
     "manual":{"repoName":"repo1"},
     "restore":{"enabled":true,"repoName":"repo1"}}},
   "dataSource":{"postgresCluster":{"clusterName":"old-thing","repoName":"repo1"}}},
 "status":{"instances":[{"name":"instance1","readyReplicas":1}]}}`

func TestCloneManifest(t *testing.T) {
	out, err := cloneManifest([]byte(srcCluster), "cosmos-pg-restore", nil)
	if err != nil {
		t.Fatalf("cloneManifest err: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("output not json: %v", err)
	}
	meta := m["metadata"].(map[string]any)
	if meta["name"] != "cosmos-pg-restore" || meta["namespace"] != "cosmos" {
		t.Fatalf("metadata name/namespace wrong: %+v", meta)
	}
	// runtime metadata + status stripped
	for _, k := range []string{"resourceVersion", "uid", "creationTimestamp", "generation", "managedFields", "annotations"} {
		if _, ok := meta[k]; ok {
			t.Fatalf("metadata.%s should be stripped", k)
		}
	}
	if _, ok := m["status"]; ok {
		t.Fatalf("status should be stripped")
	}
	spec := m["spec"].(map[string]any)
	// dataSource points at the SOURCE cluster + its repo
	ds := spec["dataSource"].(map[string]any)["postgresCluster"].(map[string]any)
	if ds["clusterName"] != "cosmos-pg" || ds["repoName"] != "repo1" {
		t.Fatalf("dataSource wrong (must point at source + repo1): %+v", ds)
	}
	if _, ok := ds["options"]; ok {
		t.Fatalf("options must be omitted when none given (got %v)", ds["options"])
	}
	// in-place restore + manual stripped from the clone; source spec otherwise inherited
	pgb := spec["backups"].(map[string]any)["pgbackrest"].(map[string]any)
	if _, ok := pgb["restore"]; ok {
		t.Fatalf("spec.backups.pgbackrest.restore must be stripped (no in-place restore on a clone)")
	}
	if _, ok := pgb["manual"]; ok {
		t.Fatalf("spec.backups.pgbackrest.manual should be stripped")
	}
	if spec["postgresVersion"].(float64) != 16 {
		t.Fatalf("postgresVersion not inherited: %v", spec["postgresVersion"])
	}
}

func TestCloneManifestPITROptions(t *testing.T) {
	out, err := cloneManifest([]byte(srcCluster), "c2", []string{"--type=time", "--target=2026-06-29 12:00:00"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(out), "--type=time") {
		t.Fatalf("PITR options not threaded into the manifest")
	}
}

func TestCloneManifestNoRepo(t *testing.T) {
	if _, err := cloneManifest([]byte(`{"metadata":{"name":"x","namespace":"y"},"spec":{"backups":{"pgbackrest":{"repos":[]}}}}`), "z", nil); err == nil {
		t.Fatalf("expected error when the source has no pgBackRest repo")
	}
}
