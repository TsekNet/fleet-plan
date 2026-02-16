package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// testClient creates a Client pointing at the test server.
// Sets FLEET_PLAN_INSECURE=1 since httptest uses http://.
func testClient(t *testing.T, ts *httptest.Server, token string) *Client {
	t.Helper()
	t.Setenv("FLEET_PLAN_INSECURE", "1")
	c, err := NewClient(ts.URL, token)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// ---------- NewClient ----------

func TestNewClient(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		token    string
		insecure string // FLEET_PLAN_INSECURE env var
		wantErr  bool
		wantURL  string
	}{
		{name: "https URL accepted", url: "https://fleet.example.com", token: "tok", wantURL: "https://fleet.example.com"},
		{name: "trailing slash stripped", url: "https://fleet.example.com/", token: "tok", wantURL: "https://fleet.example.com"},
		{name: "multiple trailing slashes stripped", url: "https://fleet.example.com///", token: "tok", wantURL: "https://fleet.example.com"},
		{name: "http rejected by default", url: "http://insecure.example.com", token: "tok", wantErr: true},
		{name: "http allowed with insecure flag", url: "http://insecure.example.com", token: "tok", insecure: "1", wantURL: "http://insecure.example.com"},
		{name: "HTTP uppercase rejected", url: "HTTP://insecure.example.com", token: "tok", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.insecure != "" {
				t.Setenv("FLEET_PLAN_INSECURE", tt.insecure)
			} else {
				t.Setenv("FLEET_PLAN_INSECURE", "")
			}

			c, err := NewClient(tt.url, tt.token)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.baseURL != tt.wantURL {
				t.Errorf("baseURL: got %q, want %q", c.baseURL, tt.wantURL)
			}
		})
	}
}

// ---------- HTTP error handling ----------

func TestHTTPErrorCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus int
	}{
		{name: "401 unauthorized", statusCode: 401, body: `{"message":"Unauthorized"}`, wantStatus: 401},
		{name: "403 forbidden", statusCode: 403, body: `{"message":"Forbidden"}`, wantStatus: 403},
		{name: "404 not found", statusCode: 404, body: `{"message":"Not found"}`, wantStatus: 404},
		{name: "500 server error", statusCode: 500, body: `{"message":"Internal error"}`, wantStatus: 500},
		{name: "429 rate limited", statusCode: 429, body: `{"message":"Too many requests"}`, wantStatus: 429},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.body))
			}))
			defer ts.Close()

			c := testClient(t, ts, "tok")
			_, err := c.GetTeams(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}

			if httpErr, ok := err.(*HTTPError); ok {
				if httpErr.StatusCode != tt.wantStatus {
					t.Errorf("status: got %d, want %d", httpErr.StatusCode, tt.wantStatus)
				}
			}
		})
	}
}

// ---------- GetTeams ----------

func TestGetTeams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer testtoken" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(teamsResponse{
			Teams: []Team{
				{ID: 1, Name: "Workstations"},
				{ID: 2, Name: "Infrastructure"},
			},
		})
	}))
	defer ts.Close()

	c := testClient(t, ts, "testtoken")
	teams, err := c.GetTeams(context.Background())
	if err != nil {
		t.Fatalf("GetTeams: %v", err)
	}
	if len(teams) != 2 {
		t.Fatalf("expected 2 teams, got %d", len(teams))
	}
	if teams[0].Name != "Workstations" {
		t.Errorf("first team: got %q", teams[0].Name)
	}
}

// ---------- GetPolicies ----------

func TestGetPoliciesPathRouting(t *testing.T) {
	tests := []struct {
		name     string
		teamID   uint
		wantPath string
	}{
		{name: "global policies", teamID: 0, wantPath: "/api/v1/fleet/policies"},
		{name: "team 1 policies", teamID: 1, wantPath: "/api/v1/fleet/teams/1/policies"},
		{name: "team 42 policies", teamID: 42, wantPath: "/api/v1/fleet/teams/42/policies"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				json.NewEncoder(w).Encode(policiesResponse{Policies: []Policy{}})
			}))
			defer ts.Close()

			c := testClient(t, ts, "tok")
			_, err := c.GetPolicies(context.Background(), tt.teamID)
			if err != nil {
				t.Fatalf("GetPolicies: %v", err)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tt.wantPath)
			}
		})
	}
}

func TestGetPoliciesFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(policiesResponse{
			Policies: []Policy{
				{ID: 10, Name: "Disk Encryption", Platform: "darwin", Critical: true},
			},
		})
	}))
	defer ts.Close()

	c := testClient(t, ts, "tok")
	policies, err := c.GetPolicies(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetPolicies: %v", err)
	}
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(policies))
	}
	if policies[0].Name != "Disk Encryption" {
		t.Errorf("policy name: got %q", policies[0].Name)
	}
	if !policies[0].Critical {
		t.Error("policy should be critical")
	}
}

