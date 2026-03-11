// Package diff computes semantic diffs between current Fleet state (from API)
// and proposed state (from parsed YAML). It produces structured DiffResult
// values showing what will be added, modified, or deleted.
package diff

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/parser"
)

// wsRE collapses runs of whitespace (spaces, tabs, newlines) into a single space.
var wsRE = regexp.MustCompile(`\s+`)

// ---------- Result types ----------

// DiffResult holds the diff for a single team (or global scope).
type DiffResult struct {
	Team                 string // "(global)" for default.yml scope
	Policies             ResourceDiff
	Queries              ResourceDiff
	Software             ResourceDiff
	Profiles             ResourceDiff
	Labels               LabelValidation
	Config               []ConfigChange // org_settings, agent_options, controls diffs
	Errors               []string
	SkippedConfigSections []string // config sections absent from API (e.g. "agent_options")
}

// ConfigChange represents a change in a top-level config section.
type ConfigChange struct {
	Section string // "org_settings", "agent_options", "controls"
	Key     string // dot-separated path, e.g. "server_settings.server_url"
	Old     string
	New     string
}

// ResourceDiff categorizes changes for one resource type.
type ResourceDiff struct {
	Added    []ResourceChange
	Modified []ResourceChange
	Deleted  []ResourceChange
}

// IsEmpty returns true if there are no changes.
func (d ResourceDiff) IsEmpty() bool {
	return len(d.Added) == 0 && len(d.Modified) == 0 && len(d.Deleted) == 0
}

// Total returns the total number of changes.
func (d ResourceDiff) Total() int {
	return len(d.Added) + len(d.Modified) + len(d.Deleted)
}

// ResourceChange describes a single resource change.
type ResourceChange struct {
	Name      string
	Fields    map[string]FieldDiff // field name -> old/new values
	HostCount uint                 // from API: affected hosts
	Warning   string               // e.g., "will delete compliance data"
}

// FieldDiff shows old vs new value for a single field.
type FieldDiff struct {
	Old string
	New string
}

// LabelValidation reports label cross-reference status.
type LabelValidation struct {
	Valid   []LabelRef
	Missing []LabelRef
}

// LabelRef is a label name with metadata.
type LabelRef struct {
	Name         string
	HostCount    uint   // only for valid labels
	ReferencedBy string // which policy/query references it
}

func matchesAnyTeam(name string, filters []string) bool {
	for _, f := range filters {
		if strings.EqualFold(name, f) {
			return true
		}
	}
	return false
}

// ---------- Diff engine ----------

