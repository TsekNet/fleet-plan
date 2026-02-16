package diff

import (
	"strings"
	"testing"

	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/parser"
	"github.com/TsekNet/fleet-plan/internal/testutil"
)

// TestDiffTestdataAgainstMockAPI parses the shared testdata/ fixture and diffs
// it against a simulated current Fleet state. This is the integration test that
// exercises parser → differ together.
//
// Mock API state for Workstations:
//   - Policies: FileVault (different query → modified), Legacy AV (not in YAML → deleted, 100 hosts)
//   - Queries: Disk Usage (same), old "Network Interfaces" (not in YAML → deleted)
//   - Software: slack (old URL → modified), old-agent (not in YAML → deleted)
//
// Mock API state for Servers:
//   - Queries: Uptime (interval changed → modified)
//   - No policies (SSH Root Login is new → added)
//
// Mobile team: not in API → new team info message.
// Labels: macOS 14+ and Windows 11 exist. Ubuntu 24.04 does NOT → missing label error.
func TestDiffTestdataAgainstMockAPI(t *testing.T) {
	root := testutil.TestdataRoot(t)

	proposed, err := parser.ParseRepo(root, "", "")
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	if len(proposed.Errors) > 0 {
		t.Fatalf("unexpected parse errors: %v", proposed.Errors)
	}

	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Policies: []api.Policy{
					// FileVault exists but with a different (simpler) query → modified
					{Name: "[macOS] FileVault Enabled", Query: "SELECT 1 FROM disk_encryption WHERE encrypted = 1;", Platform: "darwin", Critical: true},
					// Legacy AV not in YAML → deleted, has hosts
					{Name: "[Windows] Legacy AV Check", Query: "SELECT 1;", Platform: "windows", PassingHostCount: 100},
				},
				Queries: []api.Query{
					{Name: "Disk Usage", Query: "SELECT path, type, blocks_size, blocks, blocks_free, blocks_available FROM mounts WHERE type != 'tmpfs';", Interval: 3600, Platform: "darwin,windows,linux"},
					// Network Interfaces not in YAML → deleted
					{Name: "Network Interfaces", Query: "SELECT * FROM interface_addresses;", Interval: 3600, Platform: "darwin,windows,linux"},
				},
				Software: api.TeamSoftware{
					Packages: []api.TeamSoftwarePackage{
						// Slack: old URL → modified
						{ReferencedYAMLPath: "software/mac/slack/slack.yml", URL: "https://downloads.slack-edge.com/desktop-releases/macos/4.41.47/Slack-4.41.47-macOS.dmg", HashSHA256: "abc123def456789"},
						// old-agent: not in YAML → deleted
						{ReferencedYAMLPath: "software/mac/old-agent/old-agent.yml", URL: "https://example.com/old-agent.pkg"},
					},
				},
				// Profile uses PayloadDisplayName "fleet_orbit-allowfulldiskaccess"
				// which matches the content of the .mobileconfig file (not the filename).
				Profiles: []api.Profile{
					{Name: "fleet_orbit-allowfulldiskaccess"},
				},
			},
			{
				ID:   2,
				Name: "Servers",
				Queries: []api.Query{
					// Uptime exists but with different interval → modified
					{Name: "System Uptime", Query: "SELECT total_seconds, days, hours FROM uptime;", Interval: 3600, Platform: "darwin,windows,linux"},
				},
			},
		},
		Labels: []api.Label{
			{ID: 1, Name: "macOS 14+", HostCount: 8512},
			{ID: 2, Name: "Windows 11", HostCount: 3200},
			// Ubuntu 24.04 NOT present → missing label error
		},
	}

	// --- Test all teams (unfiltered) ---
	allResults := Diff(current, proposed, "")

	// Should have 4 results: (global), Workstations, Servers, Mobile
	// The (global) result comes from default.yml parsing.
	if len(allResults) != 4 {
		t.Fatalf("expected 4 results, got %d", len(allResults))
	}

	// Verify global result exists and is first
	if allResults[0].Team != "(global)" {
		t.Errorf("first result should be (global), got %q", allResults[0].Team)
	}

	// --- Workstations ---
	ws := findTeam(t, allResults, "Workstations")

	// Policies: Gatekeeper, Defender, SSH, Firewall are new (4 added)
	if len(ws.Policies.Added) != 4 {
		t.Errorf("Workstations: expected 4 added policies, got %d: %v", len(ws.Policies.Added), ws.Policies.Added)
	}
	// FileVault modified (query changed)
	if len(ws.Policies.Modified) != 1 {
		t.Errorf("Workstations: expected 1 modified policy, got %d", len(ws.Policies.Modified))
	} else if ws.Policies.Modified[0].Name != "[macOS] FileVault Enabled" {
		t.Errorf("Workstations: modified policy name: got %q", ws.Policies.Modified[0].Name)
	}
	// Legacy AV deleted
	if len(ws.Policies.Deleted) != 1 {
		t.Fatalf("Workstations: expected 1 deleted policy, got %d", len(ws.Policies.Deleted))
	}
	if ws.Policies.Deleted[0].Name != "[Windows] Legacy AV Check" {
		t.Errorf("deleted policy: got %q", ws.Policies.Deleted[0].Name)
	}
	if ws.Policies.Deleted[0].HostCount != 100 {
		t.Errorf("deleted policy host count: got %d", ws.Policies.Deleted[0].HostCount)
	}
	if ws.Policies.Deleted[0].Warning == "" {
		t.Error("expected warning for deletion with hosts")
	}

	// Queries: Uptime + OS Version are new (2 added), Disk Usage unchanged, Network Interfaces deleted
	if len(ws.Queries.Added) != 2 {
		t.Errorf("Workstations: expected 2 added queries, got %d", len(ws.Queries.Added))
	}
	if len(ws.Queries.Deleted) != 1 {
		t.Errorf("Workstations: expected 1 deleted query, got %d", len(ws.Queries.Deleted))
	} else if ws.Queries.Deleted[0].Name != "Network Interfaces" {
		t.Errorf("Workstations: deleted query name: got %q", ws.Queries.Deleted[0].Name)
	}

	// Software: slack modified (URL changed), example-app added, old-agent deleted
	if len(ws.Software.Modified) != 1 {
		t.Errorf("Workstations: expected 1 modified software, got %d", len(ws.Software.Modified))
	}
	if len(ws.Software.Added) != 1 {
		t.Errorf("Workstations: expected 1 added software, got %d", len(ws.Software.Added))
	}
	if len(ws.Software.Deleted) != 1 {
		t.Errorf("Workstations: expected 1 deleted software, got %d", len(ws.Software.Deleted))
	}

	// Profiles: fleet_orbit-allowfulldiskaccess matches by PayloadDisplayName → no diff
	if !ws.Profiles.IsEmpty() {
		t.Errorf("Workstations: expected no profile changes (name matches PayloadDisplayName), got added=%d modified=%d deleted=%d",
			len(ws.Profiles.Added), len(ws.Profiles.Modified), len(ws.Profiles.Deleted))
	}

	// Labels: macOS 14+ valid, Ubuntu 24.04 missing
	if len(ws.Labels.Valid) == 0 {
		t.Error("expected at least 1 valid label reference")
	}
	if len(ws.Labels.Missing) != 1 {
		t.Errorf("expected 1 missing label, got %d", len(ws.Labels.Missing))
	} else if ws.Labels.Missing[0].Name != "Ubuntu 24.04" {
		t.Errorf("missing label: got %q", ws.Labels.Missing[0].Name)
	}

	// --- Servers ---
	srv := findTeam(t, allResults, "Servers")

	// SSH Root Login is new → added
	if len(srv.Policies.Added) != 1 {
		t.Errorf("Servers: expected 1 added policy, got %d", len(srv.Policies.Added))
	}
	// SSH Root Login references Ubuntu 24.04 which is not in mock API → missing
	if len(srv.Labels.Missing) != 1 {
		t.Errorf("Servers: expected 1 missing label, got %d", len(srv.Labels.Missing))
	} else if srv.Labels.Missing[0].Name != "Ubuntu 24.04" {
		t.Errorf("Servers: missing label: got %q", srv.Labels.Missing[0].Name)
	}
	// OS Version is new → added, Uptime modified (interval 3600→86400)
	if len(srv.Queries.Added) != 1 {
		t.Errorf("Servers: expected 1 added query, got %d", len(srv.Queries.Added))
	}
	if len(srv.Queries.Modified) != 1 {
		t.Errorf("Servers: expected 1 modified query, got %d", len(srv.Queries.Modified))
	} else {
		if _, ok := srv.Queries.Modified[0].Fields["interval"]; !ok {
			t.Error("expected interval field diff for Servers Uptime")
		}
	}

	// --- Mobile (new team) ---
	mob := findTeam(t, allResults, "Mobile")

	// All resources should be "added" since team is new
	if len(mob.Policies.Added) != 1 {
		t.Errorf("Mobile: expected 1 added policy, got %d", len(mob.Policies.Added))
	}
	if len(mob.Queries.Added) != 1 {
		t.Errorf("Mobile: expected 1 added query, got %d", len(mob.Queries.Added))
	}

	// Should have info message about new team
	foundNewTeamInfo := false
	for _, e := range mob.Errors {
		if strings.Contains(e, "does not exist in Fleet yet") {
			foundNewTeamInfo = true
			break
		}
	}
	if !foundNewTeamInfo {
		t.Error("expected info message about new team for Mobile")
	}
}

