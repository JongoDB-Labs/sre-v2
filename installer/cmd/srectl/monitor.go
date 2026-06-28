package main

import (
	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/monitor"
	"github.com/spf13/cobra"
)

// newMonitorCmd builds the `srectl monitor` command — the k9s-style live console.
func newMonitorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "monitor",
		Short: "Live k9s-style console of the substrate (overview, cluster resources, packages, apps)",
		Long: "monitor opens a terminal console of the running substrate: live UDS Packages,\n" +
			"cluster resources (nodes, pods, workloads, services), and the installed mission\n" +
			"apps, refreshed on an interval. Read-only.\n\n" +
			"Keys: 0 overview · 1 packages · 2 apps · 3 nodes · 4 pods · 5 workloads · 6 services · Tab cycle · : jump · j/k move · q quit.",
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			state := appcatalog.State{Kube: appcatalog.NewKube()}
			return monitor.Run(version, state)
		},
	}
}
