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
