package catalog

import "testing"

func TestLoad_EmbeddedParses(t *testing.T) {
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Version == "" {
		t.Error("catalog version should be set")
	}
	if len(c.Layers) == 0 || len(c.Operators) == 0 {
		t.Fatal("catalog should have layers and operators")
	}
}

func TestRequiredMatchesBundle(t *testing.T) {
	c := MustLoad()
	want := map[string]bool{"init": true, "core-base": true, "pgo": true}
	got := map[string]bool{}
	for _, e := range c.Required() {
		got[e.ID] = true
	}
	for id := range want {
		if !got[id] {
			t.Errorf("expected %q to be a required entry", id)
		}
	}
}

func TestFindAndPending(t *testing.T) {
	c := MustLoad()
	minio, ok := c.Find("minio")
	if !ok {
		t.Fatal("minio should be in the catalog")
	}
	if !minio.Pending() {
		t.Error("minio should be flagged pending")
	}
	if _, ok := c.Find("does-not-exist"); ok {
		t.Error("Find should miss unknown IDs")
	}
}
