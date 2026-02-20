// Package api provides a read-only Fleet REST API client. It only performs GET
// requests and enforces this at the type level -- there are no exported methods
// that perform writes. This is intentional and permanent.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Client is a read-only Fleet REST API client.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient creates a new read-only Fleet API client.
// Returns an error if the URL uses plain HTTP (token would be sent in cleartext).
func NewClient(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")

	if strings.HasPrefix(strings.ToLower(baseURL), "http://") && os.Getenv("FLEET_PLAN_INSECURE") != "1" {
		return nil, fmt.Errorf("refusing to send API token over plain HTTP (%s)\nUse https:// or set FLEET_PLAN_INSECURE=1 to override", baseURL)
	}

	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// get performs a GET request and decodes JSON into dest.
func (c *Client) get(ctx context.Context, path string, query url.Values, dest any) error {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request to %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return &HTTPError{
			StatusCode: resp.StatusCode,
			URL:        u,
			Body:       string(body),
		}
	}

	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("decoding response from %s: %w", path, err)
	}

	return nil
}

// HTTPError represents a non-200 HTTP response.
type HTTPError struct {
	StatusCode int
	URL        string
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d from %s: %s", e.StatusCode, e.URL, e.Body)
}

// ---------- Response types ----------

// FleetState holds the complete current state fetched from the Fleet API.
type FleetState struct {
	Teams                  []Team
	Labels                 []Label
	FleetMaintainedCatalog []FleetMaintainedApp
	Config                 map[string]any // from GET /api/v1/fleet/config
	GlobalPolicies         []Policy       // from GET /api/v1/fleet/global/policies (teamID=0)
	GlobalQueries          []Query        // from GET /api/v1/fleet/queries (teamID=0)
}

// Team represents a Fleet team with its associated resources.
// Policies/queries/profiles are fetched via separate endpoints.
// Managed software definitions come directly from /teams[].software.
type Team struct {
	ID             uint         `json:"id"`
	Name           string       `json:"name"`
	Software       TeamSoftware `json:"software"`
	Policies       []Policy
	Queries        []Query
	Profiles       []Profile // populated by GetProfiles
	SoftwareTitles []SoftwareTitle
}

// TeamSoftware mirrors /api/v1/fleet/teams[].software for managed software
// definitions configured in Fleet/GitOps.
type TeamSoftware struct {
	Packages        []TeamSoftwarePackage `json:"packages"`
	FleetMaintained []TeamFleetApp        `json:"fleet_maintained_apps"`
	AppStoreApps    []TeamAppStoreApp     `json:"app_store_apps"`
}

type TeamSoftwarePackage struct {
	URL                string `json:"url"`
	HashSHA256         string `json:"hash_sha256"`
	SelfService        bool   `json:"self_service"`
	ReferencedYAMLPath string `json:"referenced_yaml_path"`
}

type TeamFleetApp struct {
	Slug        string `json:"slug"`
	SelfService bool   `json:"self_service"`
}

type TeamAppStoreApp struct {
	AppStoreID  string `json:"app_store_id"`
	SelfService bool   `json:"self_service"`
}

