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

func TestFindAndNotPending(t *testing.T) {
	c := MustLoad()
	minio, ok := c.Find("minio")
	if !ok {
		t.Fatal("minio should be in the catalog")
	}
	// packages/minio ships in bundle/uds-bundle.yaml today — it is no longer an
	// undecided entry (the Gitea acceptance run exercised it live; see
	// docs/platform-runbook.md's gotcha catalog and docs/app-onboarding.md's
	// worked example #2). The stale `status: pending` drift is fixed in
	// catalog.yaml; assert it stays fixed.
	if minio.Pending() {
		t.Error("minio should no longer be flagged pending — it ships in the bundle")
	}
	if _, ok := c.Find("does-not-exist"); ok {
		t.Error("Find should miss unknown IDs")
	}
}

func TestEntry_Pending(t *testing.T) {
	// Exercise the Pending() predicate directly (synthetic entries), since no
	// embedded catalog entry currently carries status: pending.
	if (Entry{Status: StatusReady}).Pending() {
		t.Error("StatusReady (empty) should not be pending")
	}
	if !(Entry{Status: StatusPending}).Pending() {
		t.Error("StatusPending should be pending")
	}
}