// Diff computes the diff between current Fleet state and proposed YAML for all
// teams. If teamFilters is non-empty, only matching teams are diffed.
// If changedFiles is non-empty, only resources whose SourceFile matches are
// included in the output (MR-scoped filtering).
// Global config from default.yml is diffed when proposed.Global is non-nil.
func Diff(current *api.FleetState, proposed *parser.ParsedRepo, teamFilters []string, changedFiles []string) []DiffResult {
	var results []DiffResult

	// Build label lookup from API
	labelMap := make(map[string]api.Label)
	for _, l := range current.Labels {
		labelMap[l.Name] = l
	}

	// --- Global config diff (default.yml) ---
	if proposed.Global != nil && len(teamFilters) == 0 {
		globalResult := DiffResult{Team: "(global)"}

		if current.Config != nil {
			globalResult.Config, globalResult.SkippedConfigSections = diffConfig(current.Config, proposed.Global)
		}

		// Diff global policies
		globalResult.Policies = diffPolicies(current.GlobalPolicies, proposed.Global.Policies)

		// Diff global queries
		globalResult.Queries = diffQueries(current.GlobalQueries, proposed.Global.Queries)

		results = append(results, globalResult)
	}

	// --- Per-team diffs ---
	// Index current teams by name
	currentTeams := make(map[string]api.Team)
	for _, t := range current.Teams {
		currentTeams[t.Name] = t
	}

	for _, proposedTeam := range proposed.Teams {
		if len(teamFilters) > 0 && !matchesAnyTeam(proposedTeam.Name, teamFilters) {
			continue
		}

		result := DiffResult{Team: proposedTeam.Name}

		currentTeam, exists := currentTeams[proposedTeam.Name]
		if !exists {
			// "No team" is a special Fleet concept -- it always exists but isn't
			// returned by the /teams API endpoint. It holds hosts not assigned to
			// any team. Skip the "will be created" warning for it.
			if strings.EqualFold(proposedTeam.Name, "No team") {
				// Can't deep-diff against API state for "No team" since it's not
				// in the teams list. Just show it exists with its resource counts.
				pCount := len(proposedTeam.Policies)
				qCount := len(proposedTeam.Queries)
				if pCount > 0 || qCount > 0 {
					result.Errors = append(result.Errors,
						fmt.Sprintf("%d policies, %d queries configured (no API diff available for \"No team\")", pCount, qCount))
				}
			} else {
				// Genuinely new team
				for _, p := range proposedTeam.Policies {
					result.Policies.Added = append(result.Policies.Added, ResourceChange{Name: p.Name})
				}
				for _, q := range proposedTeam.Queries {
					result.Queries.Added = append(result.Queries.Added, ResourceChange{Name: q.Name})
				}
				result.Errors = append(result.Errors, fmt.Sprintf("info: team %q does not exist in Fleet yet (will be created)", proposedTeam.Name))
			}
		} else {
			result.Policies = diffPolicies(currentTeam.Policies, proposedTeam.Policies)
			result.Queries = diffQueries(currentTeam.Queries, proposedTeam.Queries)

			if currentTeam.SoftwareUnavailable {
				result.Errors = append(result.Errors, "software diff skipped: API token lacks permission to read software titles")
			} else {
				currentSoftware := currentTeam.Software

				// Fleet's /teams API returns fleet_maintained_apps: null for some
				// teams even when they have fleet-maintained apps configured. When
				// the field is null but the YAML defines fleet-maintained apps,
				// silently reconstruct the current state from software titles +
				// the fleet-maintained catalog so we can produce an accurate diff.
			if currentTeam.Software.FleetMaintained == nil &&
				len(proposedTeam.Software.FleetMaintained) > 0 {
					inferred := inferFleetMaintainedApps(currentTeam, current.FleetMaintainedCatalog, proposedTeam.Software.Packages)
					currentSoftware.FleetMaintained = inferred // may be nil; that's fine
				}

				result.Software = diffSoftware(currentSoftware, proposedTeam.Software)
			}

			if currentTeam.ProfilesUnavailable {
				result.Errors = append(result.Errors, "profiles diff skipped: API token lacks permission to read profiles")
			} else {
				var profileWarnings []string
				result.Profiles, profileWarnings = diffProfiles(currentTeam.Profiles, proposedTeam.Profiles)
				result.Errors = append(result.Errors, profileWarnings...)
			}
		}

		if len(changedFiles) > 0 {
			sourceNames := buildSourceMap(proposedTeam)
			result.Policies = filterResourceDiff(result.Policies, sourceNames, changedFiles)
			result.Queries = filterResourceDiff(result.Queries, sourceNames, changedFiles)
			result.Software = filterResourceDiff(result.Software, sourceNames, changedFiles)
			result.Profiles = filterResourceDiff(result.Profiles, sourceNames, changedFiles)
		}

		result.Labels = validateLabels(proposedTeam, labelMap, changedNames(result.Policies))
		results = append(results, result)
	}

	return results
}

func buildSourceMap(team parser.ParsedTeam) map[string]string {
	m := make(map[string]string)
	for _, p := range team.Policies {
		m[p.Name] = p.SourceFile
	}
	for _, q := range team.Queries {
		m[q.Name] = q.SourceFile
	}
	for _, p := range team.Software.Packages {
		key := normalizeSoftwarePath(p.RefPath)
		if key == "" {
			key = inferSoftwarePathFromSource(p.SourceFile)
		}
		if key == "" {
			key = normalizeSoftwarePath(p.URL)
		}
		if key != "" {
			m[normalizeSoftwarePath(key)] = p.SourceFile
		}
	}
	for _, f := range team.Software.FleetMaintained {
		slug := normalizeSoftwarePath(f.Slug)
		if slug != "" {
			m["fleet app "+slug] = team.SourceFile
		}
	}
	for _, p := range team.Profiles {
		m[p.Name] = p.SourceFile
	}
	return m
}

func filterResourceDiff(rd ResourceDiff, sourceNames map[string]string, changedFiles []string) ResourceDiff {
	match := func(name string) bool {
		src, ok := sourceNames[name]
		if !ok {
			return true
		}
		for _, cf := range changedFiles {
			if src == cf || strings.HasSuffix(src, "/"+cf) {
				return true
			}
		}
		return false
	}
	return ResourceDiff{
		Added:    filterChanges(rd.Added, match),
		Modified: filterChanges(rd.Modified, match),
		Deleted:  filterChanges(rd.Deleted, match),
	}
}

