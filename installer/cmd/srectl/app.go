package main

import (
	"fmt"
	"io"
	"log"
	"os/user"
	"text/tabwriter"
	"time"

	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog"
	"github.com/JongoDB-Labs/sre-v2/installer/internal/appcatalog/source"
	"github.com/spf13/cobra"
)

// defaultCatalogPath is the shipped catalog location (repo-root catalog.yaml).
const defaultCatalogPath = "catalog.yaml"

// appDeps bundles the appcatalog collaborators a command run needs. Production
// builds it from the real exec wrappers (newAppDeps); tests inject fakes so the
// commands are exercised without a cluster, registry, or external binaries.
type appDeps struct {
	// Cat is the loaded catalog.
	Cat *appcatalog.Catalog
	// Cosign verifies signatures (fail-closed).
	Cosign appcatalog.Cosign
	// UDS deploys/removes packages.
	UDS appcatalog.UDS
	// Inspect supplies zarf inspect output to preflight.
	Inspect appcatalog.Inspector
	// State reads/writes the install record + lists live packages.
	State appcatalog.State
	// Zarf resolves OCI refs to digests (the source.OCI adapter's dependency).
	Zarf source.Zarf
	// Now returns the install timestamp (RFC3339); injectable for tests.
	Now func() string
	// Actor is who performed the action (kubeconfig user for the CLI).
	Actor string
	// AllowUnsigned permits installing a local directory source without a cosign
	// signature (dev opt-out). It MUST NOT weaken verification of signed sources.
	AllowUnsigned bool
}

// newAppDeps builds production dependencies from the real wrappers.
func newAppDeps(catalogPath string) (appDeps, error) {
	cat, err := appcatalog.Load(catalogPath)
	if err != nil {
		return appDeps{}, err
	}
	z := source.NewZarf()
	return appDeps{
		Cat:     cat,
		Cosign:  appcatalog.NewCosign(),
		UDS:     appcatalog.NewUDS(),
		Inspect: z,
		State:   appcatalog.State{Kube: appcatalog.NewKube()},
		Zarf:    z,
		Now:     func() string { return time.Now().UTC().Format(time.RFC3339) },
		Actor:   currentActor(),
	}, nil
}

// currentActor returns the local username as a best-effort actor label for the
// CLI (the serve API will substitute the OIDC subject — deferred).
func currentActor() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "unknown"
}

// adapterFor selects the source adapter for an entry. local and oci are MVP;
// github is deferred (spec §12) and returns a clear error.
func adapterFor(e appcatalog.Entry, z source.Zarf) (source.Adapter, error) {
	switch e.Source.Type {
	case appcatalog.SourceLocal:
		return source.Local{}, nil
	case appcatalog.SourceOCI:
		return source.OCI{Zarf: z}, nil
	case appcatalog.SourceGitHub:
		return nil, fmt.Errorf("source type %q is deferred (connected-only github adapter not in MVP)", e.Source.Type)
	default:
		return nil, fmt.Errorf("unknown source type %q", e.Source.Type)
	}
}

// newAppCmd builds the `srectl app` parent command and its subcommands.
func newAppCmd() *cobra.Command {
	var catalogPath string
	var installedOnly bool

	cmd := &cobra.Command{
		Use:   "app",
		Short: "Deploy and manage mission apps on the running substrate",
		Long: "app deploys signed mission-app bundles from the catalog onto a running SRE\n" +
			"(verify → preflight → uds deploy → record) and manages them Day-2.",
	}
	cmd.PersistentFlags().StringVar(&catalogPath, "catalog", defaultCatalogPath, "path to catalog.yaml")

	list := &cobra.Command{
		Use:   "list",
		Short: "List catalog apps (and, with --installed, what is deployed)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppList(c.OutOrStdout(), d, installedOnly)
		},
	}
	list.Flags().BoolVar(&installedOnly, "installed", false, "cross-check the install record against live UDS Packages")

	var allowUnsigned bool
	install := &cobra.Command{
		Use:   "install <name>",
		Short: "Verify and deploy an app from the catalog",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			d.AllowUnsigned = allowUnsigned
			return runAppInstall(c.OutOrStdout(), d, args[0])
		},
	}
	install.Flags().BoolVar(&allowUnsigned, "allow-unsigned", false,
		"skip cosign verification for local directory sources only (dev opt-out; never weakens signed-source verification)")

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a deployed app and prune its record",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppRemove(c.OutOrStdout(), d, args[0])
		},
	}

	status := &cobra.Command{
		Use:   "status <name>",
		Short: "Show an app's install record and live presence",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := newAppDeps(catalogPath)
			if err != nil {
				return err
			}
			return runAppStatus(c.OutOrStdout(), d, args[0])
		},
	}

	cmd.AddCommand(list, install, remove, status)
	return cmd
}