// TestDiffTestdataWorkstationsOnly verifies filtered diff for a single team.
func TestDiffTestdataWorkstationsOnly(t *testing.T) {
	root := testutil.TestdataRoot(t)

	proposed, err := parser.ParseRepo(root, "", "")
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}

	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Policies: []api.Policy{
					{Name: "[macOS] FileVault Enabled", Query: "SELECT 1 FROM disk_encryption WHERE encrypted = 1;", Platform: "darwin", Critical: true},
				},
			},
		},
		Labels: []api.Label{
			{ID: 1, Name: "macOS 14+", HostCount: 8512},
		},
	}

	results := Diff(current, proposed, "Workstations")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Team != "Workstations" {
		t.Errorf("team: got %q", results[0].Team)
	}
}

// TestDiffPolicyScenarios consolidates policy add/modify/delete/no-change tests.
func TestDiffPolicyScenarios(t *testing.T) {
	tests := []struct {
		name         string
		current      []api.Policy
		proposed     []parser.ParsedPolicy
		wantAdded    int
		wantModified int
		wantDeleted  int
		checkName    string // name to verify in the first result of the expected bucket
		checkFields  []string
		checkWarning bool
	}{
		{
			name:      "new policy added",
			current:   []api.Policy{{Name: "Existing", Platform: "darwin"}},
			proposed:  []parser.ParsedPolicy{{Name: "Existing", Platform: "darwin"}, {Name: "New Policy", Platform: "windows"}},
			wantAdded: 1, checkName: "New Policy",
		},
		{
			name:         "policy modified (query + critical)",
			current:      []api.Policy{{Name: "Disk Encryption", Query: "SELECT 1 FROM old;", Platform: "darwin", Critical: false}},
			proposed:     []parser.ParsedPolicy{{Name: "Disk Encryption", Query: "SELECT 1 FROM new;", Platform: "darwin", Critical: true}},
			wantModified: 1, checkName: "Disk Encryption", checkFields: []string{"query", "critical"},
		},
		{
			name:         "policy deleted with hosts",
			current:      []api.Policy{{Name: "Keep", Platform: "darwin"}, {Name: "Delete This", Platform: "darwin", PassingHostCount: 50}},
			proposed:     []parser.ParsedPolicy{{Name: "Keep", Platform: "darwin"}},
			wantDeleted:  1, checkName: "Delete This", checkWarning: true,
		},
		{
			name:     "no changes",
			current:  []api.Policy{{Name: "P1", Query: "SELECT 1;", Platform: "darwin"}},
			proposed: []parser.ParsedPolicy{{Name: "P1", Query: "SELECT 1;", Platform: "darwin"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := &api.FleetState{Teams: []api.Team{{ID: 1, Name: "T", Policies: tt.current}}}
			proposed := &parser.ParsedRepo{Teams: []parser.ParsedTeam{{Name: "T", Policies: tt.proposed}}}

			results := Diff(current, proposed, "")
			r := results[0]

			if len(r.Policies.Added) != tt.wantAdded {
				t.Errorf("added: got %d, want %d", len(r.Policies.Added), tt.wantAdded)
			}
			if len(r.Policies.Modified) != tt.wantModified {
				t.Errorf("modified: got %d, want %d", len(r.Policies.Modified), tt.wantModified)
			}
			if len(r.Policies.Deleted) != tt.wantDeleted {
				t.Errorf("deleted: got %d, want %d", len(r.Policies.Deleted), tt.wantDeleted)
			}

			if tt.checkName != "" {
				var found *ResourceChange
				for _, bucket := range [][]ResourceChange{r.Policies.Added, r.Policies.Modified, r.Policies.Deleted} {
					for i := range bucket {
						if bucket[i].Name == tt.checkName {
							found = &bucket[i]
						}
					}
				}
				if found == nil {
					t.Fatalf("expected resource %q not found", tt.checkName)
				}
				for _, f := range tt.checkFields {
					if _, ok := found.Fields[f]; !ok {
						t.Errorf("expected field diff %q", f)
					}
				}
				if tt.checkWarning && found.Warning == "" {
					t.Error("expected warning on deleted resource")
				}
			}
		})
	}
}

func TestDiffNewTeam(t *testing.T) {
	current := &api.FleetState{Teams: []api.Team{}, Labels: []api.Label{}}
	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{{Name: "New Team", Policies: []parser.ParsedPolicy{{Name: "Test Policy"}}}},
	}

	results := Diff(current, proposed, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Team != "New Team" {
		t.Errorf("team name: got %q", r.Team)
	}
	if len(r.Policies.Added) != 1 {
		t.Errorf("expected 1 added policy, got %d", len(r.Policies.Added))
	}

	foundInfo := false
	for _, e := range r.Errors {
		if strings.Contains(e, "does not exist in Fleet yet") {
			foundInfo = true
			if !strings.HasPrefix(e, "info:") {
				t.Errorf("new team message should be prefixed with 'info:', got %q", e)
			}
		}
	}
	if !foundInfo {
		t.Error("expected info message about new team")
	}
}

func TestDiffTeamFilter(t *testing.T) {
	current := &api.FleetState{
		Teams: []api.Team{{ID: 1, Name: "Alpha"}, {ID: 2, Name: "Beta"}},
	}
	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{Name: "Alpha", Policies: []parser.ParsedPolicy{{Name: "P1"}}},
			{Name: "Beta", Policies: []parser.ParsedPolicy{{Name: "P2"}}},
		},
	}

	results := Diff(current, proposed, "Alpha")
	if len(results) != 1 {
		t.Fatalf("expected 1 result with filter, got %d", len(results))
	}
	if results[0].Team != "Alpha" {
		t.Errorf("team: got %q", results[0].Team)
	}
}

