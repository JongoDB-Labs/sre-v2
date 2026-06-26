// Command srectl is the Security-Onion-style installer for the SRE substrate.
// It stands up the platform (round 1) and re-entrantly reconfigures it (Day-2):
// a bubbletea TUI for humans plus full cobra CLI parity for headless/airgap use.
//
// Subcommands:
//
//	srectl preflight   run host readiness checks and print a table
//	srectl install     launch the wizard (or replay answers.yaml) and render config
//	srectl version     print version information
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newRootCmd builds the cobra root command and wires every subcommand.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "srectl",
		Short: "Installer for the SRE substrate (UDS Core + data operators)",
		Long: "srectl stands up and reconfigures the Secure Runtime Environment (SRE) " +
			"substrate.\nIt runs host preflight, captures install answers, and renders the " +
			"UDS bundle\nconfig + Helm overlay that drive a UDS deploy.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newPreflightCmd(),
		newInstallCmd(),
		newAppCmd(),
		newVersionCmd(),
	)
	return root
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
