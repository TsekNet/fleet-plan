// fleet-plan: terraform plan, but for your device fleet.
//
// A strictly read-only CLI that diffs proposed Fleet GitOps YAML against
// current Fleet state. It never mutates Fleet. GET requests only.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/config"
	"github.com/TsekNet/fleet-plan/internal/diff"
	"github.com/TsekNet/fleet-plan/internal/envmerge"
	"github.com/TsekNet/fleet-plan/internal/gitci"
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
	flagFormat           string
	flagNoColor          bool
	flagVerbose          bool
	flagTeams            []string
	flagHeading          string
	flagDetailedExitCode bool

	// --git mode flags.
	flagGit  bool
	flagBase string
	flagEnv  string
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
	pf.StringVar(&flagHeading, "heading", "", "## heading for markdown output")
	pf.BoolVar(&flagDetailedExitCode, "detailed-exitcode", false, "exit 2 when changes detected (0=no changes, 1=error, 2=changes)")

	// --git mode.
	pf.BoolVar(&flagGit, "git", false, "enable CI mode: auto-detect changed files, infer affected teams, post MR/PR comment")
	pf.StringVar(&flagBase, "base", "", "path to base.yml for multi-env config merge (use with --env)")
	pf.StringVar(&flagEnv, "env", "", "path to environment overlay YAML, merged with --base in-memory")

	root.AddCommand(versionCmd())

	return root
}

func runDiff(cmd *cobra.Command, _ []string) error {
	start := time.Now()

	auth, err := config.ResolveAuth(flagURL, flagToken, flagRepo)
	if err != nil {
		return err
	}

	info, err := os.Stat(flagRepo)
	if err != nil {
		return fmt.Errorf("repo path %q does not exist: %w", flagRepo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path %q is not a directory", flagRepo)
	}

	// Resolve the default.yml path: merge base+env if provided, else auto-detect.
	defaultFile, cleanup, err := resolveDefaultFile(flagRepo, flagBase, flagEnv)
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// --git: detect CI context, resolve changed files + affected teams.
	var changedFiles []string
	var ci gitci.Env
	teams := flagTeams

	if flagGit {
		ci = gitci.Detect()
		resolved, skip := resolveCIScope(ci, flagRepo, flagEnv, &defaultFile, teams)
		if skip {
			return nil
		}
		changedFiles = resolved.ChangedFiles
		if len(resolved.Teams) > 0 && len(teams) == 0 {
			teams = resolved.Teams
		}
	}

	repo, err := parser.ParseRepo(flagRepo, teams, defaultFile)
	if err != nil {
		return fmt.Errorf("parsing repo: %w", err)
	}

	if len(repo.Teams) == 0 && len(repo.Errors) == 0 {
		if len(teams) > 0 {
			return fmt.Errorf("no teams matching %v found in %s/teams/", teams, flagRepo)
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

	results := diff.Diff(state, repo, teams, changedFiles,
		diff.WithScriptEnricher(client))
	elapsed := time.Since(start)

	hasChanges := output.HasChanges(results)

	const marker = "fleet-plan-marker"

	switch flagFormat {
	case "json":
		out, err := output.RenderDiffJSON(results)
		if err != nil {
			return err
		}
		fmt.Println(out)
	case "markdown":
		heading := flagHeading
		if heading == "" && flagGit {
			heading = buildHeading(auth.URL)
		}

		mdBody := output.RenderDiffMarkdown(results, output.MarkdownOptions{
			Heading: heading,
			Marker:  marker,
			JobURL:  ci.JobURL(),
		})
		fmt.Println(mdBody)

		if flagGit && hasChanges && ci.Platform != gitci.PlatformUnknown {
			commentURL, err := ci.PostOrUpdateComment(mdBody, marker)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: could not post MR comment: %v\n", err)
				fmt.Fprintln(os.Stderr, "The diff is printed above.")
			} else {
				fmt.Fprintf(os.Stderr, "MR comment posted: %s\n", commentURL)
			}
		}
	default:
		fmt.Println(output.RenderDiffTerminal(results, flagVerbose))
	}

	fmt.Fprintf(os.Stderr, "Completed in %s\n", elapsed.Round(time.Millisecond))

	if flagDetailedExitCode && hasChanges {
		os.Exit(2)
	}

	return nil
}

// resolveDefaultFile returns the path to default.yml to pass to ParseRepo.
// If base+env are provided, they are merged into a temp file and a cleanup
// func is returned to delete it. If neither is provided, returns empty string
// (ParseRepo will auto-detect).
func resolveDefaultFile(repo, base, env string) (path string, cleanup func(), err error) {
	if base == "" && env == "" {
		return "", nil, nil
	}
	if base == "" || env == "" {
		return "", nil, fmt.Errorf("--base and --env must be used together")
	}

	// Resolve relative to repo root if not absolute.
	if !filepath.IsAbs(base) {
		base = filepath.Join(repo, base)
	}
	if !filepath.IsAbs(env) {
		env = filepath.Join(repo, env)
	}

	tmp, err := os.CreateTemp("", "fleet-plan-default-*.yml")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp file for merged config: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()

	if err := envmerge.MergeFiles(base, env, tmpPath); err != nil {
		os.Remove(tmpPath)
		return "", nil, fmt.Errorf("merging base+env: %w", err)
	}

	return tmpPath, func() { os.Remove(tmpPath) }, nil
}

// buildHeading returns the default CI heading using the Fleet server URL.
func buildHeading(fleetURL string) string {
	display := strings.TrimPrefix(fleetURL, "https://")
	return fmt.Sprintf("Planned changes for [%s](%s)", display, fleetURL)
}

// resolveCIScope detects the CI platform, fetches changed files, and resolves
// affected teams. Returns the scope and whether the caller should skip the diff
// (no fleet-relevant files changed). Updates defaultFile in place if global
// config is affected and no default was explicitly provided.
func resolveCIScope(ci gitci.Env, repo, envFile string, defaultFile *string, explicitTeams []string) (gitci.Scope, bool) {
	if ci.Platform == gitci.PlatformUnknown {
		fmt.Fprintln(os.Stderr, "Warning: --git specified but no CI MR/PR context detected; running full diff")
		return gitci.Scope{}, false
	}

	files, err := ci.ChangedFiles()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not determine changed files (%v); running full diff\n", err)
		return gitci.Scope{}, false
	}

	scope := gitci.ResolveScope(repo, files, envFile)
	if !scope.IncludeGlobal && len(scope.Teams) == 0 {
		fmt.Fprintln(os.Stderr, "No fleet-relevant files changed in this MR, skipping diff.")
		return scope, true
	}

	if scope.IncludeGlobal && *defaultFile == "" {
		candidate := filepath.Join(repo, "default.yml")
		if _, err := os.Stat(candidate); err == nil {
			*defaultFile = candidate
		}
	}

	if len(scope.Teams) > 0 && len(explicitTeams) == 0 {
		for _, t := range scope.Teams {
			fmt.Fprintf(os.Stderr, "Affected team: %s\n", t)
		}
	}

	return scope, false
}

func main() {
	if err := buildRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
