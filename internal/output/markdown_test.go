package output

import (
	"strings"
	"testing"

	"github.com/TsekNet/fleet-plan/internal/diff"
)

func TestRenderDiffMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		results  []diff.DiffResult
		wantAll  []string
		wantNone []string
	}{
		{
			name:    "empty results",
			results: nil,
			wantAll: []string{"## fleet-plan", "0 added, 0 modified, 0 deleted"},
		},
		{
			name: "added policy",
			results: []diff.DiffResult{{
				Team: "Workstations",
				Policies: diff.ResourceDiff{
					Added: []diff.ResourceChange{{Name: "[macOS] FileVault"}},
				},
			}},
			wantAll: []string{
				"### Workstations",
				"**Policies:**",
				"`[macOS] FileVault`",
				"1 added",
			},
		},
		{
			name: "added with host count",
			results: []diff.DiffResult{{
				Team: "Servers",
				Policies: diff.ResourceDiff{
					Added: []diff.ResourceChange{{Name: "Test", HostCount: 50}},
				},
			}},
			wantAll: []string{"~50 hosts"},
		},
		{
			name: "modified query with fields",
			results: []diff.DiffResult{{
				Team: "Endpoints",
				Queries: diff.ResourceDiff{
					Modified: []diff.ResourceChange{{
						Name:   "Disk Usage",
						Fields: map[string]diff.FieldDiff{"interval": {Old: "3600", New: "7200"}},
					}},
				},
			}},
			wantAll: []string{
				"`Disk Usage`",
				"`interval`",
				"`3600`",
				"`7200`",
			},
		},
		{
			name: "deleted with warning",
			results: []diff.DiffResult{{
				Team: "Mobile",
				Software: diff.ResourceDiff{
					Deleted: []diff.ResourceChange{{Name: "OldApp", Warning: "in use by 10 hosts"}},
				},
			}},
			wantAll: []string{"`OldApp`", "in use by 10 hosts"},
		},
		{
			name: "errors rendered",
			results: []diff.DiffResult{{
				Team:   "BadTeam",
				Errors: []string{"parse error in team YAML"},
			}},
			wantAll: []string{"parse error in team YAML"},
		},
		{
			name: "labels section",
			results: []diff.DiffResult{{
				Team: "Workstations",
				Labels: diff.LabelValidation{
					Valid:   []diff.LabelRef{{Name: "x86-64", HostCount: 100}},
					Missing: []diff.LabelRef{{Name: "Ghost Label", ReferencedBy: "policy: Test"}},
				},
			}},
			wantAll: []string{
				"### Labels",
				"`x86-64`",
				"100 hosts",
				"`Ghost Label`",
				"NOT FOUND",
			},
			wantNone: []string{"unique labels"},
		},
		{
			name: "multiple teams aggregated summary",
			results: []diff.DiffResult{
				{
					Team:     "TeamA",
					Policies: diff.ResourceDiff{Added: []diff.ResourceChange{{Name: "P1"}, {Name: "P2"}}},
				},
				{
					Team:    "TeamB",
					Queries: diff.ResourceDiff{Deleted: []diff.ResourceChange{{Name: "Q1"}}},
				},
			},
			wantAll: []string{"2 added", "1 deleted"},
		},
		{
			name: "all resource types",
			results: []diff.DiffResult{{
				Team:     "Full",
				Policies: diff.ResourceDiff{Added: []diff.ResourceChange{{Name: "P"}}},
				Queries:  diff.ResourceDiff{Modified: []diff.ResourceChange{{Name: "Q", Fields: map[string]diff.FieldDiff{"f": {Old: "a", New: "b"}}}}},
				Software: diff.ResourceDiff{Deleted: []diff.ResourceChange{{Name: "S"}}},
				Profiles: diff.ResourceDiff{Added: []diff.ResourceChange{{Name: "Prof"}}},
			}},
			wantAll: []string{
				"**Policies:**", "**Queries:**", "**Software:**", "**Profiles:**",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := RenderDiffMarkdown(tt.results)
			for _, want := range tt.wantAll {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q in output, got:\n%s", want, out)
				}
			}
			for _, notWant := range tt.wantNone {
				if strings.Contains(out, notWant) {
					t.Errorf("did not expect %q in output, got:\n%s", notWant, out)
				}
			}
		})
	}
}
