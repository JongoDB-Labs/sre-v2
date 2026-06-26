package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is the srectl build version. It is overridable at build time via
//
//	-ldflags "-X main.version=v1.2.3".
var version = "0.0.0-dev"

// newVersionCmd builds the `srectl version` command.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the srectl version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "srectl %s\n", version)
			return nil
		},
	}
}