func filterChanges(changes []ResourceChange, keep func(string) bool) []ResourceChange {
	var out []ResourceChange
	for _, c := range changes {
		if keep(c.Name) {
			out = append(out, c)
		}
	}
	return out
}

// ---------- Per-resource diffing ----------

func diffPolicies(current []api.Policy, proposed []parser.ParsedPolicy) ResourceDiff {
	var diff ResourceDiff

	currentMap := make(map[string]api.Policy)
	for _, p := range current {
		currentMap[p.Name] = p
	}

	proposedNames := make(map[string]bool)
	for _, p := range proposed {
		proposedNames[p.Name] = true
		cur, exists := currentMap[p.Name]
		if !exists {
			fields := map[string]FieldDiff{
				"query":    {New: normalizeWS(p.Query)},
				"platform": {New: p.Platform},
				"critical": {New: fmt.Sprint(p.Critical)},
			}
			if p.Description != "" {
				fields["description"] = FieldDiff{New: normalizeWS(p.Description)}
			}
			if p.Resolution != "" {
				fields["resolution"] = FieldDiff{New: normalizeWS(p.Resolution)}
			}
			diff.Added = append(diff.Added, ResourceChange{Name: p.Name, Fields: fields})
			continue
		}

		fields := make(map[string]FieldDiff)
		if normalizeWS(cur.Query) != normalizeWS(p.Query) {
			fields["query"] = FieldDiff{Old: normalizeWS(cur.Query), New: normalizeWS(p.Query)}
		}
		if normalizeWS(cur.Description) != normalizeWS(p.Description) {
			fields["description"] = FieldDiff{Old: normalizeWS(cur.Description), New: normalizeWS(p.Description)}
		}
		if normalizeWS(cur.Resolution) != normalizeWS(p.Resolution) {
			fields["resolution"] = FieldDiff{Old: normalizeWS(cur.Resolution), New: normalizeWS(p.Resolution)}
		}
		if cur.Platform != p.Platform {
			fields["platform"] = FieldDiff{Old: cur.Platform, New: p.Platform}
		}
		if cur.Critical != p.Critical {
			fields["critical"] = FieldDiff{Old: fmt.Sprint(cur.Critical), New: fmt.Sprint(p.Critical)}
		}

		if len(fields) > 0 {
			diff.Modified = append(diff.Modified, ResourceChange{
				Name:      p.Name,
				Fields:    fields,
				HostCount: cur.PassingHostCount + cur.FailingHostCount,
			})
		}
	}

	// Anything in current but not proposed will be deleted
	for _, cur := range current {
		if !proposedNames[cur.Name] {
			hostCount := cur.PassingHostCount + cur.FailingHostCount
			warning := ""
			if hostCount > 0 {
				warning = fmt.Sprintf("will delete policy affecting %d hosts", hostCount)
			}
			diff.Deleted = append(diff.Deleted, ResourceChange{
				Name:      cur.Name,
				HostCount: hostCount,
				Warning:   warning,
			})
		}
	}

	return diff
}

func diffQueries(current []api.Query, proposed []parser.ParsedQuery) ResourceDiff {
	var diff ResourceDiff

	currentMap := make(map[string]api.Query)
	for _, q := range current {
		currentMap[q.Name] = q
	}

	proposedNames := make(map[string]bool)
	for _, q := range proposed {
		proposedNames[q.Name] = true
		cur, exists := currentMap[q.Name]
		if !exists {
			fields := map[string]FieldDiff{
				"query":    {New: normalizeWS(q.Query)},
				"interval": {New: fmt.Sprint(q.Interval)},
				"platform": {New: q.Platform},
			}
			if q.Logging != "" {
				fields["logging"] = FieldDiff{New: q.Logging}
			}
			diff.Added = append(diff.Added, ResourceChange{Name: q.Name, Fields: fields})
			continue
		}

		fields := make(map[string]FieldDiff)
		if normalizeWS(cur.Query) != normalizeWS(q.Query) {
			fields["query"] = FieldDiff{Old: normalizeWS(cur.Query), New: normalizeWS(q.Query)}
		}
		if cur.Interval != q.Interval {
			fields["interval"] = FieldDiff{
				Old: fmt.Sprint(cur.Interval),
				New: fmt.Sprint(q.Interval),
			}
		}
		if cur.Platform != q.Platform {
			fields["platform"] = FieldDiff{Old: cur.Platform, New: q.Platform}
		}
		if cur.Logging != q.Logging && q.Logging != "" {
			fields["logging"] = FieldDiff{Old: cur.Logging, New: q.Logging}
		}

		if len(fields) > 0 {
			diff.Modified = append(diff.Modified, ResourceChange{
				Name:   q.Name,
				Fields: fields,
			})
		}
	}

	for _, cur := range current {
		if !proposedNames[cur.Name] {
			diff.Deleted = append(diff.Deleted, ResourceChange{Name: cur.Name})
		}
	}

	return diff
}

