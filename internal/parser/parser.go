// Package parser reads fleet-gitops YAML files, resolves path: references,
// and produces normalized ParsedRepo structures for diffing. The schema and
// validation rules are derived from Fleet's Go source code, not documentation.
package parser

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// repoRoot is set by ParseRepo and used by safePath to prevent path traversal.
var repoRoot string

// safePath ensures a resolved file path stays within the repo root.
// It resolves symlinks to prevent traversal via symlinked paths.
func safePath(root, resolved string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolving repo root: %w", err)
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("resolving path: %w", err)
	}
	// Resolve symlinks to get real paths. If the file doesn't exist yet,
	// EvalSymlinks will fail — fall back to the non-symlink check.
	if realResolved, err := filepath.EvalSymlinks(absResolved); err == nil {
		absResolved = realResolved
	}
	if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = realRoot
	}
	if !strings.HasPrefix(absResolved, absRoot+string(filepath.Separator)) && absResolved != absRoot {
		return fmt.Errorf("path %q escapes repository root %q", resolved, root)
	}
	return nil
}

// Valid platform identifiers (from server/fleet/policies.go and queries.go).
var ValidPlatforms = map[string]bool{
	"darwin":  true,
	"windows": true,
	"linux":   true,
	"chrome":  true,
}

// Valid logging types (from server/fleet/queries.go).
var ValidLogging = map[string]bool{
	"snapshot":                     true,
	"differential":                 true,
	"differential_ignore_removals": true,
}

// Valid top-level YAML keys (from pkg/spec/gitops.go).
var ValidTopLevelKeys = map[string]bool{
	"name":          true,
	"team_settings": true,
	"org_settings":  true,
	"agent_options": true,
	"controls":      true,
	"policies":      true,
	"queries":       true,
	"software":      true,
	"labels":        true,
}

// Valid label membership types (from server/fleet/labels.go).
var ValidLabelMembershipTypes = map[string]bool{
	"dynamic":     true,
	"manual":      true,
	"host_vitals": true,
}

// ---------- Parsed types ----------

// ParsedRepo represents the fully parsed fleet-gitops repository.
type ParsedRepo struct {
	Teams  []ParsedTeam
	Labels []ParsedLabel
	Global *ParsedGlobal // from default.yml (org_settings, agent_options, controls, policies, queries)
	Errors []ParseError
}

// ParsedGlobal holds global configuration parsed from default.yml.
type ParsedGlobal struct {
	OrgSettings  map[string]any // raw org_settings as nested map
	AgentOptions map[string]any // raw agent_options as nested map
	Controls     map[string]any // raw controls as nested map
	Policies     []ParsedPolicy
	Queries      []ParsedQuery
	SourceFile   string
}

// ParsedTeam represents a single team's configuration.
type ParsedTeam struct {
	Name       string
	Policies   []ParsedPolicy
	Queries    []ParsedQuery
	Software   ParsedSoftware
	Profiles   []ParsedProfile
	SourceFile string
}

// ParsedPolicy represents a policy from YAML.
type ParsedPolicy struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	Resolution       string   `yaml:"resolution"`
	Query            string   `yaml:"query"`
	Platform         string   `yaml:"platform"`
	Critical         bool     `yaml:"critical"`
	LabelsIncludeAny []string `yaml:"labels_include_any"`
	LabelsExcludeAny []string `yaml:"labels_exclude_any"`
	SourceFile       string   `yaml:"-"`
}

// ParsedQuery represents a query from YAML.
type ParsedQuery struct {
	Name       string `yaml:"name"`
	Query      string `yaml:"query"`
	Interval   uint   `yaml:"interval"`
	Platform   string `yaml:"platform"`
	Logging    string `yaml:"logging"`
	SourceFile string `yaml:"-"`
}

// ParsedSoftware holds all software types for a team.
type ParsedSoftware struct {
	Packages        []ParsedSoftwarePackage `yaml:"packages"`
	FleetMaintained []ParsedFleetApp        `yaml:"fleet_maintained_apps"`
	AppStoreApps    []ParsedAppStoreApp     `yaml:"app_store_apps"`
}

