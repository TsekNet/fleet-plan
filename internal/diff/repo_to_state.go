package diff

import (
	"github.com/TsekNet/fleet-plan/internal/api"
	"github.com/TsekNet/fleet-plan/internal/parser"
)

// ParsedRepoToFleetState converts a ParsedRepo (from YAML) into a FleetState
// so it can be used as the "current" side of a Diff. This enables YAML-to-YAML
// diffing (MR branch vs base branch) without querying the Fleet API.
func ParsedRepoToFleetState(repo *parser.ParsedRepo) *api.FleetState {
	state := &api.FleetState{}

	if repo.Global != nil {
		state.Config = repo.Global.OrgSettings
		for _, p := range repo.Global.Policies {
			state.GlobalPolicies = append(state.GlobalPolicies, policyToAPI(p))
		}
		for _, q := range repo.Global.Queries {
			state.GlobalQueries = append(state.GlobalQueries, queryToAPI(q))
		}
	}

	for _, pt := range repo.Teams {
		team := api.Team{
			Name: pt.Name,
		}

		for _, p := range pt.Policies {
			team.Policies = append(team.Policies, policyToAPI(p))
		}
		for _, q := range pt.Queries {
			team.Queries = append(team.Queries, queryToAPI(q))
		}

		for _, pkg := range pt.Software.Packages {
			team.Software.Packages = append(team.Software.Packages, api.TeamSoftwarePackage{
				ReferencedYAMLPath: pkg.RefPath,
				URL:                pkg.URL,
				HashSHA256:         pkg.HashSHA256,
				SelfService:        pkg.SelfService,
			})
		}
		for _, fa := range pt.Software.FleetMaintained {
			team.Software.FleetMaintained = append(team.Software.FleetMaintained, api.TeamFleetApp{
				Slug:        fa.Slug,
				SelfService: fa.SelfService,
			})
		}
		for _, aa := range pt.Software.AppStoreApps {
			team.Software.AppStoreApps = append(team.Software.AppStoreApps, api.TeamAppStoreApp{
				AppStoreID:  aa.AppStoreID,
				SelfService: aa.SelfService,
			})
		}

		for _, pr := range pt.Profiles {
			team.Profiles = append(team.Profiles, api.Profile{
				Name: pr.Name,
			})
		}

		state.Teams = append(state.Teams, team)
	}

	return state
}

func policyToAPI(p parser.ParsedPolicy) api.Policy {
	return api.Policy{
		Name:        p.Name,
		Query:       p.Query,
		Description: p.Description,
		Resolution:  p.Resolution,
		Platform:    p.Platform,
		Critical:    p.Critical,
	}
}

func queryToAPI(q parser.ParsedQuery) api.Query {
	return api.Query{
		Name:     q.Name,
		Query:    q.Query,
		Interval: q.Interval,
		Platform: q.Platform,
		Logging:  q.Logging,
	}
}
