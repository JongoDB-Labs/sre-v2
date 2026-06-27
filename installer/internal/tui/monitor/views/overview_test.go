package views

import (
	"strings"
	"testing"
)

func TestBuildOverview_RendersPanels(t *testing.T) {
	out := BuildOverview(Inputs{
		Nodes: 1, Pods: 56, Namespaces: 22, Packages: 6, FiringAlerts: 1,
		CPUPct: 3, MemPct: 12, CPUSeries: []float64{3, 4, 3}, MemSeries: []float64{12, 12, 13},
		LayerHealth: [3]int{6, 0, 0}, AlertNames: []string{"Watchdog"}, MetricsOK: true,
	})
	for _, frag := range []string{"56", "pods", "CPU", "MEM", "3%", "12%", "✓ 6", "Watchdog"} {
		if !strings.Contains(out, frag) {
			t.Fatalf("overview missing %q\n---\n%s", frag, out)
		}
	}
}

func TestBuildOverview_MetricsDegraded(t *testing.T) {
	out := BuildOverview(Inputs{Nodes: 1, Pods: 56, MetricsOK: false})
	if !strings.Contains(out, "metrics unavailable") {
		t.Fatalf("degraded overview should note unavailable metrics:\n%s", out)
	}
}

func TestBuildOverview_CapsAlerts(t *testing.T) {
	names := []string{"A1", "A2", "A3", "A4", "A5", "A6", "A7", "A8", "A9", "A10"}
	out := BuildOverview(Inputs{AlertNames: names, FiringAlerts: len(names), MetricsOK: false})
	if !strings.Contains(out, "A8") {
		t.Fatalf("expected 8th alert name A8 in output:\n%s", out)
	}
	if strings.Contains(out, "A9") {
		t.Fatalf("9th alert name A9 should be capped out:\n%s", out)
	}
	if !strings.Contains(out, "and 2 more") {
		t.Fatalf("expected overflow line 'and 2 more' in output:\n%s", out)
	}
}