func TestDiffLabelValidation(t *testing.T) {
	tests := []struct {
		name        string
		apiLabels   []api.Label
		yamlLabels  []string
		wantValid   int
		wantMissing int
	}{
		{
			name:        "valid and missing labels on new policy",
			apiLabels:   []api.Label{{Name: "Managed Devices", HostCount: 24}},
			yamlLabels:  []string{"Managed Devices", "Missing Scope Label"},
			wantValid:   1,
			wantMissing: 1,
		},
		{
			name:       "all labels valid on new policy",
			apiLabels:  []api.Label{{Name: "A"}, {Name: "B"}},
			yamlLabels: []string{"A", "B"},
			wantValid:  2,
		},
		{
			name:        "all labels missing on new policy",
			apiLabels:   []api.Label{},
			yamlLabels:  []string{"Ghost"},
			wantMissing: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Policy "P" is new (not in current) so it appears in the diff,
			// which means its labels get validated.
			current := &api.FleetState{
				Teams:  []api.Team{{ID: 1, Name: "T", Policies: []api.Policy{}}},
				Labels: tt.apiLabels,
			}
			proposed := &parser.ParsedRepo{
				Teams: []parser.ParsedTeam{{
					Name:     "T",
					Policies: []parser.ParsedPolicy{{Name: "P", LabelsIncludeAny: tt.yamlLabels}},
				}},
			}

			results := Diff(current, proposed, "")
			r := results[0]

			if len(r.Labels.Valid) != tt.wantValid {
				t.Errorf("valid labels: got %d, want %d", len(r.Labels.Valid), tt.wantValid)
			}
			if len(r.Labels.Missing) != tt.wantMissing {
				t.Errorf("missing labels: got %d, want %d", len(r.Labels.Missing), tt.wantMissing)
			}
		})
	}
}

