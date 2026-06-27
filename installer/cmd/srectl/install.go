package main

import (
	"fmt"
	"io"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/catalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/config"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/render"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/tui/wizard"
	"github.com/spf13/cobra"
)

// installOptions holds the flag values for `srectl install`.
type installOptions struct {
	// out is the directory the rendered files are written to.
	out string
	// from is an answers.yaml to replay instead of prompting.
	from string
	// nonInteractive skips the TUI; requires --from.
	nonInteractive bool
	// dryRun renders the files and prints them without deploying.
	dryRun bool
}

// newInstallCmd builds the `srectl install` command.
func newInstallCmd() *cobra.Command {
	opts := &installOptions{}
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Run the install wizard and render the substrate config",
		Long: "install captures the install answers — interactively via the TUI wizard, or " +
			"by\nreplaying an answers.yaml — then renders uds-config.yaml + values.overlay.yaml.\n\n" +
			"With --dry-run it renders and prints the files without deploying. The deploy step\n" +
			"itself is currently a stub: it prints the `uds deploy` command it would run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstall(cmd.OutOrStdout(), *opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.out, "out", "./out", "directory to render config files into")
	f.StringVar(&opts.from, "from", "", "replay an answers.yaml instead of prompting")
	f.BoolVar(&opts.nonInteractive, "non-interactive", false, "skip the TUI; requires --from")
	f.BoolVar(&opts.dryRun, "dry-run", false, "render and print the files; do not deploy")
	return cmd
}

// runInstall resolves the answers (from a file or the wizard), renders the two
// config files, and either stubs the deploy or stops at dry-run.
func runInstall(out io.Writer, opts installOptions) error {
	cat, err := catalog.Load()
	if err != nil {
		return err
	}

	answers, err := resolveAnswers(out, opts, cat)
	if err != nil {
		return err
	}
	if err := answers.Validate(); err != nil {
		return err
	}

	files, err := render.Render(*answers, cat)
	if err != nil {
		return err
	}

	paths, err := render.Write(*answers, cat, opts.out)
	if err != nil {
		return err
	}

	// Echo the rendered files (the headline output of a dry-run).
	for _, f := range files {
		fmt.Fprintf(out, "# ---- %s ----\n%s\n", f.Name, f.Content)
	}
	fmt.Fprintf(out, "wrote %d file(s) to %s\n", len(paths), opts.out)

	if opts.dryRun {
		fmt.Fprintln(out, "\ndry-run: not deploying.")
		return nil
	}
	return stubDeploy(out, opts.out)
}

// resolveAnswers returns the install answers: parsed from --from, or gathered by
// the TUI wizard. --non-interactive requires --from.
func resolveAnswers(out io.Writer, opts installOptions, cat *catalog.Catalog) (*config.Answers, error) {
	if opts.from != "" {
		answers, err := config.Load(opts.from)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "loaded answers from %s\n", opts.from)
		return answers, nil
	}
	if opts.nonInteractive {
		return nil, fmt.Errorf("--non-interactive requires --from <answers.yaml>")
	}
	return wizard.Run(cat, version)
}

// stubDeploy is the placeholder for the real deploy orchestration (build order
// step 2). It prints the `uds deploy` command the renderer's output implies.
func stubDeploy(out io.Writer, outDir string) error {
	fmt.Fprintln(out, "\n[stub] deploy not yet implemented. Would run:")
	fmt.Fprintf(out, "  uds deploy <sre-bundle> --confirm --set-file config=%s/%s\n",
		outDir, render.UDSConfigFile)
	return nil
}
