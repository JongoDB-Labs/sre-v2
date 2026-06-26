package render

import (
	"strings"
	"testing"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
)

func TestFlavorFor(t *testing.T) {
	if got := FlavorFor(config.PostureBaseline); got != FlavorUpstream {
		t.Errorf("Baseline: want upstream, got %s", got)
	}
	if got := FlavorFor(config.PostureDoD); got != FlavorRegistry1 {
		t.Errorf("DoD: want registry1, got %s", got)
	}
}

func TestProfileFor_DoDHardens(t *testing.T) {
	p := ProfileFor(config.PostureDoD)
	if !p.FIPS {
		t.Error("DoD profile should enable FIPS")
	}
	if p.AuditRetentionDays < 1095 {
		t.Errorf("DoD retention floor should be >=1095, got %d", p.AuditRetentionDays)
	}
}

func TestResourcesFor_ScalesUp(t *testing.T) {
	small := ResourcesFor(config.SizingSmall)
	large := ResourcesFor(config.SizingLarge)
	if small.Replicas >= large.Replicas {
		t.Errorf("large replicas (%d) should exceed small (%d)", large.Replicas, small.Replicas)
	}
	if large.PGInstances < 3 {
		t.Errorf("large should run an HA Postgres (>=3 instances), got %d", large.PGInstances)
	}
}

func TestRender_RequiredPackagesAlwaysIncluded(t *testing.T) {
	cat := catalog.MustLoad()
	a := config.Default()
	a.Services = nil // select no optional services

	files, err := Render(a, cat)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	udsCfg := files[0].Content
	for _, id := range []string{"init", "core-base", "pgo"} {
		if !strings.Contains(udsCfg, id) {
			t.Errorf("required package %q missing from uds-config.yaml:\n%s", id, udsCfg)
		}
	}
}

func TestRender_DoDMediumMappings(t *testing.T) {
	cat := catalog.MustLoad()
	a := config.Answers{
		Posture: config.PostureDoD,
		Sizing:  config.SizingMedium,
		SSO:     config.SSONone,
		Domain:  "sre.example.mil",
		Secrets: config.SecretsExternal,
	}
	files, err := Render(a, cat)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	uds, overlay := files[0].Content, files[1].Content
	if !strings.Contains(uds, "flavor: registry1") {
		t.Errorf("DoD should map to registry1 flavor:\n%s", uds)
	}
	if !strings.Contains(overlay, "fips: true") {
		t.Errorf("DoD should set fips: true:\n%s", overlay)
	}
}

func TestRender_InvalidAnswersError(t *testing.T) {
	cat := catalog.MustLoad()
	if _, err := Render(config.Answers{}, cat); err == nil {
		t.Error("expected error rendering empty/invalid answers")
	}
}