// FleetMaintainedApp is an entry from Fleet's maintained-app catalog.
type FleetMaintainedApp struct {
	ID              uint   `json:"id"`
	SoftwareTitleID uint   `json:"software_title_id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Platform        string `json:"platform"`
}

// Policy represents a Fleet policy as returned by the API.
type Policy struct {
	ID               uint     `json:"id"`
	Name             string   `json:"name"`
	Query            string   `json:"query"`
	Description      string   `json:"description"`
	Resolution       string   `json:"resolution"`
	Platform         string   `json:"platform"`
	Critical         bool     `json:"critical"`
	PassingHostCount uint     `json:"passing_host_count"`
	FailingHostCount uint     `json:"failing_host_count"`
	LabelsIncludeAny []string `json:"-"` // normalized from API response
	LabelsExcludeAny []string `json:"-"`
}

// Query represents a Fleet query.
type Query struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Query    string `json:"query"`
	Interval uint   `json:"interval"`
	Platform string `json:"platform"`
	Logging  string `json:"logging"`
}

// SoftwareTitle represents a software title in Fleet.
type SoftwareTitle struct {
	ID              uint                      `json:"id"`
	Name            string                    `json:"name"`
	Source          string                    `json:"source"`
	HostCount       uint                      `json:"hosts_count"`
	AppStoreApp     *SoftwareTitleAppStore    `json:"app_store_app"`
	SoftwarePackage *SoftwareTitlePackageMeta `json:"software_package"`
}

// SoftwareTitleAppStore contains app-store-specific metadata for a title.
type SoftwareTitleAppStore struct {
	AppStoreID  string `json:"app_store_id"`
	SelfService bool   `json:"self_service"`
	Platform    string `json:"platform"`
}

// SoftwareTitlePackageMeta contains package metadata for a title.
type SoftwareTitlePackageMeta struct {
	Name       string `json:"name"`
	PackageURL string `json:"package_url"`
	SelfService bool  `json:"self_service"`
	Platform   string `json:"platform"`
}

// Label represents a Fleet label.
type Label struct {
	ID        uint   `json:"id"`
	Name      string `json:"name"`
	Query     string `json:"query"`
	Platform  string `json:"platform"`
	HostCount uint   `json:"host_count"`
}

// Profile represents an MDM configuration profile.
type Profile struct {
	ProfileUUID string `json:"profile_uuid"`
	Name        string `json:"name"`
	Platform    string `json:"platform"`
}

// ---------- API response wrappers ----------

type teamsResponse struct {
	Teams []Team `json:"teams"`
}

type policiesResponse struct {
	Policies []Policy `json:"policies"`
}

type queriesResponse struct {
	Queries []Query `json:"queries"`
}

type softwareResponse struct {
	SoftwareTitles []SoftwareTitle `json:"software_titles"`
	Meta           struct {
		HasNextResults bool `json:"has_next_results"`
	} `json:"meta"`
}

type labelsResponse struct {
	Labels []Label `json:"labels"`
}

type profilesResponse struct {
	Profiles []Profile `json:"profiles"`
}

type fleetMaintainedAppsResponse struct {
	FleetMaintainedApps []FleetMaintainedApp `json:"fleet_maintained_apps"`
	Meta                struct {
		HasNextResults bool `json:"has_next_results"`
	} `json:"meta"`
}

// ---------- Fetch methods ----------

// GetConfig fetches the Fleet server configuration (org_settings, agent_options, etc.).
func (c *Client) GetConfig(ctx context.Context) (map[string]any, error) {
	var result map[string]any
	if err := c.get(ctx, "/api/v1/fleet/config", nil, &result); err != nil {
		return nil, fmt.Errorf("fetching config: %w", err)
	}
	return result, nil
}

// GetTeams fetches all teams with pagination.
func (c *Client) GetTeams(ctx context.Context) ([]Team, error) {
	var all []Team
	page := 0
	for {
		q := url.Values{
			"per_page": {"250"},
			"page":     {strconv.Itoa(page)},
		}
		var resp teamsResponse
		if err := c.get(ctx, "/api/v1/fleet/teams", q, &resp); err != nil {
			return nil, fmt.Errorf("fetching teams: %w", err)
		}
		all = append(all, resp.Teams...)
		if len(resp.Teams) < 250 {
			break
		}
		page++
		if page > 100 { // safety: max 25k teams
			break
		}
	}
	return all, nil
}

// GetPolicies fetches policies for a team (0 = global) with pagination.
func (c *Client) GetPolicies(ctx context.Context, teamID uint) ([]Policy, error) {
	apiPath := "/api/v1/fleet/global/policies"
	if teamID > 0 {
		apiPath = fmt.Sprintf("/api/v1/fleet/teams/%d/policies", teamID)
	}
	var all []Policy
	page := 0
	for {
		q := url.Values{
			"per_page": {"250"},
			"page":     {strconv.Itoa(page)},
		}
		var resp policiesResponse
		if err := c.get(ctx, apiPath, q, &resp); err != nil {
			return nil, fmt.Errorf("fetching policies (team %d): %w", teamID, err)
		}
		all = append(all, resp.Policies...)
		if len(resp.Policies) < 250 {
			break
		}
		page++
		if page > 100 { // safety: max 25k policies
			break
		}
	}
	return all, nil
}

// GetQueries fetches queries, optionally filtered by team, with pagination.
func (c *Client) GetQueries(ctx context.Context, teamID uint) ([]Query, error) {
	var all []Query
	page := 0
	for {
		q := url.Values{
			"per_page": {"250"},
			"page":     {strconv.Itoa(page)},
		}
		if teamID > 0 {
			q.Set("team_id", strconv.FormatUint(uint64(teamID), 10))
		}
		var resp queriesResponse
		if err := c.get(ctx, "/api/v1/fleet/queries", q, &resp); err != nil {
			return nil, fmt.Errorf("fetching queries (team %d): %w", teamID, err)
		}
		all = append(all, resp.Queries...)
		if len(resp.Queries) < 250 {
			break
		}
		page++
		if page > 100 { // safety: max 25k queries
			break
		}
	}
	return all, nil
}

// GetSoftware fetches managed (available_for_install) software titles for a team.
// Uses available_for_install=true to exclude detected-only titles (OS packages,
// browser extensions, etc.) and only return software deployed via Fleet/GitOps.
// Paginates to collect all results.
func (c *Client) GetSoftware(ctx context.Context, teamID uint) ([]SoftwareTitle, error) {
	var all []SoftwareTitle
	page := 0
	for {
		q := url.Values{
			"per_page":              {"250"},
			"page":                  {strconv.Itoa(page)},
			"available_for_install": {"true"},
		}
		if teamID > 0 {
			q.Set("team_id", strconv.FormatUint(uint64(teamID), 10))
		}
		var resp softwareResponse
		if err := c.get(ctx, "/api/v1/fleet/software/titles", q, &resp); err != nil {
			return nil, fmt.Errorf("fetching software (team %d): %w", teamID, err)
		}
		all = append(all, resp.SoftwareTitles...)
		if !resp.Meta.HasNextResults || len(resp.SoftwareTitles) == 0 {
			break // last page
		}
		page++
		if page > 400 { // safety: max 100k titles
			break
		}
	}
	return all, nil
}

// GetFleetMaintainedApps fetches Fleet's maintained-app catalog.
func (c *Client) GetFleetMaintainedApps(ctx context.Context) ([]FleetMaintainedApp, error) {
	var all []FleetMaintainedApp
	page := 0
	for {
		q := url.Values{
			"per_page": {"250"},
			"page":     {strconv.Itoa(page)},
		}
		var resp fleetMaintainedAppsResponse
		if err := c.get(ctx, "/api/v1/fleet/software/fleet_maintained_apps", q, &resp); err != nil {
			return nil, fmt.Errorf("fetching fleet maintained apps: %w", err)
		}
		all = append(all, resp.FleetMaintainedApps...)
		if !resp.Meta.HasNextResults || len(resp.FleetMaintainedApps) == 0 {
			break
		}
		page++
		if page > 400 { // safety: max 100k catalog entries
			break
		}
	}
	return all, nil
}

// GetLabels fetches all labels with pagination.
func (c *Client) GetLabels(ctx context.Context) ([]Label, error) {
	var all []Label
	page := 0
	for {
		q := url.Values{
			"per_page": {"250"},
			"page":     {strconv.Itoa(page)},
		}
		var resp labelsResponse
		if err := c.get(ctx, "/api/v1/fleet/labels", q, &resp); err != nil {
			return nil, fmt.Errorf("fetching labels: %w", err)
		}
		all = append(all, resp.Labels...)
		if len(resp.Labels) < 250 {
			break
		}
		page++
		if page > 100 { // safety: max 25k labels
			break
		}
	}
	return all, nil
}

// GetProfiles fetches MDM profiles for a team.
func (c *Client) GetProfiles(ctx context.Context, teamID uint) ([]Profile, error) {
	q := url.Values{"per_page": {"250"}}
	if teamID > 0 {
		q.Set("team_id", strconv.FormatUint(uint64(teamID), 10))
	}
	var resp profilesResponse
	if err := c.get(ctx, "/api/v1/fleet/mdm/profiles", q, &resp); err != nil {
		return nil, fmt.Errorf("fetching profiles (team %d): %w", teamID, err)
	}
	return resp.Profiles, nil
}

// FetchAll concurrently fetches the complete Fleet state. Uses errgroup for
// parallel requests. If fetchGlobal is true, also fetches global config,
// policies, and queries (for default.yml diffing).
func (c *Client) FetchAll(ctx context.Context, fetchGlobal ...bool) (*FleetState, error) {
	state := &FleetState{}
	wantGlobal := len(fetchGlobal) > 0 && fetchGlobal[0]

	teams, err := c.GetTeams(ctx)
	if err != nil {
		return nil, err
	}

	labels, err := c.GetLabels(ctx)
	if err != nil {
		return nil, err
	}
	state.Labels = labels

	fleetMaintainedCatalog, err := c.GetFleetMaintainedApps(ctx)
	if err != nil {
		var httpErr *HTTPError
		if !errors.As(err, &httpErr) ||
			(httpErr.StatusCode != http.StatusNotFound && httpErr.StatusCode != http.StatusForbidden) {
			return nil, err
		}
		// Older Fleet versions (or restricted roles) may not expose this endpoint.
		fleetMaintainedCatalog = nil
	}
	state.FleetMaintainedCatalog = fleetMaintainedCatalog

	// Fetch per-team resources concurrently
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	// Fetch global config/policies/queries in parallel with team resources
	if wantGlobal {
		g.Go(func() error {
			cfg, err := c.GetConfig(gctx)
			if err != nil {
				return err
			}
			state.Config = cfg
			return nil
		})
		g.Go(func() error {
			policies, err := c.GetPolicies(gctx, 0)
			if err != nil {
				return err
			}
			state.GlobalPolicies = policies
			return nil
		})
		g.Go(func() error {
			queries, err := c.GetQueries(gctx, 0)
			if err != nil {
				return err
			}
			state.GlobalQueries = queries
			return nil
		})
	}

	teamResults := make([]Team, len(teams))
	for i, t := range teams {
		teamResults[i] = t
		idx := i
		teamID := t.ID

		g.Go(func() error {
			policies, err := c.GetPolicies(gctx, teamID)
			if err != nil {
				return err
			}
			teamResults[idx].Policies = policies
			return nil
		})

		g.Go(func() error {
			queries, err := c.GetQueries(gctx, teamID)
			if err != nil {
				return err
			}
			teamResults[idx].Queries = queries
			return nil
		})

		g.Go(func() error {
			profiles, err := c.GetProfiles(gctx, teamID)
			if err != nil {
				return err
			}
			teamResults[idx].Profiles = profiles
			return nil
		})

		g.Go(func() error {
			softwareTitles, err := c.GetSoftware(gctx, teamID)
			if err != nil {
				return err
			}
			teamResults[idx].SoftwareTitles = softwareTitles
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	state.Teams = teamResults
	return state, nil
}