// ParsedSoftwarePackage represents a custom software package.
type ParsedSoftwarePackage struct {
	URL         string `yaml:"url"`
	HashSHA256  string `yaml:"hash_sha256"`
	SelfService bool   `yaml:"self_service"`
	SourceFile  string `yaml:"-"`
	RefPath     string `yaml:"-"`
}

// ParsedFleetApp represents a Fleet-maintained app.
type ParsedFleetApp struct {
	Slug        string `yaml:"slug"`
	SelfService bool   `yaml:"self_service"`
}

// ParsedAppStoreApp represents an App Store app.
type ParsedAppStoreApp struct {
	AppStoreID  string `yaml:"app_store_id"`
	SelfService bool   `yaml:"self_service"`
}

// ParsedLabel represents a label from YAML.
type ParsedLabel struct {
	Name                string `yaml:"name"`
	Description         string `yaml:"description"`
	Query               string `yaml:"query"`
	Platform            string `yaml:"platform"`
	LabelMembershipType string `yaml:"label_membership_type"`
	SourceFile          string `yaml:"-"`
}

// ParsedProfile represents an MDM profile reference.
type ParsedProfile struct {
	Path       string `yaml:"path"`
	Name       string `yaml:"-"` // extracted from file content (PayloadDisplayName, etc.)
	Platform   string `yaml:"-"` // inferred from file extension
	SourceFile string `yaml:"-"`
}

// ParseError represents a parse/validation error with file context.
type ParseError struct {
	File    string
	Line    int
	Message string
}

func (e ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", e.File, e.Line, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.File, e.Message)
}

// ---------- Team YAML raw types (for initial parsing) ----------

type rawTeamFile struct {
	Name         string           `yaml:"name"`
	TeamSettings yaml.Node        `yaml:"team_settings"`
	OrgSettings  yaml.Node        `yaml:"org_settings"`
	AgentOptions yaml.Node        `yaml:"agent_options"`
	Controls     rawControls      `yaml:"controls"`
	Policies     []rawPathRef     `yaml:"policies"`
	Queries      []rawPathRef     `yaml:"queries"`
	Software     rawSoftwareBlock `yaml:"software"`
	Labels       []rawPathRef     `yaml:"labels"`
}

type rawPathRef struct {
	Path string `yaml:"path"`
}

type rawSoftwareBlock struct {
	Packages        []rawSoftwareRef    `yaml:"packages"`
	FleetMaintained []ParsedFleetApp    `yaml:"fleet_maintained_apps"`
	AppStoreApps    []ParsedAppStoreApp `yaml:"app_store_apps"`
}

type rawSoftwareRef struct {
	Path        string `yaml:"path"`
	SelfService *bool  `yaml:"self_service"`
}

type rawControls struct {
	MacOSSettings struct {
		CustomSettings []rawProfileRef `yaml:"custom_settings"`
	} `yaml:"macos_settings"`
	WindowsSettings struct {
		CustomSettings []rawProfileRef `yaml:"custom_settings"`
	} `yaml:"windows_settings"`
}

type rawProfileRef struct {
	Path string `yaml:"path"`
}

// ---------- Parser ----------