func TestDiffLabelValidationSkipsUnchangedPolicies(t *testing.T) {
	// Policy exists in both current and proposed with identical config.
	// Its labels should NOT appear in the label validation output.
	current := &api.FleetState{
		Teams: []api.Team{{
			ID:       1,
			Name:     "T",
			Policies: []api.Policy{{Name: "Unchanged", Query: "SELECT 1;", Platform: "darwin"}},
		}},
		Labels: []api.Label{{Name: "Some Label", HostCount: 10}},
	}
	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{{
			Name: "T",
			Policies: []parser.ParsedPolicy{{
				Name:             "Unchanged",
				Query:            "SELECT 1;",
				Platform:         "darwin",
				LabelsIncludeAny: []string{"Some Label"},
			}},
		}},
	}

	results := Diff(current, proposed, "")
	r := results[0]

	if len(r.Labels.Valid) != 0 {
		t.Errorf("unchanged policy labels should not appear: got %d valid", len(r.Labels.Valid))
	}
	if len(r.Labels.Missing) != 0 {
		t.Errorf("unchanged policy labels should not appear: got %d missing", len(r.Labels.Missing))
	}
}

func TestDiffQueryChanges(t *testing.T) {
	tests := []struct {
		name         string
		current      []api.Query
		proposed     []parser.ParsedQuery
		wantAdded    int
		wantModified int
		wantDeleted  int
		checkField   string
	}{
		{
			name:         "interval changed + new query",
			current:      []api.Query{{Name: "Inventory", Query: "SELECT * FROM disk_info;", Interval: 3600, Platform: "darwin"}},
			proposed:     []parser.ParsedQuery{{Name: "Inventory", Query: "SELECT * FROM disk_info;", Interval: 7200, Platform: "darwin"}, {Name: "New", Query: "SELECT 1;", Interval: 300, Platform: "windows"}},
			wantAdded:    1,
			wantModified: 1,
			checkField:   "interval",
		},
		{
			name:        "query deleted",
			current:     []api.Query{{Name: "Old", Query: "SELECT 1;", Interval: 3600}},
			proposed:    []parser.ParsedQuery{},
			wantDeleted: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := &api.FleetState{Teams: []api.Team{{ID: 1, Name: "T", Queries: tt.current}}}
			proposed := &parser.ParsedRepo{Teams: []parser.ParsedTeam{{Name: "T", Queries: tt.proposed}}}

			results := Diff(current, proposed, "")
			r := results[0]

			if len(r.Queries.Added) != tt.wantAdded {
				t.Errorf("added: got %d, want %d", len(r.Queries.Added), tt.wantAdded)
			}
			if len(r.Queries.Modified) != tt.wantModified {
				t.Errorf("modified: got %d, want %d", len(r.Queries.Modified), tt.wantModified)
			}
			if len(r.Queries.Deleted) != tt.wantDeleted {
				t.Errorf("deleted: got %d, want %d", len(r.Queries.Deleted), tt.wantDeleted)
			}
			if tt.checkField != "" && len(r.Queries.Modified) > 0 {
				if _, ok := r.Queries.Modified[0].Fields[tt.checkField]; !ok {
					t.Errorf("expected %q field diff", tt.checkField)
				}
			}
		})
	}
}

