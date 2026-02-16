//go:build ignore

// gen_screenshot prints representative fleet-plan output for screenshot capture.
// Uses the same testdata fixtures and mock API state as differ_test.go.
//
// Usage (two-step for termshot):
//   go run ./internal/output/gen_screenshot.go > /tmp/fleet-plan-raw.txt
//   termshot --raw-read /tmp/fleet-plan-raw.txt -f assets/screenshot.png -C 100
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/diff"
	"github.com/TsekNet/fleet-plan/internal/output"
	"github.com/TsekNet/fleet-plan/internal/parser"
)

// testdataRoot finds the testdata/ directory relative to this source file.
func testdataRoot() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("could not determine source file path")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find repo root (no go.mod)")
		}
		dir = parent
	}
	return filepath.Join(dir, "testdata")
}

func main() {
	root := testdataRoot()

	proposed, err := parser.ParseRepo(root, "", "")
	if err != nil {
		log.Fatalf("ParseRepo: %v", err)
	}

	// Same mock API state as TestDiffTestdataAgainstMockAPI in differ_test.go.
	current := &api.FleetState{
		Teams: []api.Team{
			{
				ID:   1,
				Name: "Workstations",
				Policies: []api.Policy{
					{Name: "[macOS] FileVault Enabled", Query: "SELECT 1 FROM disk_encryption WHERE encrypted = 1;", Platform: "darwin", Critical: true},
					{Name: "[Windows] Legacy AV Check", Query: "SELECT 1;", Platform: "windows", PassingHostCount: 100},
				},
				Queries: []api.Query{
					{Name: "Disk Usage", Query: "SELECT path, type, blocks_size, blocks, blocks_free, blocks_available FROM mounts WHERE type != 'tmpfs';", Interval: 3600, Platform: "darwin,windows,linux"},
					{Name: "Network Interfaces", Query: "SELECT * FROM interface_addresses;", Interval: 3600, Platform: "darwin,windows,linux"},
				},
				Software: api.TeamSoftware{
					Packages: []api.TeamSoftwarePackage{
						{ReferencedYAMLPath: "software/mac/slack/slack.yml", URL: "https://downloads.slack-edge.com/desktop-releases/macos/4.41.47/Slack-4.41.47-macOS.dmg", HashSHA256: "abc123def456789"},
						{ReferencedYAMLPath: "software/mac/old-agent/old-agent.yml", URL: "https://example.com/old-agent.pkg"},
					},
				},
				Profiles: []api.Profile{
					{Name: "fleet_orbit-allowfulldiskaccess"},
				},
			},
			{
				ID:   2,
				Name: "Servers",
				Queries: []api.Query{
					{Name: "System Uptime", Query: "SELECT total_seconds, days, hours FROM uptime;", Interval: 3600, Platform: "darwin,windows,linux"},
				},
			},
		},
		Labels: []api.Label{
			{ID: 1, Name: "macOS 14+", HostCount: 8512},
			{ID: 2, Name: "Windows 11", HostCount: 3200},
		},
	}

	results := diff.Diff(current, proposed, "")
	fmt.Println(output.RenderDiffTerminal(results, false))
}
