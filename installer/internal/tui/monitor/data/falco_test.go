package data

import "testing"

const falcoLines = `{"priority":"Notice","rule":"Run shell untrusted","time":"2026-06-27T00:00:12.834189113Z","output_fields":{"k8s.ns.name":"cosmos","k8s.pod.name":"cosmos-pg-instance1-m9hm-0"}}
not-json-garbage-line
{"priority":"Warning","rule":"Terminal shell in container","time":"2026-06-27T01:18:14.911096955Z","output_fields":{"k8s.ns.name":"default","k8s.pod.name":"app-xyz"}}`

func TestFalcoRows(t *testing.T) {
	got := FalcoRows([]byte(falcoLines))
	if len(got) != 2 {
		t.Fatalf("want 2 rows (junk skipped), got %d: %+v", len(got), got)
	}
	// newest-first: the Warning at 01:18 comes before the Notice at 00:00
	if got[0].Rule != "Terminal shell in container" || got[0].Priority != "Warning" {
		t.Fatalf("newest-first wrong: %+v", got[0])
	}
	if got[0].Namespace != "default" || got[0].Pod != "app-xyz" {
		t.Fatalf("k8s fields wrong: %+v", got[0])
	}
	if got[1].Rule != "Run shell untrusted" || got[1].Namespace != "cosmos" {
		t.Fatalf("row2 wrong: %+v", got[1])
	}
}

func TestLogsByLabelArgs(t *testing.T) {
	got := logsByLabelArgs("falco", "app.kubernetes.io/name=falco", "falco", 200)
	want := []string{"logs", "-n", "falco", "-l", "app.kubernetes.io/name=falco", "-c", "falco", "--tail", "200"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("logsByLabelArgs[%d]: got %q want %q (full %v)", i, got[i], want[i], got)
		}
	}
}