func TestResourceDiffTotal(t *testing.T) {
	rd := ResourceDiff{
		Added:    []ResourceChange{{Name: "a"}, {Name: "b"}},
		Modified: []ResourceChange{{Name: "c"}},
		Deleted:  []ResourceChange{{Name: "d"}},
	}
	if rd.Total() != 4 {
		t.Errorf("total: got %d, want 4", rd.Total())
	}
}

func TestResourceDiffIsEmpty(t *testing.T) {
	rd := ResourceDiff{}
	if !rd.IsEmpty() {
		t.Error("empty diff should report IsEmpty")
	}

	rd.Added = append(rd.Added, ResourceChange{Name: "a"})
	if rd.IsEmpty() {
		t.Error("non-empty diff should not report IsEmpty")
	}
}

func TestDiffSoftwarePackageAddedDeleted(t *testing.T) {
	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Software: api.TeamSoftware{
					Packages: []api.TeamSoftwarePackage{
						{ReferencedYAMLPath: "software/mac/old/old.yml", URL: "https://example.com/old.pkg"},
						{ReferencedYAMLPath: "software/mac/keep/keep.yml", URL: "https://example.com/keep.pkg"},
					},
				},
			},
		},
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "Workstations",
				Software: parser.ParsedSoftware{
					Packages: []parser.ParsedSoftwarePackage{
						{RefPath: "software/mac/keep/keep.yml", URL: "https://example.com/keep.pkg"},
						{RefPath: "software/mac/new/new.yml", URL: "https://example.com/new.pkg"},
					},
				},
			},
		},
	}

	results := Diff(current, proposed, "")
	r := results[0]

	if len(r.Software.Added) != 1 {
		t.Fatalf("expected 1 added package, got %d", len(r.Software.Added))
	}
	if r.Software.Added[0].Name != "software/mac/new/new.yml" {
		t.Fatalf("unexpected added package: %q", r.Software.Added[0].Name)
	}
	if len(r.Software.Deleted) != 1 {
		t.Fatalf("expected 1 deleted package, got %d", len(r.Software.Deleted))
	}
	if r.Software.Deleted[0].Name != "software/mac/old/old.yml" {
		t.Fatalf("unexpected deleted package: %q", r.Software.Deleted[0].Name)
	}
}

func TestDiffSoftwarePackageModified(t *testing.T) {
	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Software: api.TeamSoftware{
					Packages: []api.TeamSoftwarePackage{
						{
							ReferencedYAMLPath: "software/mac/slack/slack.yml",
							URL:                "https://example.com/slack-1.pkg",
							HashSHA256:         "abc123",
							SelfService:        false,
						},
					},
				},
			},
		},
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "Workstations",
				Software: parser.ParsedSoftware{
					Packages: []parser.ParsedSoftwarePackage{
						{
							RefPath:     "software/mac/slack/slack.yml",
							URL:         "https://example.com/slack-2.pkg",
							HashSHA256:  "def456",
							SelfService: true,
						},
					},
				},
			},
		},
	}

	results := Diff(current, proposed, "")
	r := results[0]

	if len(r.Software.Modified) != 1 {
		t.Fatalf("expected 1 modified package, got %d", len(r.Software.Modified))
	}
	mod := r.Software.Modified[0]
	if mod.Name != "software/mac/slack/slack.yml" {
		t.Fatalf("unexpected modified package name: %q", mod.Name)
	}
	if _, ok := mod.Fields["url"]; !ok {
		t.Fatal("expected url field diff")
	}
	if _, ok := mod.Fields["hash_sha256"]; !ok {
		t.Fatal("expected hash_sha256 field diff")
	}
	if _, ok := mod.Fields["self_service"]; !ok {
		t.Fatal("expected self_service field diff")
	}
}

