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
	flagURL     string
	flagToken   string
	flagRepo    string
	flagFormat  string
	flagNoColor bool
	flagVerbose bool
	flagTeam    string
	flagDefault string
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
	pf.StringVar(&flagTeam, "team", "", "diff only this team (default: all)")
	pf.StringVar(&flagDefault, "default", "", "path to default.yml (overrides auto-detection)")

	root.AddCommand(versionCmd())

	return root
}

func runDiff(cmd *cobra.Command, _ []string) error {
	start := time.Now()

	auth, err := config.ResolveAuth(flagURL, flagToken, flagRepo)
	if err != nil {
		return err
	}

	if info, err := os.Stat(flagRepo); err != nil {
		return fmt.Errorf("repo path %q does not exist: %w", flagRepo, err)
	} else if !info.IsDir() {
		return fmt.Errorf("repo path %q is not a directory", flagRepo)
	}

	repo, err := parser.ParseRepo(flagRepo, flagTeam, flagDefault)
	if err != nil {
		return fmt.Errorf("parsing repo: %w", err)
	}

	if len(repo.Teams) == 0 && len(repo.Errors) == 0 {
		if flagTeam != "" {
			return fmt.Errorf("no team matching %q found in %s/teams/", flagTeam, flagRepo)
		}
		return fmt.Errorf("no teams found in %s/teams/\nAre you in a fleet-gitops repo? Try --repo /path/to/repo", flagRepo)
	}

	client, err := api.NewClient(auth.URL, auth.Token)
	if err != nil {
		return err
	}
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "Fetching Fleet state from %s...\n", auth.URL)

	fetchGlobal := repo.Global != nil
	state, err := client.FetchAll(ctx, fetchGlobal)
	if err != nil {
		return err
	}

	results := diff.Diff(state, repo, flagTeam)
	elapsed := time.Since(start)

	switch flagFormat {
	case "json":
		out, err := output.RenderDiffJSON(results)
		if err != nil {
			return err
		}
		fmt.Println(out)
	case "markdown":
		fmt.Println(output.RenderDiffMarkdown(results))
	default:
		fmt.Println(output.RenderDiffTerminal(results, flagVerbose))
	}

	fmt.Fprintf(os.Stderr, "Completed in %s\n", elapsed.Round(time.Millisecond))

	return nil
}

func main() {
	if err := buildRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