// ParseRepo parses the fleet-gitops repository at the given root directory.
// If teamFilter is non-empty, only that team is parsed.
// If defaultFile is non-empty, it is used as the path to default.yml (the
// pre-merged global config). Otherwise, the parser looks for default.yml in
// the repo root directory.
func ParseRepo(root string, teamFilter string, defaultFile string) (*ParsedRepo, error) {
	repo := &ParsedRepo{}
	repoRoot = root

	teamsDir := filepath.Join(root, "teams")
	entries, err := os.ReadDir(teamsDir)
	if err != nil {
		if os.IsNotExist(err) {
			repo.Errors = append(repo.Errors, ParseError{
				File:    teamsDir,
				Message: "teams/ directory not found. Are you in a fleet-gitops repo?",
			})
			return repo, nil
		}
		return nil, fmt.Errorf("reading teams directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}

		teamFile := filepath.Join(teamsDir, entry.Name())
		team, errs := parseTeamFile(teamFile)
		if len(errs) > 0 {
			repo.Errors = append(repo.Errors, errs...)
		}
		if team == nil {
			continue
		}

		if teamFilter != "" && !strings.EqualFold(team.Name, teamFilter) {
			continue
		}

		repo.Teams = append(repo.Teams, *team)
	}

	// Resolve default.yml: --default flag takes priority, then default.yml in repo root.
	resolvedDefault := defaultFile
	if resolvedDefault == "" {
		resolvedDefault = filepath.Join(root, "default.yml")
	}
	if _, err := os.Stat(resolvedDefault); err == nil {
		parsed, errs := parseDefaultFile(resolvedDefault)
		repo.Errors = append(repo.Errors, errs...)
		if parsed != nil {
			repo.Global = parsed.ParsedGlobal
			repo.Labels = parsed.labels
		}
	}

	return repo, nil
}

// parseTeamFile parses a single teams/*.yml file and resolves all path: refs.
func parseTeamFile(path string) (*ParsedTeam, []ParseError) {
	var errs []ParseError

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("could not read: %s", err)}}
	}

	// Check for unknown top-level keys
	var rawMap map[string]yaml.Node
	if err := yaml.Unmarshal(data, &rawMap); err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("invalid YAML: %s", err)}}
	}
	for key := range rawMap {
		if !ValidTopLevelKeys[key] {
			errs = append(errs, ParseError{File: path, Message: fmt.Sprintf("unknown top-level key: %q", key)})
		}
	}

	var raw rawTeamFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	if raw.Name == "" {
		errs = append(errs, ParseError{File: path, Message: "missing required 'name' field"})
		return nil, errs
	}

	team := &ParsedTeam{
		Name:       raw.Name,
		SourceFile: path,
	}

	dir := filepath.Dir(path)
	seenSoftwareRefs := make(map[string]bool)

	// Resolve policies
	for _, ref := range raw.Policies {
		policies, parseErrs := resolvePolicyRef(dir, ref.Path, path)
		errs = append(errs, parseErrs...)
		team.Policies = append(team.Policies, policies...)
	}

	// Resolve queries
	for _, ref := range raw.Queries {
		queries, parseErrs := resolveQueryRef(dir, ref.Path, path)
		errs = append(errs, parseErrs...)
		team.Queries = append(team.Queries, queries...)
	}

	// Resolve software packages
	for _, ref := range raw.Software.Packages {
		pkgs, parseErrs := resolveSoftwareRef(dir, ref.Path, path)
		errs = append(errs, parseErrs...)
		for i := range pkgs {
			canonicalRef := ""
			if repoRoot != "" && pkgs[i].SourceFile != "" {
				if rel, err := filepath.Rel(repoRoot, pkgs[i].SourceFile); err == nil {
					canonicalRef = normalizeSoftwareRefPath(rel)
				}
			}
			if canonicalRef == "" {
				canonicalRef = normalizeSoftwareRefPath(ref.Path)
			}
			pkgs[i].RefPath = canonicalRef
			// Team-level package entries can override package file settings.
			// Apply explicit self_service override when present.
			if ref.SelfService != nil {
				pkgs[i].SelfService = *ref.SelfService
			}
			// Guard against duplicate package refs in the same team YAML.
			if canonicalRef != "" {
				if seenSoftwareRefs[canonicalRef] {
					errs = append(errs, ParseError{
						File:    path,
						Message: fmt.Sprintf("duplicate software package reference: %q", canonicalRef),
					})
					continue
				}
				seenSoftwareRefs[canonicalRef] = true
			}
			team.Software.Packages = append(team.Software.Packages, pkgs[i])
		}
	}
	team.Software.FleetMaintained = raw.Software.FleetMaintained
	team.Software.AppStoreApps = raw.Software.AppStoreApps

	// Resolve profile paths and extract names from file content.
	// Fleet identifies profiles by the name embedded in the file (e.g.,
	// PayloadDisplayName for .mobileconfig), NOT by the filename.
	for _, ref := range raw.Controls.MacOSSettings.CustomSettings {
		resolved := filepath.Join(dir, ref.Path)
		if repoRoot != "" {
			if err := safePath(repoRoot, resolved); err != nil {
				errs = append(errs, ParseError{File: path, Message: err.Error()})
				continue
			}
		}
		name := extractProfileName(resolved)
		if name == "" {
			name = profileNameFromFilename(resolved)
		}
		team.Profiles = append(team.Profiles, ParsedProfile{
			Path:       resolved,
			Name:       name,
			Platform:   "darwin",
			SourceFile: path,
		})
	}
	for _, ref := range raw.Controls.WindowsSettings.CustomSettings {
		resolved := filepath.Join(dir, ref.Path)
		if repoRoot != "" {
			if err := safePath(repoRoot, resolved); err != nil {
				errs = append(errs, ParseError{File: path, Message: err.Error()})
				continue
			}
		}
		name := extractProfileName(resolved)
		if name == "" {
			name = profileNameFromFilename(resolved)
		}
		team.Profiles = append(team.Profiles, ParsedProfile{
			Path:       resolved,
			Name:       name,
			Platform:   "windows",
			SourceFile: path,
		})
	}

	return team, errs
}