func TestDiffSoftwareFleetAndAppStore(t *testing.T) {
	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Software: api.TeamSoftware{
					FleetMaintained: []api.TeamFleetApp{
						{Slug: "slack/darwin", SelfService: false},
						{Slug: "zoom/darwin", SelfService: true},
					},
					AppStoreApps: []api.TeamAppStoreApp{
						{AppStoreID: "111", SelfService: true},
						{AppStoreID: "222", SelfService: false},
					},
				},
			},
		},
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "Workstations",
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{
						{Slug: "slack/darwin", SelfService: true},  // modified
						{Slug: "notion/darwin", SelfService: true}, // added
					},
					AppStoreApps: []parser.ParsedAppStoreApp{
						{AppStoreID: "111", SelfService: true},  // unchanged
						{AppStoreID: "333", SelfService: false}, // added
					},
				},
			},
		},
	}

	results := Diff(current, proposed, "")
	r := results[0]

	// Expect at least one add/delete/modify across fleet + app store
	if r.Software.IsEmpty() {
		t.Fatal("expected software changes, got empty diff")
	}
}

func TestDiffFleetMaintainedAppsNullAPIShowsAdded(t *testing.T) {
	// When the API returns fleet_maintained_apps: null and inference can't
	// reconstruct the state (no catalog/titles), the proposed apps should
	// appear as "added" — this is the honest diff.
	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Software: api.TeamSoftware{
					AppStoreApps: []api.TeamAppStoreApp{
						{AppStoreID: "111", SelfService: true},
					},
					FleetMaintained: nil, // API returned null
				},
			},
		},
		// No catalog and no software titles -> inference returns nil
		FleetMaintainedCatalog: nil,
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "Workstations",
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{
						{Slug: "zoom/darwin", SelfService: true},
					},
					AppStoreApps: []parser.ParsedAppStoreApp{
						{AppStoreID: "111", SelfService: true},
					},
				},
			},
		},
	}

	results := Diff(current, proposed, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]

	// With no inference data, the fleet app should show as "added".
	if len(r.Software.Added) != 1 {
		t.Fatalf("expected 1 added fleet app, got %d added", len(r.Software.Added))
	}
	if r.Software.Added[0].Name != "fleet app zoom/darwin" {
		t.Fatalf("unexpected added name: %q", r.Software.Added[0].Name)
	}

	// No info/skip messages should be emitted.
	for _, e := range r.Errors {
		if strings.Contains(e, "fleet_maintained") {
			t.Fatalf("unexpected fleet_maintained error message: %q", e)
		}
	}
}

func TestDiffFleetMaintainedAppsInferenceFromTitles(t *testing.T) {
	current := &api.FleetState{
		FleetMaintainedCatalog: []api.FleetMaintainedApp{
			{
				Slug:     "google-chrome/darwin",
				Name:     "Google Chrome",
				Platform: "darwin",
			},
		},
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Software: api.TeamSoftware{
					// Custom package URL should be excluded from maintained-app inference.
					Packages: []api.TeamSoftwarePackage{
						{URL: "https://downloads.example.com/custom-agent.pkg"},
					},
					FleetMaintained: nil, // API currently returns null here
				},
				SoftwareTitles: []api.SoftwareTitle{
					{
						Name:   "Google Chrome",
						Source: "apps",
						SoftwarePackage: &api.SoftwareTitlePackageMeta{
							PackageURL:  "https://fleet-maintained.example/google-chrome.pkg",
							Platform:    "darwin",
							SelfService: true,
						},
					},
					{
						Name:   "Custom Agent",
						Source: "apps",
						SoftwarePackage: &api.SoftwareTitlePackageMeta{
							PackageURL:  "https://downloads.example.com/custom-agent.pkg",
							Platform:    "darwin",
							SelfService: true,
						},
					},
				},
			},
		},
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "Workstations",
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{
						{Slug: "google-chrome/darwin", SelfService: false},
					},
				},
			},
		},
	}

	results := Diff(current, proposed, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]

	// Inferred current self_service=true, proposed=false -> modified.
	if len(r.Software.Modified) != 1 {
		t.Fatalf("expected 1 modified fleet app from inference, got %d", len(r.Software.Modified))
	}
	if r.Software.Modified[0].Name != "fleet app google-chrome/darwin" {
		t.Fatalf("unexpected modified name: %q", r.Software.Modified[0].Name)
	}
	if _, ok := r.Software.Modified[0].Fields["self_service"]; !ok {
		t.Fatal("expected self_service diff for inferred fleet app")
	}

	// No info/skip messages should be emitted — inference is silent.
	for _, e := range r.Errors {
		if strings.Contains(e, "fleet_maintained") {
			t.Fatalf("unexpected fleet_maintained message: %q", e)
		}
	}
}