func diffSoftware(current api.TeamSoftware, proposed parser.ParsedSoftware) ResourceDiff {
	var rd ResourceDiff

	// -------- Packages (keyed by referenced_yaml_path) --------
	currentPkgs := make(map[string]api.TeamSoftwarePackage)
	for _, p := range current.Packages {
		key := normalizeSoftwarePath(p.ReferencedYAMLPath)
		if key == "" {
			key = normalizeSoftwarePath(p.URL)
		}
		if key != "" {
			currentPkgs[key] = p
		}
	}

	proposedPkgs := make(map[string]parser.ParsedSoftwarePackage)
	for _, p := range proposed.Packages {
		key := normalizeSoftwarePath(p.RefPath)
		if key == "" {
			key = inferSoftwarePathFromSource(p.SourceFile)
		}
		if key == "" {
			key = normalizeSoftwarePath(p.URL)
		}
		if key != "" {
			proposedPkgs[key] = p
		}
	}

	for key, p := range proposedPkgs {
		cur, exists := currentPkgs[key]
		if !exists {
			fields := map[string]FieldDiff{
				"url":          {New: p.URL},
				"self_service": {New: fmt.Sprint(p.SelfService)},
			}
			if p.HashSHA256 != "" {
				fields["hash_sha256"] = FieldDiff{New: p.HashSHA256}
			}
			rd.Added = append(rd.Added, ResourceChange{Name: normalizeSoftwarePath(key), Fields: fields})
			continue
		}
		fields := make(map[string]FieldDiff)
		if cur.URL != p.URL {
			fields["url"] = FieldDiff{Old: cur.URL, New: p.URL}
		}
		if cur.HashSHA256 != p.HashSHA256 {
			fields["hash_sha256"] = FieldDiff{Old: cur.HashSHA256, New: p.HashSHA256}
		}
		if cur.SelfService != p.SelfService {
			fields["self_service"] = FieldDiff{Old: fmt.Sprint(cur.SelfService), New: fmt.Sprint(p.SelfService)}
		}
		if len(fields) > 0 {
			rd.Modified = append(rd.Modified, ResourceChange{
				Name:   normalizeSoftwarePath(key),
				Fields: fields,
			})
		}
	}
	for key := range currentPkgs {
		if _, exists := proposedPkgs[key]; !exists {
			rd.Deleted = append(rd.Deleted, ResourceChange{Name: normalizeSoftwarePath(key)})
		}
	}

	// -------- Fleet-maintained apps (keyed by slug) --------
	{
		currentFleet := make(map[string]api.TeamFleetApp)
		for _, a := range current.FleetMaintained {
			slug := normalizeSoftwarePath(a.Slug)
			if slug != "" {
				currentFleet[slug] = a
			}
		}
		proposedFleet := make(map[string]parser.ParsedFleetApp)
		for _, a := range proposed.FleetMaintained {
			slug := normalizeSoftwarePath(a.Slug)
			if slug != "" {
				proposedFleet[slug] = a
			}
		}

		for slug, a := range proposedFleet {
			cur, exists := currentFleet[slug]
			if !exists {
				rd.Added = append(rd.Added, ResourceChange{
					Name: "fleet app " + slug,
					Fields: map[string]FieldDiff{
						"slug":         {New: a.Slug},
						"self_service": {New: fmt.Sprint(a.SelfService)},
					},
				})
				continue
			}
			if cur.SelfService != a.SelfService {
				rd.Modified = append(rd.Modified, ResourceChange{
					Name: "fleet app " + slug,
					Fields: map[string]FieldDiff{
						"self_service": {
							Old: fmt.Sprint(cur.SelfService),
							New: fmt.Sprint(a.SelfService),
						},
					},
				})
			}
		}
		for slug := range currentFleet {
			if _, exists := proposedFleet[slug]; !exists {
				rd.Deleted = append(rd.Deleted, ResourceChange{Name: "fleet app " + slug})
			}
		}
	}

	// -------- App Store apps (keyed by app_store_id) --------
	currentApps := make(map[string]api.TeamAppStoreApp)
	for _, a := range current.AppStoreApps {
		id := strings.TrimSpace(a.AppStoreID)
		if id != "" {
			currentApps[id] = a
		}
	}
	proposedApps := make(map[string]parser.ParsedAppStoreApp)
	for _, a := range proposed.AppStoreApps {
		id := strings.TrimSpace(a.AppStoreID)
		if id != "" {
			proposedApps[id] = a
		}
	}

	for id, a := range proposedApps {
		cur, exists := currentApps[id]
		if !exists {
			rd.Added = append(rd.Added, ResourceChange{
				Name: "app store app " + id,
				Fields: map[string]FieldDiff{
					"app_store_id": {New: a.AppStoreID},
					"self_service": {New: fmt.Sprint(a.SelfService)},
				},
			})
			continue
		}
		if cur.SelfService != a.SelfService {
			rd.Modified = append(rd.Modified, ResourceChange{
				Name: "app store app " + id,
				Fields: map[string]FieldDiff{
					"self_service": {
						Old: fmt.Sprint(cur.SelfService),
						New: fmt.Sprint(a.SelfService),
					},
				},
			})
		}
	}
	for id := range currentApps {
		if _, exists := proposedApps[id]; !exists {
			rd.Deleted = append(rd.Deleted, ResourceChange{Name: "app store app " + id})
		}
	}

	sortResourceChanges(&rd)
	return rd
}

