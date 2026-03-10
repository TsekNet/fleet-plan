package diff

import (
	"testing"

	"github.com/TsekNet/fleet-plan/internal/parser"
)

func TestParsedRepoToFleetState(t *testing.T) {
	repo := &parser.ParsedRepo{
		Global: &parser.ParsedGlobal{
			OrgSettings: map[string]any{"org_info": map[string]any{"org_name": "Acme"}},
			Policies:    []parser.ParsedPolicy{{Name: "GP1", Query: "SELECT 1;", Platform: "darwin"}},
			Queries:     []parser.ParsedQuery{{Name: "GQ1", Query: "SELECT hostname FROM system_info;", Interval: 3600}},
		},
		Teams: []parser.ParsedTeam{
			{
				Name:     "Workstations",
				Policies: []parser.ParsedPolicy{{Name: "P1", Query: "SELECT 1;", Platform: "darwin"}},
				Queries:  []parser.ParsedQuery{{Name: "Q1", Query: "SELECT 1;", Interval: 300}},
				Software: parser.ParsedSoftware{
					Packages:        []parser.ParsedSoftwarePackage{{RefPath: "software/slack.yml", URL: "https://example.com/slack.pkg"}},
					FleetMaintained: []parser.ParsedFleetApp{{Slug: "zoom/darwin", SelfService: true}},
					AppStoreApps:    []parser.ParsedAppStoreApp{{AppStoreID: "111", SelfService: true}},
				},
			},
		},
	}

	state := ParsedRepoToFleetState(repo)

	if state.Config == nil {
		t.Fatal("expected non-nil Config")
	}
	if len(state.GlobalPolicies) != 1 || state.GlobalPolicies[0].Name != "GP1" {
		t.Errorf("global policies: got %v", state.GlobalPolicies)
	}
	if len(state.GlobalQueries) != 1 || state.GlobalQueries[0].Name != "GQ1" {
		t.Errorf("global queries: got %v", state.GlobalQueries)
	}
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}

	team := state.Teams[0]
	if team.Name != "Workstations" {
		t.Errorf("team name: got %q", team.Name)
	}
	if len(team.Policies) != 1 {
		t.Errorf("policies: got %d", len(team.Policies))
	}
	if len(team.Queries) != 1 {
		t.Errorf("queries: got %d", len(team.Queries))
	}
	if len(team.Software.Packages) != 1 {
		t.Errorf("packages: got %d", len(team.Software.Packages))
	}
	if len(team.Software.FleetMaintained) != 1 || team.Software.FleetMaintained[0].Slug != "zoom/darwin" {
		t.Errorf("fleet maintained: got %v", team.Software.FleetMaintained)
	}
	if len(team.Software.AppStoreApps) != 1 {
		t.Errorf("app store apps: got %d", len(team.Software.AppStoreApps))
	}
}

func TestDiffBaseRepoNoChanges(t *testing.T) {
	repo := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name:     "T",
				Policies: []parser.ParsedPolicy{{Name: "P1", Query: "SELECT 1;", Platform: "darwin"}},
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{{Slug: "zoom/darwin", SelfService: true}},
				},
			},
		},
	}

	state := ParsedRepoToFleetState(repo)
	results := Diff(state, repo, nil, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Policies.IsEmpty() {
		t.Errorf("expected no policy changes, got added=%d modified=%d deleted=%d",
			len(r.Policies.Added), len(r.Policies.Modified), len(r.Policies.Deleted))
	}
	if !r.Software.IsEmpty() {
		t.Errorf("expected no software changes, got added=%d modified=%d deleted=%d",
			len(r.Software.Added), len(r.Software.Modified), len(r.Software.Deleted))
	}
}

func TestDiffBaseRepoDetectsAdditions(t *testing.T) {
	baseRepo := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "T",
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{
						{Slug: "zoom/darwin", SelfService: true},
					},
				},
			},
		},
	}

	proposed := &parser.ParsedRepo{
		Teams: []parser.ParsedTeam{
			{
				Name: "T",
				Software: parser.ParsedSoftware{
					FleetMaintained: []parser.ParsedFleetApp{
						{Slug: "zoom/darwin", SelfService: true},
						{Slug: "slack/darwin", SelfService: true},
					},
					Packages: []parser.ParsedSoftwarePackage{
						{RefPath: "software/new.yml", URL: "https://example.com/new.pkg"},
					},
				},
			},
		},
	}

	state := ParsedRepoToFleetState(baseRepo)
	results := Diff(state, proposed, nil, nil)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]

	if len(r.Software.Added) != 2 {
		t.Errorf("expected 2 added software, got %d: %v", len(r.Software.Added), r.Software.Added)
	}
	if len(r.Software.Deleted) != 0 {
		t.Errorf("expected 0 deleted, got %d", len(r.Software.Deleted))
	}
}