// TestDiffProfilesMatchByContentName verifies that profiles are matched by
// the name extracted from file content (e.g., PayloadDisplayName), not by
// filename. This is the exact bug that caused false add/delete diffs when
// a .mobileconfig file was renamed but its PayloadDisplayName stayed the same.
func TestDiffProfilesMatchByContentName(t *testing.T) {
	// API has a profile named "fleet_orbit-allowfulldiskaccess"
	current := []api.Profile{
		{Name: "fleet_orbit-allowfulldiskaccess"},
		{Name: "wifi-corporate"},
	}

	// YAML has a file named "renamed-fulldisk.mobileconfig" but its
	// PayloadDisplayName is "fleet_orbit-allowfulldiskaccess" (matches API).
	// Also has "wifi-corporate" that matches.
	proposed := []parser.ParsedProfile{
		{Path: "/repo/profiles/mac/renamed-fulldisk.mobileconfig", Name: "fleet_orbit-allowfulldiskaccess", Platform: "darwin"},
		{Path: "/repo/profiles/mac/wifi-corporate.mobileconfig", Name: "wifi-corporate", Platform: "darwin"},
	}

	diff, warnings := diffProfiles(current, proposed)
	if len(warnings) > 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if !diff.IsEmpty() {
		t.Errorf("expected no profile changes (names match by content), got added=%d modified=%d deleted=%d",
			len(diff.Added), len(diff.Modified), len(diff.Deleted))
	}
}

// TestDiffProfilesDetectsRealChanges verifies that genuine profile additions
// and deletions are still detected correctly.
func TestDiffProfilesDetectsRealChanges(t *testing.T) {
	current := []api.Profile{
		{Name: "old-profile"},
		{Name: "unchanged-profile"},
	}

	proposed := []parser.ParsedProfile{
		{Path: "/repo/profiles/mac/unchanged-profile.mobileconfig", Name: "unchanged-profile", Platform: "darwin"},
		{Path: "/repo/profiles/mac/new-profile.mobileconfig", Name: "new-profile", Platform: "darwin"},
	}

	diff, _ := diffProfiles(current, proposed)
	if len(diff.Added) != 1 || diff.Added[0].Name != "new-profile" {
		t.Errorf("expected 1 added profile 'new-profile', got %v", diff.Added)
	}
	if len(diff.Deleted) != 1 || diff.Deleted[0].Name != "old-profile" {
		t.Errorf("expected 1 deleted profile 'old-profile', got %v", diff.Deleted)
	}
}

// --- Global config diff tests ---

func TestDiffGlobalConfig(t *testing.T) {
	tests := []struct {
		name            string
		apiConfig       map[string]any
		proposedOrg     map[string]any
		wantChanges     int
		wantKey         string // key to find in changes (empty = skip check)
		wantKeyAbsent   string // key that must NOT appear in changes
		wantOld, wantNew string
	}{
		{
			name:      "new key added",
			apiConfig: map[string]any{"org_info": map[string]any{"org_name": "Acme Corp"}},
			proposedOrg: map[string]any{"org_info": map[string]any{
				"org_name": "Acme Corp", "org_logo_url": "https://example.com/logo.png",
			}},
			wantChanges: 1, wantKey: "org_info.org_logo_url", wantNew: "https://example.com/logo.png",
		},
		{
			name:      "value modified",
			apiConfig: map[string]any{"server_settings": map[string]any{"server_url": "https://fleet.old.com", "live_query_disabled": false}},
			proposedOrg: map[string]any{"server_settings": map[string]any{
				"server_url": "https://fleet.new.com", "live_query_disabled": false,
			}},
			wantChanges: 1, wantKey: "server_settings.server_url",
		},
		{
			name:      "env var placeholder skipped",
			apiConfig: map[string]any{"integrations": map[string]any{"jira": map[string]any{"api_token": "actual-token"}}},
			proposedOrg: map[string]any{"integrations": map[string]any{"jira": map[string]any{
				"api_token": "$JIRA_API_TOKEN",
			}}},
			wantChanges: 0, wantKeyAbsent: "integrations.jira.api_token",
		},
		{
			name:        "no changes when identical",
			apiConfig:   map[string]any{"org_info": map[string]any{"org_name": "Same Corp"}},
			proposedOrg: map[string]any{"org_info": map[string]any{"org_name": "Same Corp"}},
			wantChanges: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := &api.FleetState{Config: tt.apiConfig}
			proposed := &parser.ParsedRepo{
				Global: &parser.ParsedGlobal{OrgSettings: tt.proposedOrg},
				Teams:  []parser.ParsedTeam{},
			}

			results := Diff(current, proposed, "")
			global := findTeam(t, results, "(global)")

			if len(global.Config) != tt.wantChanges {
				t.Fatalf("config changes: got %d, want %d: %+v", len(global.Config), tt.wantChanges, global.Config)
			}
			if tt.wantKey != "" {
				found := false
				for _, c := range global.Config {
					if c.Key == tt.wantKey {
						found = true
						if tt.wantOld != "" && c.Old != tt.wantOld {
							t.Errorf("old: got %q, want %q", c.Old, tt.wantOld)
						}
						if tt.wantNew != "" && c.New != tt.wantNew {
							t.Errorf("new: got %q, want %q", c.New, tt.wantNew)
						}
					}
				}
				if !found {
					t.Errorf("expected key %q in config changes", tt.wantKey)
				}
			}
			if tt.wantKeyAbsent != "" {
				for _, c := range global.Config {
					if c.Key == tt.wantKeyAbsent {
						t.Errorf("key %q should not appear in config changes", tt.wantKeyAbsent)
					}
				}
			}
		})
	}
}