func normalizeSoftwarePath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.TrimPrefix(s, "./")
	for strings.HasPrefix(s, "../") {
		s = strings.TrimPrefix(s, "../")
	}
	s = strings.TrimPrefix(s, "/")
	return s
}

func inferSoftwarePathFromSource(source string) string {
	source = strings.ReplaceAll(source, "\\", "/")
	if idx := strings.Index(source, "/software/"); idx >= 0 {
		return strings.TrimPrefix(source[idx+1:], "/")
	}
	return ""
}

func sortResourceChanges(rd *ResourceDiff) {
	byName := func(a, b ResourceChange) bool { return a.Name < b.Name }
	sort.Slice(rd.Added, func(i, j int) bool { return byName(rd.Added[i], rd.Added[j]) })
	sort.Slice(rd.Modified, func(i, j int) bool { return byName(rd.Modified[i], rd.Modified[j]) })
	sort.Slice(rd.Deleted, func(i, j int) bool { return byName(rd.Deleted[i], rd.Deleted[j]) })
}

func inferFleetMaintainedApps(team api.Team, catalog []api.FleetMaintainedApp, proposedPackages []parser.ParsedSoftwarePackage) []api.TeamFleetApp {
	if len(team.SoftwareTitles) == 0 || len(catalog) == 0 {
		return nil
	}

	// Build exclusion set from YAML-defined custom packages. We cannot use
	// team.Software.Packages because the API merges fleet-maintained apps
	// into that list when fleet_maintained_apps is null. The proposed YAML
	// packages are the authoritative set of custom (non-FMA) software.
	proposedPkgURLs := make(map[string]bool)
	for _, p := range proposedPackages {
		u := normalizeSoftwarePath(p.URL)
		if u != "" {
			proposedPkgURLs[u] = true
		}
	}

	catalogByAppID := make(map[uint]api.FleetMaintainedApp)
	catalogByTitleID := make(map[uint]api.FleetMaintainedApp)
	catalogByNamePlatform := make(map[string][]api.FleetMaintainedApp)
	catalogByPlatform := make(map[string][]api.FleetMaintainedApp)
	for _, app := range catalog {
		if app.ID != 0 {
			catalogByAppID[app.ID] = app
		}
		if app.SoftwareTitleID != 0 {
			catalogByTitleID[app.SoftwareTitleID] = app
		}
		key := fleetCatalogKey(app.Name, app.Platform)
		if key != "" {
			catalogByNamePlatform[key] = append(catalogByNamePlatform[key], app)
		}
		plat := normalizeFleetPlatform(app.Platform)
		if plat != "" {
			catalogByPlatform[plat] = append(catalogByPlatform[plat], app)
		}
	}

	inferred := make(map[string]api.TeamFleetApp)
	for _, title := range team.SoftwareTitles {
		if title.AppStoreApp != nil {
			continue
		}
		if title.SoftwarePackage == nil {
			continue
		}

		packageURL := normalizeSoftwarePath(title.SoftwarePackage.PackageURL)
		if packageURL != "" && proposedPkgURLs[packageURL] {
			continue
		}

		// Strategy 1: match by fleet_maintained_app_id (exact, from API).
		if title.SoftwarePackage.FleetMaintainedAppID != nil {
			if app, ok := catalogByAppID[*title.SoftwarePackage.FleetMaintainedAppID]; ok {
				slug := normalizeSoftwarePath(app.Slug)
				if slug != "" {
					inferred[slug] = api.TeamFleetApp{
						Slug:        slug,
						SelfService: title.SoftwarePackage.SelfService,
					}
					continue
				}
			}
		}

		// Strategy 2: match by SoftwareTitleID -> catalog SoftwareTitleID.
		if app, ok := catalogByTitleID[title.ID]; ok {
			slug := normalizeSoftwarePath(app.Slug)
			if slug != "" {
				inferred[slug] = api.TeamFleetApp{
					Slug:        slug,
					SelfService: title.SoftwarePackage.SelfService,
				}
				continue
			}
		}

		// Strategy 3: match by name|platform (requires exactly 1 catalog hit).
		// Windows titles often include arch suffixes (e.g., "Notepad++ (64-bit x64)")
		// that the catalog omits, so try the raw name first, then stripped.
		// The catalog uses short marketing names (e.g., "OBS", "Zoom") while the
		// OS reports full product names (e.g., "OBS Studio", "Zoom Workplace"),
		// so fall back to prefix matching when exact/stripped matching fails.
		key := fleetCatalogKey(title.Name, title.SoftwarePackage.Platform)
		matches := catalogByNamePlatform[key]
		if len(matches) != 1 {
			stripped := stripArchSuffix(title.Name)
			if stripped != strings.TrimSpace(strings.ToLower(title.Name)) {
				key = fleetCatalogKey(stripped, title.SoftwarePackage.Platform)
				matches = catalogByNamePlatform[key]
			}
		}
		if len(matches) != 1 {
			matches = catalogPrefixMatch(title.Name, title.SoftwarePackage.Platform, catalogByPlatform)
		}
		if len(matches) != 1 {
			continue
		}

		slug := normalizeSoftwarePath(matches[0].Slug)
		if slug == "" {
			continue
		}
		inferred[slug] = api.TeamFleetApp{
			Slug:        slug,
			SelfService: title.SoftwarePackage.SelfService,
		}
	}

	if len(inferred) == 0 {
		return nil
	}
	out := make([]api.TeamFleetApp, 0, len(inferred))
	for _, app := range inferred {
		out = append(out, app)
	}
	return out
}