// ---------- GetLabels ----------

func TestGetLabels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(labelsResponse{
			Labels: []Label{
				{ID: 1, Name: "Managed Devices", HostCount: 24},
				{ID: 2, Name: "macOS 15+", HostCount: 120},
			},
		})
	}))
	defer ts.Close()

	c := testClient(t, ts, "tok")
	labels, err := c.GetLabels(context.Background())
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	if len(labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(labels))
	}
	if labels[0].HostCount != 24 {
		t.Errorf("host count: got %d", labels[0].HostCount)
	}
}

// ---------- GetQueries ----------

func TestGetQueries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		teamID := r.URL.Query().Get("team_id")
		if teamID != "5" {
			t.Errorf("expected team_id=5, got %q", teamID)
		}
		json.NewEncoder(w).Encode(queriesResponse{
			Queries: []Query{
				{ID: 1, Name: "Disk Usage", Interval: 3600, Platform: "darwin"},
			},
		})
	}))
	defer ts.Close()

	c := testClient(t, ts, "tok")
	queries, err := c.GetQueries(context.Background(), 5)
	if err != nil {
		t.Fatalf("GetQueries: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("expected 1 query, got %d", len(queries))
	}
	if queries[0].Interval != 3600 {
		t.Errorf("interval: got %d", queries[0].Interval)
	}
}

// ---------- GetProfiles ----------

func TestGetProfilesTeamID(t *testing.T) {
	tests := []struct {
		name       string
		teamID     uint
		wantTeamID string
	}{
		{name: "global profiles", teamID: 0, wantTeamID: ""},
		{name: "team 5 profiles", teamID: 5, wantTeamID: "5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotTeamID string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotTeamID = r.URL.Query().Get("team_id")
				json.NewEncoder(w).Encode(profilesResponse{Profiles: []Profile{}})
			}))
			defer ts.Close()

			c := testClient(t, ts, "tok")
			_, err := c.GetProfiles(context.Background(), tt.teamID)
			if err != nil {
				t.Fatalf("GetProfiles: %v", err)
			}
			if gotTeamID != tt.wantTeamID {
				t.Errorf("team_id param: got %q, want %q", gotTeamID, tt.wantTeamID)
			}
		})
	}
}

// ---------- GetSoftware pagination ----------

func TestGetSoftwarePagination(t *testing.T) {
	tests := []struct {
		name      string
		pages     int
		perPage   int
		wantTotal int
	}{
		{name: "single page", pages: 1, perPage: 3, wantTotal: 3},
		{name: "two pages", pages: 2, perPage: 2, wantTotal: 4},
		{name: "empty result", pages: 1, perPage: 0, wantTotal: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageCount := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pageCount++
				var titles []SoftwareTitle
				if tt.perPage > 0 && pageCount <= tt.pages {
					for i := 0; i < tt.perPage; i++ {
						titles = append(titles, SoftwareTitle{ID: uint(pageCount*100 + i), Name: "App"})
					}
				}
				resp := softwareResponse{SoftwareTitles: titles}
				resp.Meta.HasNextResults = pageCount < tt.pages
				json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			c := testClient(t, ts, "tok")
			titles, err := c.GetSoftware(context.Background(), 1)
			if err != nil {
				t.Fatalf("GetSoftware: %v", err)
			}
			if len(titles) != tt.wantTotal {
				t.Errorf("total: got %d, want %d", len(titles), tt.wantTotal)
			}
		})
	}
}

// ---------- GetFleetMaintainedApps pagination ----------

func TestGetFleetMaintainedAppsPagination(t *testing.T) {
	tests := []struct {
		name      string
		pages     int
		perPage   int
		wantTotal int
	}{
		{name: "single page", pages: 1, perPage: 5, wantTotal: 5},
		{name: "three pages", pages: 3, perPage: 2, wantTotal: 6},
		{name: "empty catalog", pages: 1, perPage: 0, wantTotal: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pageCount := 0
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				pageCount++
				var apps []FleetMaintainedApp
				if tt.perPage > 0 && pageCount <= tt.pages {
					for i := 0; i < tt.perPage; i++ {
						apps = append(apps, FleetMaintainedApp{ID: uint(pageCount*100 + i), Slug: "app/darwin"})
					}
				}
				resp := fleetMaintainedAppsResponse{FleetMaintainedApps: apps}
				resp.Meta.HasNextResults = pageCount < tt.pages
				json.NewEncoder(w).Encode(resp)
			}))
			defer ts.Close()

			c := testClient(t, ts, "tok")
			apps, err := c.GetFleetMaintainedApps(context.Background())
			if err != nil {
				t.Fatalf("GetFleetMaintainedApps: %v", err)
			}
			if len(apps) != tt.wantTotal {
				t.Errorf("total: got %d, want %d", len(apps), tt.wantTotal)
			}
		})
	}
}