func TestDiffGlobalPoliciesAndQueries(t *testing.T) {
	current := &api.FleetState{
		Config:         map[string]any{},
		GlobalPolicies: []api.Policy{{Name: "Existing", Query: "SELECT 1;", Platform: "darwin"}},
		GlobalQueries:  []api.Query{{Name: "Existing Q", Query: "SELECT hostname FROM system_info;", Interval: 3600, Platform: "darwin"}},
	}
	proposed := &parser.ParsedRepo{
		Global: &parser.ParsedGlobal{
			Policies: []parser.ParsedPolicy{
				{Name: "Existing", Query: "SELECT 1;", Platform: "darwin"},
				{Name: "New Global Policy", Query: "SELECT 1 FROM os_version;", Platform: "linux"},
			},
			Queries: []parser.ParsedQuery{
				{Name: "Existing Q", Query: "SELECT hostname FROM system_info;", Interval: 7200, Platform: "darwin"},
			},
		},
		Teams: []parser.ParsedTeam{},
	}

	results := Diff(current, proposed, "")
	global := findTeam(t, results, "(global)")

	if len(global.Policies.Added) != 1 {
		t.Errorf("expected 1 added global policy, got %d", len(global.Policies.Added))
	}
	if len(global.Queries.Modified) != 1 {
		t.Errorf("expected 1 modified global query, got %d", len(global.Queries.Modified))
	}
}

func TestDiffGlobalSkippedWithTeamFilter(t *testing.T) {
	current := &api.FleetState{
		Config: map[string]any{"org_info": map[string]any{"org_name": "Old"}},
		Teams:  []api.Team{{ID: 1, Name: "Alpha"}},
	}
	proposed := &parser.ParsedRepo{
		Global: &parser.ParsedGlobal{OrgSettings: map[string]any{"org_info": map[string]any{"org_name": "New"}}},
		Teams:  []parser.ParsedTeam{{Name: "Alpha"}},
	}

	results := Diff(current, proposed, "Alpha")
	for _, r := range results {
		if r.Team == "(global)" {
			t.Error("global result should be skipped when team filter is set")
		}
	}
}

func TestFlattenMap(t *testing.T) {
	m := map[string]any{
		"a": "1",
		"b": map[string]any{
			"c": "2",
			"d": map[string]any{
				"e": "3",
			},
		},
	}

	got := make(map[string]string)
	flattenMap(m, "", func(key, val string) {
		got[key] = val
	})

	expected := map[string]string{
		"a":     "1",
		"b.c":   "2",
		"b.d.e": "3",
	}

	for k, v := range expected {
		if got[k] != v {
			t.Errorf("flattenMap: key %q = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(expected) {
		t.Errorf("flattenMap: got %d keys, want %d", len(got), len(expected))
	}
}

func TestContainsEnvVar(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"$FLEET_SECRET_WIFI", true},
		{"$SSO_METADATA", true},
		{"https://example.com", false},
		{"plain text", false},
		{"value with $VAR inside", true},
		{"", false},
	}

	for _, tt := range tests {
		if got := containsEnvVar(tt.input); got != tt.want {
			t.Errorf("containsEnvVar(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestNormalizeWS(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"  hello  ", "hello"},
		{"hello\nworld", "hello world"},
		{"hello\n  world\n\ttab", "hello world tab"},
		{"SELECT 1\n   FROM table\n   WHERE x = 1;", "SELECT 1 FROM table WHERE x = 1;"},
		{"", ""},
		{"  \n\t  ", ""},
	}
	for _, tc := range cases {
		got := normalizeWS(tc.in)
		if got != tc.want {
			t.Errorf("normalizeWS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// findTeam locates a DiffResult by team name, failing the test if not found.
func findTeam(t *testing.T, results []DiffResult, name string) *DiffResult {
	t.Helper()
	for i := range results {
		if results[i].Team == name {
			return &results[i]
		}
	}
	t.Fatalf("team %q not found in results", name)
	return nil
}