// catalogPrefixMatch finds catalog entries whose name is a prefix of the
// title name on the same platform. Returns matches only when exactly one
// catalog entry qualifies (ambiguous results are discarded). This handles
// cases where the catalog uses short marketing names ("OBS", "Zoom") while
// the OS reports full product names ("OBS Studio", "Zoom Workplace").
func catalogPrefixMatch(titleName, titlePlatform string, byPlatform map[string][]api.FleetMaintainedApp) []api.FleetMaintainedApp {
	plat := normalizeFleetPlatform(titlePlatform)
	candidates := byPlatform[plat]
	if len(candidates) == 0 {
		return nil
	}
	norm := strings.TrimSpace(strings.ToLower(stripArchSuffix(titleName)))
	if norm == "" {
		return nil
	}
	var hits []api.FleetMaintainedApp
	for _, app := range candidates {
		catName := strings.TrimSpace(strings.ToLower(app.Name))
		if catName != "" && strings.HasPrefix(norm, catName) {
			hits = append(hits, app)
		}
	}
	return hits
}

func fleetCatalogKey(name, platform string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	platform = normalizeFleetPlatform(platform)
	if name == "" || platform == "" {
		return ""
	}
	return name + "|" + platform
}

var archSuffixRe = regexp.MustCompile(`(?i)\s*\((?:x64|x86|64-bit(?:\s+x64)?|32-bit|arm64|amd64)\)\s*$`)

