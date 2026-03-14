package git

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// fleetResourcePrefixes lists the directory prefixes for fleet-managed resources.
var fleetResourcePrefixes = []string{"policies/", "queries/", "software/", "profiles/", "scripts/"}

// Scope describes which parts of the repo are affected by a set of changed files.
type Scope struct {
	// IncludeGlobal is true when base.yml, an environment overlay, or labels/ changed.
	IncludeGlobal bool
	// Teams is the deduplicated list of affected team names.
	Teams []string
	// ChangedFiles is the filtered subset of files relevant to fleet-plan.
	ChangedFiles []string
}

// ResolveScope inspects changedFiles against the repo at root and returns the
// affected teams and whether global config is affected.
// envFile is the path to the environment overlay (e.g. "environments/nv.yml").
func ResolveScope(root string, changedFiles []string, envFile string) Scope {
	teamsSeen := map[string]bool{}
	var scope Scope

	for _, f := range changedFiles {
		cleaned := filepath.Clean(f)
		if strings.HasPrefix(cleaned, "..") || filepath.IsAbs(cleaned) {
			continue
		}
		switch {
		case f == "base.yml", f == envFile, strings.HasPrefix(f, "labels/"):
			scope.IncludeGlobal = true

		case strings.HasPrefix(f, "teams/") && (strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml")):
			name := readTeamName(filepath.Join(root, f))
			if name != "" && !teamsSeen[name] {
				teamsSeen[name] = true
				scope.Teams = append(scope.Teams, name)
			}

		case isFleetResource(f):
			patterns := buildSearchPatterns(root, f)
			for _, name := range teamsReferencingAny(root, patterns) {
				if !teamsSeen[name] {
					teamsSeen[name] = true
					scope.Teams = append(scope.Teams, name)
				}
			}
		}

		if isFleetResourceOrTeam(f) {
			scope.ChangedFiles = append(scope.ChangedFiles, f)
		}
	}

	return scope
}

// isFleetResource returns true for files under the fleet-managed resource dirs.
func isFleetResource(f string) bool {
	for _, prefix := range fleetResourcePrefixes {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	return false
}

func isFleetResourceOrTeam(f string) bool {
	return isFleetResource(f) || (strings.HasPrefix(f, "teams/") && (strings.HasSuffix(f, ".yml") || strings.HasSuffix(f, ".yaml")))
}

// buildSearchPatterns returns the set of path strings to grep for in team YAMLs.
// For non-YAML files (e.g. install scripts), also includes sibling YAML files.
func buildSearchPatterns(root, f string) []string {
	patterns := []string{"../" + f, f}

	if !strings.HasSuffix(f, ".yml") && !strings.HasSuffix(f, ".yaml") {
		dir := filepath.Dir(f)
		for _, ext := range []string{"*.yml", "*.yaml"} {
			matches, _ := filepath.Glob(filepath.Join(root, dir, ext))
			for _, m := range matches {
				rel, err := filepath.Rel(root, m)
				if err == nil {
					patterns = append(patterns, "../"+rel, rel)
				}
			}
		}
	}
	return patterns
}

// teamsReferencingAny reads teams/*.yml and returns team names whose file
// content contains any of the given patterns (plain string search).
func teamsReferencingAny(root string, patterns []string) []string {
	ymlMatches, _ := filepath.Glob(filepath.Join(root, "teams", "*.yml"))
	yamlMatches, _ := filepath.Glob(filepath.Join(root, "teams", "*.yaml"))
	matches := append(ymlMatches, yamlMatches...)
	seen := map[string]bool{}
	var names []string
	for _, teamFile := range matches {
		content, err := os.ReadFile(teamFile)
		if err != nil {
			continue
		}
		for _, pat := range patterns {
			if strings.Contains(string(content), pat) {
				name := readTeamName(teamFile)
				if name != "" && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
				break
			}
		}
	}
	return names
}

// readTeamName extracts the name field from a team YAML file.
func readTeamName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var t struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &t); err != nil {
		return ""
	}
	return t.Name
}