// readYAMLRef resolves a path: reference, validates it stays within the repo
// root, reads the file, and returns the raw bytes and resolved path. This is
// the shared core of resolvePolicyRef, resolveQueryRef, and resolveSoftwareRef.
func readYAMLRef(baseDir, refPath, parentFile, label string) (data []byte, resolved string, errs []ParseError) {
	if refPath == "" {
		return nil, "", []ParseError{{File: parentFile, Message: fmt.Sprintf("empty %spath: reference", label)}}
	}

	resolved = filepath.Join(baseDir, refPath)
	if repoRoot != "" {
		if err := safePath(repoRoot, resolved); err != nil {
			return nil, "", []ParseError{{File: parentFile, Message: err.Error()}}
		}
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, "", []ParseError{{
			File:    parentFile,
			Message: fmt.Sprintf("%spath reference %q: %s", label, refPath, err),
		}}
	}
	return data, resolved, nil
}

// resolvePolicyRef reads a policy YAML file and returns parsed policies.
func resolvePolicyRef(baseDir, refPath, parentFile string) ([]ParsedPolicy, []ParseError) {
	data, resolved, errs := readYAMLRef(baseDir, refPath, parentFile, "")
	if errs != nil {
		return nil, errs
	}

	var items []ParsedPolicy
	if err := yaml.Unmarshal(data, &items); err != nil {
		var item ParsedPolicy
		if err2 := yaml.Unmarshal(data, &item); err2 == nil {
			items = []ParsedPolicy{item}
		} else {
			return nil, []ParseError{{File: resolved, Message: fmt.Sprintf("YAML parse error: %s", err)}}
		}
	}
	for i := range items {
		items[i].SourceFile = resolved
	}
	return items, nil
}

// resolveQueryRef reads a query YAML file and returns parsed queries.
func resolveQueryRef(baseDir, refPath, parentFile string) ([]ParsedQuery, []ParseError) {
	data, resolved, errs := readYAMLRef(baseDir, refPath, parentFile, "")
	if errs != nil {
		return nil, errs
	}

	var items []ParsedQuery
	if err := yaml.Unmarshal(data, &items); err != nil {
		var item ParsedQuery
		if err2 := yaml.Unmarshal(data, &item); err2 == nil {
			items = []ParsedQuery{item}
		} else {
			return nil, []ParseError{{File: resolved, Message: fmt.Sprintf("YAML parse error: %s", err)}}
		}
	}
	for i := range items {
		items[i].SourceFile = resolved
	}
	return items, nil
}