func stripArchSuffix(name string) string {
	return strings.TrimSpace(archSuffixRe.ReplaceAllString(name, ""))
}

func normalizeFleetPlatform(platform string) string {
	p := strings.TrimSpace(strings.ToLower(platform))
	switch p {
	case "macos":
		return "darwin"
	default:
		return p
	}
}

func diffProfiles(current []api.Profile, proposed []parser.ParsedProfile) (ResourceDiff, []string) {
	var diff ResourceDiff
	var warnings []string

	currentMap := make(map[string]api.Profile)
	for _, p := range current {
		currentMap[p.Name] = p
	}

	// Profile identity is determined by the name embedded in the file content
	// (e.g., PayloadDisplayName for .mobileconfig), which is what Fleet uses.
	// The parser extracts this into ParsedProfile.Name during parsing.
	proposedNames := make(map[string]bool)
	for _, p := range proposed {
		name := p.Name
		if proposedNames[name] {
			warnings = append(warnings, fmt.Sprintf("duplicate profile name %q derived from %q (conflicts with another profile)", name, p.Path))
		}
		proposedNames[name] = true
		if _, exists := currentMap[name]; !exists {
			fields := map[string]FieldDiff{
				"platform": {New: p.Platform},
			}
			if p.Path != "" {
				fields["path"] = FieldDiff{New: p.Path}
			}
			diff.Added = append(diff.Added, ResourceChange{
				Name:   name,
				Fields: fields,
			})
		}
	}

	for _, cur := range current {
		if !proposedNames[cur.Name] {
			diff.Deleted = append(diff.Deleted, ResourceChange{
				Name: cur.Name,
			})
		}
	}

	return diff, warnings
}

// ---------- Label validation ----------

// changedNames collects all resource names from a ResourceDiff (added + modified + deleted).
func changedNames(d ResourceDiff) map[string]bool {
	names := make(map[string]bool, d.Total())
	for _, c := range d.Added {
		names[c.Name] = true
	}
	for _, c := range d.Modified {
		names[c.Name] = true
	}
	for _, c := range d.Deleted {
		names[c.Name] = true
	}
	return names
}

// validateLabels checks labels referenced by changed policies only.
// If no policies changed, the label section is empty.
func validateLabels(team parser.ParsedTeam, labelMap map[string]api.Label, changed map[string]bool) LabelValidation {
	var validation LabelValidation
	seen := make(map[string]bool)

	checkLabel := func(name, referencedBy string) {
		if seen[name] {
			return
		}
		seen[name] = true
		if l, ok := labelMap[name]; ok {
			validation.Valid = append(validation.Valid, LabelRef{
				Name:         name,
				HostCount:    l.HostCount,
				ReferencedBy: referencedBy,
			})
		} else {
			validation.Missing = append(validation.Missing, LabelRef{
				Name:         name,
				ReferencedBy: referencedBy,
			})
		}
	}

	for _, p := range team.Policies {
		if !changed[p.Name] {
			continue
		}
		for _, name := range p.LabelsIncludeAny {
			checkLabel(name, p.Name)
		}
		for _, name := range p.LabelsExcludeAny {
			checkLabel(name, p.Name)
		}
	}

	return validation
}

// ---------- Global config diffing ----------

