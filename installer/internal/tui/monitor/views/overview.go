// Package views holds the monitor's per-screen panel/row builders — pure
// functions (cluster data → renderable text/rows) that are unit-tested; the
// tview rendering that displays them is smoke.
package views

import (
	"fmt"
	"strings"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor/widgets"
)

// Inputs is everything the OVERVIEW dashboard needs, already fetched + degraded
// (MetricsOK=false ⇒ Prometheus was unreachable; gauges/sparklines are omitted).
type Inputs struct {
	Nodes, Pods, Namespaces, Packages, FiringAlerts int
	CPUPct, MemPct                                  float64
	CPUSeries, MemSeries                            []float64
	LayerHealth                                     [3]int // ok, warn, fail
	AlertNames                                      []string
	MetricsOK                                       bool
}

// BuildOverview renders the cross-layer dashboard as one tview-markup string.
func BuildOverview(in Inputs) string {
	var b strings.Builder
	// Stat tiles row.
	fmt.Fprintf(&b, "  %s    %s    %s    %s    %s\n\n",
		widgets.Tile(in.Nodes, "nodes"), widgets.Tile(in.Pods, "pods"),
		widgets.Tile(in.Namespaces, "namespaces"), widgets.Tile(in.Packages, "packages"),
		widgets.Tile(in.FiringAlerts, "alerts"))

	// Cluster CPU/MEM gauges + sparklines (or a degraded note).
	b.WriteString("  [#9FB4D8::b]Cluster[-:-:-]\n")
	if in.MetricsOK {
		fmt.Fprintf(&b, "    CPU  %s   %s\n", widgets.Bar(in.CPUPct, 24), widgets.Spark(in.CPUSeries))
		fmt.Fprintf(&b, "    MEM  %s   %s\n", widgets.Bar(in.MemPct, 24), widgets.Spark(in.MemSeries))
	} else {
		b.WriteString("    [#7C8694]metrics unavailable (Prometheus unreachable)[-]\n")
	}

	// Per-layer health rollup.
	fmt.Fprintf(&b, "\n  [#9FB4D8::b]Health[-:-:-]   %s\n",
		widgets.Health(in.LayerHealth[0], in.LayerHealth[1], in.LayerHealth[2]))

	// Firing alerts.
	b.WriteString("\n  [#9FB4D8::b]Alerts[-:-:-]\n")
	if len(in.AlertNames) == 0 {
		b.WriteString("    [#3fb950]none firing[-]\n")
	} else {
		for _, a := range in.AlertNames {
			fmt.Fprintf(&b, "    [#f85149]●[-] %s\n", a)
		}
	}
	return b.String()
}