// resolveSoftwareRef reads a software package YAML file.
func resolveSoftwareRef(baseDir, refPath, parentFile string) ([]ParsedSoftwarePackage, []ParseError) {
	data, resolved, errs := readYAMLRef(baseDir, refPath, parentFile, "software ")
	if errs != nil {
		return nil, errs
	}

	var pkg ParsedSoftwarePackage
	if err := yaml.Unmarshal(data, &pkg); err != nil {
		return nil, []ParseError{{File: resolved, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}
	pkg.SourceFile = resolved
	return []ParsedSoftwarePackage{pkg}, nil
}

// normalizeSoftwareRefPath canonicalizes teams/*.yml software package paths so
// they match Fleet API's software.packages[].referenced_yaml_path format.
// Example: "../software/mac/slack/slack.yml" -> "software/mac/slack/slack.yml"
func normalizeSoftwareRefPath(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return ""
	}
	p = path.Clean(p)
	p = strings.TrimPrefix(p, "./")
	for strings.HasPrefix(p, "../") {
		p = strings.TrimPrefix(p, "../")
	}
	p = strings.TrimPrefix(p, "/")
	return p
}

// parsedDefault is the internal result of parsing default.yml. The labels field
// is unexported because it gets moved to ParsedRepo.Labels by the caller.
type parsedDefault struct {
	*ParsedGlobal
	labels []ParsedLabel
}

// parseDefaultFile parses default.yml (the pre-merged global config) and extracts
// labels, global policies, global queries, org_settings, agent_options, and controls.
func parseDefaultFile(path string) (*parsedDefault, []ParseError) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("could not read: %s", err)}}
	}

	// Parse as generic map first for org_settings, agent_options, controls
	var rawMap map[string]any
	if err := yaml.Unmarshal(data, &rawMap); err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	// Parse structured fields (labels, policies, queries path refs)
	var rawStruct struct {
		Labels   []rawPathRef `yaml:"labels"`
		Policies []rawPathRef `yaml:"policies"`
		Queries  []rawPathRef `yaml:"queries"`
	}
	if err := yaml.Unmarshal(data, &rawStruct); err != nil {
		return nil, []ParseError{{File: path, Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	global := &ParsedGlobal{SourceFile: path}
	var errs []ParseError
	dir := filepath.Dir(path)

	// Extract org_settings, agent_options, controls as raw maps
	if v, ok := rawMap["org_settings"]; ok {
		if m, ok := v.(map[string]any); ok {
			global.OrgSettings = m
		}
	}
	if v, ok := rawMap["agent_options"]; ok {
		if m, ok := v.(map[string]any); ok {
			global.AgentOptions = m
		}
	}
	if v, ok := rawMap["controls"]; ok {
		if m, ok := v.(map[string]any); ok {
			global.Controls = m
		}
	}

	// Resolve global policies
	for _, ref := range rawStruct.Policies {
		policies, parseErrs := resolvePolicyRef(dir, ref.Path, path)
		errs = append(errs, parseErrs...)
		global.Policies = append(global.Policies, policies...)
	}

	// Resolve global queries
	for _, ref := range rawStruct.Queries {
		queries, parseErrs := resolveQueryRef(dir, ref.Path, path)
		errs = append(errs, parseErrs...)
		global.Queries = append(global.Queries, queries...)
	}

	// Resolve labels
	var labels []ParsedLabel
	for _, ref := range rawStruct.Labels {
		if ref.Path == "" {
			continue
		}
		resolved := filepath.Join(dir, ref.Path)
		if repoRoot != "" {
			if err := safePath(repoRoot, resolved); err != nil {
				errs = append(errs, ParseError{File: path, Message: err.Error()})
				continue
			}
		}

		fileData, err := os.ReadFile(resolved)
		if err != nil {
			errs = append(errs, ParseError{
				File:    path,
				Message: fmt.Sprintf("label path reference %q: %s", ref.Path, err),
			})
			continue
		}

		var items []ParsedLabel
		if err := yaml.Unmarshal(fileData, &items); err != nil {
			errs = append(errs, ParseError{
				File:    resolved,
				Message: fmt.Sprintf("YAML parse error: %s", err),
			})
			continue
		}
		for i := range items {
			items[i].SourceFile = resolved
		}
		labels = append(labels, items...)
	}

	return &parsedDefault{ParsedGlobal: global, labels: labels}, errs
}

// ---------- Profile name extraction ----------

// extractProfileName reads a profile file and extracts the name that Fleet
// will use to identify it. This matches Fleet's own behavior:
//   - .mobileconfig: top-level PayloadDisplayName from the plist
//   - .json (DDM):   PayloadDisplayName from the JSON declaration
//   - .xml (Windows): Name element from the SyncML/CSP XML
//
// Returns empty string if extraction fails (caller should fall back to filename).
// maxProfileSize is the maximum file size we'll read for profile name extraction.
// Profiles are typically under 100 KB; 10 MB is a generous safety limit.
const maxProfileSize = 10 << 20

func extractProfileName(filePath string) string {
	// Fleet uses PayloadDisplayName for .mobileconfig files, but uses the
	// filename (without extension) for .json (DDM) and .xml (Windows) files.
	// See: server/service/apple_mdm.go SameProfileNameUploadErrorMsg
	lower := strings.ToLower(filePath)
	if !strings.HasSuffix(lower, ".mobileconfig") {
		// For .json and .xml, Fleet uses the filename — no content extraction needed.
		return ""
	}

	// Size guard to prevent OOM on oversized files.
	info, err := os.Stat(filePath)
	if err != nil || info.Size() > maxProfileSize {
		return ""
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}

	return extractMobileconfigName(data)
}

// extractMobileconfigName extracts the top-level PayloadDisplayName from a
// .mobileconfig plist. In Apple's profile format, the top-level dict contains
// both a PayloadContent array (with nested payload dicts that have their own
// PayloadDisplayName) and a top-level PayloadDisplayName. Fleet uses the
// top-level one as the profile identity.
//
// In standard plist layout, the top-level PayloadDisplayName appears AFTER
// the PayloadContent array, so it's the last occurrence in the file.
func extractMobileconfigName(data []byte) string {
	s := string(data)
	needle := "<key>PayloadDisplayName</key>"
	last := ""

	// Find all occurrences and keep the last one — that's the top-level dict entry.
	remaining := s
	for {
		idx := strings.Index(remaining, needle)
		if idx < 0 {
			break
		}
		after := remaining[idx+len(needle):]
		after = strings.TrimSpace(after)
		if strings.HasPrefix(after, "<string>") {
			after = after[len("<string>"):]
			end := strings.Index(after, "</string>")
			if end >= 0 {
				last = strings.TrimSpace(after[:end])
			}
		}
		remaining = remaining[idx+len(needle):]
	}

	return last
}


// profileNameFromFilename derives a profile name from the filename by stripping
// the extension. This is the fallback when content extraction fails.
func profileNameFromFilename(filePath string) string {
	name := filepath.Base(filePath)
	for _, ext := range []string{".mobileconfig", ".json", ".xml"} {
		name = strings.TrimSuffix(name, ext)
	}
	return name
}

// ValidatePlatform checks if a platform string contains only valid identifiers.
func ValidatePlatform(platform string) []string {
	if platform == "" {
		return nil
	}
	var invalid []string
	for _, p := range strings.Split(platform, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !ValidPlatforms[p] {
			invalid = append(invalid, p)
		}
	}
	return invalid
}

// ValidateLogging checks if a logging value is valid.
func ValidateLogging(logging string) bool {
	if logging == "" {
		return true // default is fine
	}
	return ValidLogging[logging]
}