// diffConfig compares the current Fleet config (from API) against proposed
// global config sections from default.yml. Returns a list of config changes.
// Skips values containing "$" (env var placeholders that Fleet substitutes).
func diffConfig(apiConfig map[string]any, proposed *parser.ParsedGlobal) ([]ConfigChange, []string) {
	var changes []ConfigChange
	var skipped []string

	sections := map[string]map[string]any{
		"org_settings":  proposed.OrgSettings,
		"agent_options": proposed.AgentOptions,
		"controls":      proposed.Controls,
	}

	for section, proposedMap := range sections {
		if proposedMap == nil {
			continue
		}

		var apiSection map[string]any
		switch section {
		case "org_settings":
			apiSection = apiConfig
		case "agent_options":
			if v, ok := apiConfig["agent_options"]; ok {
				if m, ok := v.(map[string]any); ok {
					apiSection = m
				}
			}
		case "controls":
			if v, ok := apiConfig["mdm"]; ok {
				if m, ok := v.(map[string]any); ok {
					apiSection = m
				}
			}
		}

		if apiSection == nil {
			skipped = append(skipped, section)
			continue
		}

		// Recursive key-by-key comparison.
		// Only report a diff when the API actually has a value for this key.
		// If getNestedValue returns "" the API doesn't expose the field (e.g.
		// agent_options sub-keys, sso_settings) and we can't determine whether
		// the proposed value differs from what Fleet already has.
		flattenMap(proposedMap, "", func(key, proposedVal string) {
			if containsEnvVar(proposedVal) {
				return
			}
			if proposedVal == "<nil>" || proposedVal == "" {
				return
			}
			apiVal := getNestedValue(apiSection, key)
			if apiVal == "<nil>" || apiVal == "" {
				return
			}
			compareAPI, compareProposed := apiVal, proposedVal
			if looksLikeJSON(apiVal) && looksLikeJSON(proposedVal) {
				compareAPI = normalizeJSON(apiVal)
				compareProposed = normalizeJSON(proposedVal)
			}
			if compareAPI != compareProposed {
				changes = append(changes, ConfigChange{
					Section: section,
					Key:     key,
					Old:     apiVal,
					New:     proposedVal,
				})
			}
		})
	}

	return changes, skipped
}

// containsEnvVar returns true if the string contains a $ (env var placeholder).
func containsEnvVar(s string) bool {
	return strings.Contains(s, "$")
}

func looksLikeJSON(s string) bool {
	s = strings.TrimSpace(s)
	return len(s) > 0 && (s[0] == '{' || s[0] == '[')
}

// normalizeJSON unmarshals a JSON string, recursively removes keys with empty
// string values (""), empty arrays ([]), and null values, then re-marshals with
// sorted keys. Returns the original string on error or if input is not JSON.
// Used to compare semantically equivalent JSON where API includes empty keys
// and YAML omits them.
func normalizeJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || (len(s) > 0 && s[0] != '{' && s[0] != '[') {
		return s
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	cleaned := removeEmptyJSONValues(v)
	b, err := json.Marshal(cleaned)
	if err != nil {
		return s
	}
	return string(b)
}

func removeEmptyJSONValues(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any)
		for k, val := range x {
			if val == nil {
				continue
			}
			if s, ok := val.(string); ok && s == "" {
				continue
			}
			if arr, ok := val.([]any); ok && len(arr) == 0 {
				continue
			}
			out[k] = removeEmptyJSONValues(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, elem := range x {
			if elem == nil {
				continue
			}
			if s, ok := elem.(string); ok && s == "" {
				continue
			}
			if arr, ok := elem.([]any); ok && len(arr) == 0 {
				continue
			}
			out = append(out, removeEmptyJSONValues(elem))
		}
		return out
	default:
		return v
	}
}

// flattenMap recursively flattens a nested map into dot-separated key paths.
// Calls fn(key, value) for each leaf value. Slices are serialized to JSON
// for stable, order-independent comparison.
func flattenMap(m map[string]any, prefix string, fn func(key, val string)) {
	for k, v := range m {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenMap(val, fullKey, fn)
		case []any:
			b, err := json.Marshal(val)
			if err != nil {
				fn(fullKey, fmt.Sprint(val))
			} else {
				fn(fullKey, string(b))
			}
		default:
			fn(fullKey, fmt.Sprint(v))
		}
	}
}

// getNestedValue retrieves a value from a nested map using a dot-separated key.
// Slices are JSON-serialized to match flattenMap's output format.
func getNestedValue(m map[string]any, key string) string {
	parts := strings.Split(key, ".")
	current := m
	for i, part := range parts {
		v, ok := current[part]
		if !ok {
			return ""
		}
		if i == len(parts)-1 {
			if slice, ok := v.([]any); ok {
				b, err := json.Marshal(slice)
				if err != nil {
					return fmt.Sprint(v)
				}
				return string(b)
			}
			return fmt.Sprint(v)
		}
		if next, ok := v.(map[string]any); ok {
			current = next
		} else {
			return ""
		}
	}
	return ""
}

// ---------- Helpers ----------

// normalizeWS collapses all whitespace runs into a single space and trims.
// Used for comparison only — the raw values are stored in FieldDiff so the
// renderer can show full content in verbose mode.
func normalizeWS(s string) string {
	return strings.TrimSpace(wsRE.ReplaceAllString(s, " "))
}

