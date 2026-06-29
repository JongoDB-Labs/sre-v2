package data

import (
	"encoding/json"
	"testing"
)

func TestInPlaceRestorePatch(t *testing.T) {
	var p struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			Backups struct {
				Pgbackrest struct {
					Restore struct {
						Enabled  bool     `json:"enabled"`
						RepoName string   `json:"repoName"`
						Options  []string `json:"options"`
					} `json:"restore"`
				} `json:"pgbackrest"`
			} `json:"backups"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(inPlaceRestorePatch("repo1", "STAMP", nil)), &p); err != nil {
		t.Fatalf("patch not valid json: %v", err)
	}
	if !p.Spec.Backups.Pgbackrest.Restore.Enabled {
		t.Fatal("restore.enabled must be true")
	}
	if p.Spec.Backups.Pgbackrest.Restore.RepoName != "repo1" {
		t.Fatalf("repoName wrong: %q", p.Spec.Backups.Pgbackrest.Restore.RepoName)
	}
	if p.Metadata.Annotations["postgres-operator.crunchydata.com/pgbackrest-restore"] != "STAMP" {
		t.Fatalf("restore annotation wrong: %+v", p.Metadata.Annotations)
	}
}

func TestInPlaceRestorePatch_PITROptions(t *testing.T) {
	out := inPlaceRestorePatch("repo1", "S", []string{"--type=time", "--target=2026-06-29 12:00:00"})
	if !contains(out, "--type=time") {
		t.Fatalf("PITR options not threaded: %s", out)
	}
}