// runAppList prints the catalog; with installedOnly it cross-checks the record
// against live UDS Packages and flags drift (spec §6).
func runAppList(out io.Writer, d appDeps, installedOnly bool) error {
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	if !installedOnly {
		fmt.Fprintln(tw, "NAME\tVERSION\tSOURCE\tDESCRIPTION")
		for _, e := range d.Cat.Apps {
			fmt.Fprintf(tw, "%s\t%s\t%s:%s\t%s\n", e.Name, e.Version, e.Source.Type, e.Source.Ref, e.Description)
		}
		return tw.Flush()
	}

	recs, err := d.State.Load()
	if err != nil {
		return err
	}
	live, err := d.State.InstalledPackages()
	if err != nil {
		return err
	}
	fmt.Fprintln(tw, "NAME\tRECORD\tLIVE\tNOTE")
	seen := map[string]bool{}
	for name, r := range recs {
		seen[name] = true
		note := ""
		if !live[name] {
			note = "drift: recorded but no live Package"
		}
		fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", name, r.Version, live[name], note)
	}
	for name := range live {
		if !seen[name] {
			fmt.Fprintf(tw, "%s\t%s\t%t\t%s\n", name, "-", true, "drift: live Package without a record")
		}
	}
	return tw.Flush()
}

// runAppInstall executes the deploy flow (spec §5): resolve → verify (fail-closed)
// → advisory preflight → deploy (best-effort rollback) → record.
//
// Verify policy (decision #2):
//   - oci / local tarball with a real digest: always cosign-verified fail-closed.
//   - local directory (empty digest): rejected unless --allow-unsigned is set.
//   - --allow-unsigned ONLY skips verification for local dir sources; it never
//     weakens verification of signed sources.
func runAppInstall(out io.Writer, d appDeps, name string) error {
	e, ok := d.Cat.Find(name)
	if !ok {
		return fmt.Errorf("app %q is not in the catalog", name)
	}

	adapter, err := adapterFor(e, d.Zarf)
	if err != nil {
		return err
	}
	ref, digest, err := adapter.Resolve(e)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "resolved %s → %s\n", e.Name, ref)

	// Verify gate: fail-closed for all signed sources; local dir requires
	// --allow-unsigned to proceed without a digest.
	if digest == "" && e.Source.Type == appcatalog.SourceLocal {
		// Local directory: no content-addressable digest; cosign cannot verify.
		if !d.AllowUnsigned {
			return fmt.Errorf("verify: %s is a local directory source with no digest; pass --allow-unsigned to deploy (dev opt-out only)", e.Name)
		}
		fmt.Fprintln(out, "warning: skipping signature verification for local directory source (--allow-unsigned)")
	} else {
		// All other sources (oci, local tarball): cosign verify fail-closed.
		if err := appcatalog.CheckSignature(d.Cosign, e, ref); err != nil {
			return err
		}
		fmt.Fprintf(out, "signature verified (identity %q)\n", e.Verify.IdentityRegexp)
	}

	// Preflight: advisory — log errors but never abort the install (spec §5.3).
	live, err := d.State.InstalledPackages()
	if err != nil {
		// An unreachable cluster here is a real problem for the deploy that
		// follows; surface it rather than proceeding blind.
		return err
	}
	warns, prefErr := appcatalog.Preflight(d.Inspect, e, ref, live)
	if prefErr != nil {
		// Advisory contract: log the I/O failure but do NOT abort.
		log.Printf("preflight: %v (advisory; proceeding with deploy)", prefErr)
	}
	for _, w := range warns {
		fmt.Fprintf(out, "warning [%s]: %s\n", w.Code, w.Message)
	}

	if err := appcatalog.Deploy(d.UDS, ref, e.Name); err != nil {
		return err
	}
	fmt.Fprintf(out, "deployed %s\n", e.Name)

	rec := appcatalog.Record{
		Version:     e.Version,
		Source:      string(e.Source.Type) + ":" + e.Source.Ref,
		Digest:      digest,
		InstalledAt: d.Now(),
		InstalledBy: d.Actor,
	}
	if err := d.State.Put(e.Name, rec); err != nil {
		return err
	}
	fmt.Fprintf(out, "recorded install of %s %s\n", e.Name, e.Version)
	return nil
}

// runAppRemove removes the app and prunes its record (spec §7).
func runAppRemove(out io.Writer, d appDeps, name string) error {
	if err := appcatalog.Remove(d.UDS, name); err != nil {
		return err
	}
	if err := d.State.Delete(name); err != nil {
		return err
	}
	fmt.Fprintf(out, "removed %s and pruned its record\n", name)
	return nil
}

// runAppStatus reports an app's recorded metadata and whether a live Package
// exists (spec §6 — record is convenience, cluster is truth).
func runAppStatus(out io.Writer, d appDeps, name string) error {
	recs, err := d.State.Load()
	if err != nil {
		return err
	}
	live, err := d.State.InstalledPackages()
	if err != nil {
		return err
	}
	rec, recorded := recs[name]
	state := "not installed"
	if recorded {
		state = "installed"
	}
	fmt.Fprintf(out, "%s: %s\n", name, state)
	if recorded {
		fmt.Fprintf(out, "  version:     %s\n", rec.Version)
		fmt.Fprintf(out, "  source:      %s\n", rec.Source)
		fmt.Fprintf(out, "  digest:      %s\n", rec.Digest)
		fmt.Fprintf(out, "  installedAt: %s\n", rec.InstalledAt)
		fmt.Fprintf(out, "  installedBy: %s\n", rec.InstalledBy)
	}
	fmt.Fprintf(out, "  live UDS Package: %t\n", live[name])
	if recorded && !live[name] {
		fmt.Fprintln(out, "  note: drift — recorded but no live Package")
	}
	if !recorded && live[name] {
		fmt.Fprintln(out, "  note: drift — live Package without a record")
	}
	return nil
}