// ---------- Authorization header ----------

func TestAuthorizationHeader(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{name: "standard token", token: "abc123", want: "Bearer abc123"},
		{name: "empty token", token: "", want: "Bearer"},
		{name: "token with spaces", token: "my token", want: "Bearer my token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotAuth string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				json.NewEncoder(w).Encode(labelsResponse{Labels: []Label{}})
			}))
			defer ts.Close()

			c := testClient(t, ts, tt.token)
			_, _ = c.GetLabels(context.Background())

			if gotAuth != tt.want {
				t.Errorf("auth header: got %q, want %q", gotAuth, tt.want)
			}
		})
	}
}

// ---------- FetchAll ----------

func TestFetchAll(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/fleet/teams", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(teamsResponse{Teams: []Team{{ID: 1, Name: "Test Team"}}})
	})
	mux.HandleFunc("/api/v1/fleet/labels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(labelsResponse{Labels: []Label{{ID: 1, Name: "Test Label"}}})
	})
	mux.HandleFunc("/api/v1/fleet/teams/1/policies", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(policiesResponse{Policies: []Policy{{ID: 1, Name: "Test Policy"}}})
	})
	mux.HandleFunc("/api/v1/fleet/queries", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(queriesResponse{})
	})
	mux.HandleFunc("/api/v1/fleet/software/titles", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(softwareResponse{})
	})
	mux.HandleFunc("/api/v1/fleet/software/fleet_maintained_apps", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(fleetMaintainedAppsResponse{})
	})
	mux.HandleFunc("/api/v1/fleet/mdm/profiles", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(profilesResponse{})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := testClient(t, ts, "tok")
	state, err := c.FetchAll(context.Background())
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(state.Teams) != 1 {
		t.Fatalf("expected 1 team, got %d", len(state.Teams))
	}
	if len(state.Teams[0].Policies) != 1 {
		t.Fatalf("expected 1 policy, got %d", len(state.Teams[0].Policies))
	}
	if len(state.Labels) != 1 {
		t.Fatalf("expected 1 label, got %d", len(state.Labels))
	}
}

// ---------- FetchAll fleet maintained catalog fallback ----------

func TestFetchAllFleetMaintainedFallback(t *testing.T) {
	tests := []struct {
		name           string
		catalogStatus  int
		wantCatalogLen int
		wantErr        bool
	}{
		{name: "200 returns catalog", catalogStatus: 200, wantCatalogLen: 1},
		{name: "404 gracefully returns empty", catalogStatus: 404, wantCatalogLen: 0},
		{name: "403 gracefully returns empty", catalogStatus: 403, wantCatalogLen: 0},
		{name: "500 returns error", catalogStatus: 500, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/fleet/teams", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(teamsResponse{Teams: []Team{{ID: 1, Name: "T"}}})
			})
			mux.HandleFunc("/api/v1/fleet/labels", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(labelsResponse{})
			})
			mux.HandleFunc("/api/v1/fleet/software/fleet_maintained_apps", func(w http.ResponseWriter, r *http.Request) {
				if tt.catalogStatus != 200 {
					w.WriteHeader(tt.catalogStatus)
					w.Write([]byte(`{"message":"error"}`))
					return
				}
				json.NewEncoder(w).Encode(fleetMaintainedAppsResponse{
					FleetMaintainedApps: []FleetMaintainedApp{{ID: 1, Slug: "slack/darwin"}},
				})
			})
			mux.HandleFunc("/api/v1/fleet/teams/1/policies", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(policiesResponse{})
			})
			mux.HandleFunc("/api/v1/fleet/queries", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(queriesResponse{})
			})
			mux.HandleFunc("/api/v1/fleet/software/titles", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(softwareResponse{})
			})
			mux.HandleFunc("/api/v1/fleet/mdm/profiles", func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(profilesResponse{})
			})

			ts := httptest.NewServer(mux)
			defer ts.Close()

			c := testClient(t, ts, "tok")
			state, err := c.FetchAll(context.Background())

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchAll: %v", err)
			}
			if len(state.FleetMaintainedCatalog) != tt.wantCatalogLen {
				t.Errorf("catalog len: got %d, want %d", len(state.FleetMaintainedCatalog), tt.wantCatalogLen)
			}
		})
	}
}
