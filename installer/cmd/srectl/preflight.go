package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/preflight"
	"github.com/spf13/cobra"
)

// newPreflightCmd builds the `srectl preflight` command.
func newPreflightCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "preflight",
		Short: "Run host readiness checks and print a table",
		Long: "preflight inspects the host (architecture, vCPU/RAM/disk, kernel, swap, " +
			"/dev/kmsg,\nand connected-vs-airgap) and prints each check's status plus a " +
			"one-line remediation.\nIt exits non-zero if any check fails.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := preflight.Run()
			printPreflightTable(cmd.OutOrStdout(), report)
			if !report.OK() {
				return fmt.Errorf("preflight failed: %d check(s) need attention", report.Fails())
			}
			return nil
		},
	}
}

// printPreflightTable renders a preflight report as an aligned table.
func printPreflightTable(out io.Writer, r preflight.Report) {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tSTATUS\tDETAIL\tREMEDIATION")
	for _, res := range r.Results {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", res.Name, res.Status, res.Detail, res.Remediation)
	}
	tw.Flush()
	fmt.Fprintf(out, "\n%d passed, %d warnings, %d failed\n", r.Passes(), r.Warns(), r.Fails())
}
