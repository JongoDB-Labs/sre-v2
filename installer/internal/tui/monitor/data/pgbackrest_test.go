package data

import (
	"reflect"
	"testing"
)

const pgbInfo = `[{"name":"db","status":{"code":0,"message":"ok"},"backup":[
 {"label":"20260626-025441F","type":"full","timestamp":{"start":1782442481,"stop":1782442587},"info":{"size":30955260,"repository":{"size":4105845}}},
 {"label":"20260627-010000F_20260627-013000I","type":"incr","timestamp":{"start":1782522000,"stop":1782522030},"info":{"size":31000000,"repository":{"size":120000}}}]}]`

func TestBackupRows(t *testing.T) {
	got := BackupRows([]byte(pgbInfo), "cosmos-pg")
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d: %+v", len(got), got)
	}
	// Newest-first: incr backup (1782522000) comes before full backup (1782442481)
	if got[0].Cluster != "cosmos-pg" || got[0].Label != "20260627-010000F_20260627-013000I" || got[0].Type != "incr" {
		t.Fatalf("row0 wrong (newest-first): %+v", got[0])
	}
	if got[1].Cluster != "cosmos-pg" || got[1].Label != "20260626-025441F" || got[1].Type != "full" {
		t.Fatalf("row1 wrong: %+v", got[1])
	}
	if got[1].Size != "29.5 MB" { // info.size 30955260 → "29.5 MB"
		t.Fatalf("row1 size wrong (want backup size human): %q", got[1].Size)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 512: "512 B", 1536: "1.5 KB", 30955260: "29.5 MB", 5368709120: "5.0 GB"}
	for n, want := range cases {
		if got := humanBytes(n); got != want {
			t.Fatalf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPgBackrestArgs(t *testing.T) {
	if got := pgbackrestInfoArgs("cosmos", "cosmos-pg-repo-host-0"); !reflect.DeepEqual(got,
		[]string{"exec", "-n", "cosmos", "cosmos-pg-repo-host-0", "-c", "pgbackrest", "--", "pgbackrest", "info", "--output=json"}) {
		t.Fatalf("pgbackrestInfoArgs: %v", got)
	}
	if got := triggerBackupArgs("cosmos", "cosmos-pg", "2026-06-29T11:00:00Z"); !reflect.DeepEqual(got,
		[]string{"annotate", "postgrescluster", "cosmos-pg", "-n", "cosmos", "postgres-operator.crunchydata.com/pgbackrest-backup=2026-06-29T11:00:00Z", "--overwrite"}) {
		t.Fatalf("triggerBackupArgs: %v", got)
	}
}
