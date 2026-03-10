// fleet-plan: terraform plan, but for your device fleet.
//
// A strictly read-only CLI that diffs proposed Fleet GitOps YAML against
// current Fleet state. It never mutates Fleet. GET requests only.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/config"
	"github.com/TsekNet/fleet-plan/internal/diff"
	"github.com/TsekNet/fleet-plan/internal/output"
	"github.com/TsekNet/fleet-plan/internal/parser"
)

// Set via -ldflags at build time.
var (
	version   = "dev"
	buildDate = "unknown"
	goVersion = "unknown"
)

// Flags.
var (
	flagURL              string
	flagToken            string
	flagRepo             string
	flagBaseRepo         string
	flagBaseDefault      string
	flagFormat           string
	flagNoColor          bool
	flagVerbose          bool
	flagTeams            []string
	flagDefault          string
	flagCIHeader         string
	flagCIMarker         string
	flagDetailedExitCode bool
	flagChangedFiles     []string
)

// buildRootCmd constructs the root cobra.Command with all subcommands and flags.
// Extracted from main() so tests can call it without os.Exit.
func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "fleet-plan",
		Short: "terraform plan, but for your device fleet",
		Long: `fleet-plan diffs proposed Fleet GitOps YAML against current Fleet state.
Strictly read-only -- GET requests only.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE:          runDiff,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&flagURL, "url", "", "Fleet server URL (or $FLEET_PLAN_URL)")
	pf.StringVar(&flagToken, "token", "", "API token (or $FLEET_PLAN_TOKEN)")
	pf.StringVar(&flagRepo, "repo", ".", "path to fleet-gitops repo")
	pf.StringVarP(&flagFormat, "format", "f", "terminal", "output format: terminal, json, markdown")
	pf.BoolVar(&flagNoColor, "no-color", false, "disable color output")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "show full old/new values for modified fields")
	pf.StringSliceVar(&flagTeams, "team", nil, "diff only these teams (repeatable, default: all)")
	pf.StringVar(&flagDefault, "default", "", "path to default.yml (overrides auto-detection)")
	pf.StringVar(&flagBaseRepo, "base-repo", "", "path to base branch checkout for YAML-to-YAML diff (skips API)")
	pf.StringVar(&flagBaseDefault, "base-default", "", "path to base branch default.yml (used with --base-repo)")
	pf.StringVar(&flagCIHeader, "ci-header", "", "blockquote header prepended to markdown output (CI use)")
	pf.StringVar(&flagCIMarker, "ci-marker", "", "HTML comment marker appended to markdown output for idempotent MR note updates")
	pf.BoolVar(&flagDetailedExitCode, "detailed-exitcode", false, "exit 2 when changes detected (0=no changes, 1=error, 2=changes)")
	pf.StringSliceVar(&flagChangedFiles, "changed-file", nil, "only show diffs for resources from these source files (repeatable, CI use)")

	root.AddCommand(versionCmd())

	return root
}

func runDiff(cmd *cobra.Command, _ []string) error {
	start := time.Now()

	info, err := os.Stat(flagRepo)
	if err != nil {
		return fmt.Errorf("repo path %q does not exist: %w", flagRepo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path %q is not a directory", flagRepo)
	}

	repo, err := parser.ParseRepo(flagRepo, flagTeams, flagDefault)
	if err != nil {
		return fmt.Errorf("parsing repo: %w", err)
	}

	if len(repo.Teams) == 0 && len(repo.Errors) == 0 {
		if len(flagTeams) > 0 {
			return fmt.Errorf("no teams matching %v found in %s/teams/", flagTeams, flagRepo)
		}
		return fmt.Errorf("no teams found in %s/teams/\nAre you in a fleet-gitops repo? Try --repo /path/to/repo", flagRepo)
	}

	var state *api.FleetState

	if flagBaseRepo != "" {
		baseInfo, err := os.Stat(flagBaseRepo)
		if err != nil {
			return fmt.Errorf("base-repo path %q does not exist: %w", flagBaseRepo, err)
		}
		if !baseInfo.IsDir() {
			return fmt.Errorf("base-repo path %q is not a directory", flagBaseRepo)
		}

		fmt.Fprintf(os.Stderr, "Diffing against base branch at %s...\n", flagBaseRepo)
		baseRepo, err := parser.ParseRepo(flagBaseRepo, flagTeams, flagBaseDefault)
		if err != nil {
			return fmt.Errorf("parsing base repo: %w", err)
		}
		state = diff.ParsedRepoToFleetState(baseRepo)
	} else {
		auth, err := config.ResolveAuth(flagURL, flagToken, flagRepo)
		if err != nil {
			return err
		}

		client, err := api.NewClient(auth.URL, auth.Token)
		if err != nil {
			return err
		}
		ctx := context.Background()

		fmt.Fprintf(os.Stderr, "Fetching Fleet state from %s...\n", auth.URL)

		fetchGlobal := repo.Global != nil
		state, err = client.FetchAll(ctx, fetchGlobal)
		if err != nil {
			return err
		}
	}

	results := diff.Diff(state, repo, flagTeams, flagChangedFiles)
	elapsed := time.Since(start)

	hasChanges := output.HasChanges(results)

	switch flagFormat {
	case "json":
		out, err := output.RenderDiffJSON(results)
		if err != nil {
			return err
		}
		fmt.Println(out)
	case "markdown":
		opts := output.MarkdownOptions{
			Header: flagCIHeader,
			Marker: flagCIMarker,
		}
		fmt.Println(output.RenderDiffMarkdown(results, opts))
	default:
		fmt.Println(output.RenderDiffTerminal(results, flagVerbose))
	}

	fmt.Fprintf(os.Stderr, "Completed in %s\n", elapsed.Round(time.Millisecond))

	if flagDetailedExitCode && hasChanges {
		os.Exit(2)
	}

	return nil
}

func main() {
	if err := buildRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
