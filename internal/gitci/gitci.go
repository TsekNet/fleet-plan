// Package gitci detects the CI platform, fetches MR/PR changed files,
// and posts or updates a comment with the fleet-plan diff output.
package gitci

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Platform identifies the CI provider.
type Platform int

const (
	PlatformUnknown Platform = iota
	PlatformGitLab
	PlatformGitHub
)

// Env holds all CI environment variables resolved at startup.
type Env struct {
	Platform Platform

	// GitLab
	GitLabAPIURL    string
	GitLabProjectID string
	GitLabMRIID     string
	GitLabJobURL    string
	GitLabMRURL     string
	GitLabToken     string

	// GitHub
	GitHubAPIURL    string
	GitHubRepo      string
	GitHubPRNumber  string
	GitHubServerURL string
	GitHubToken     string

	// Git
	DiffBaseSHA        string
	TargetBranch       string
}

// Detect reads CI environment variables and returns a populated Env.
// Returns PlatformUnknown if not running in a recognized CI MR/PR context.
func Detect() Env {
	var e Env

	// GitLab: CI_MERGE_REQUEST_IID is only set in merge request pipelines.
	if iid := os.Getenv("CI_MERGE_REQUEST_IID"); iid != "" {
		e.Platform = PlatformGitLab
		e.GitLabAPIURL = os.Getenv("CI_API_V4_URL")
		e.GitLabProjectID = os.Getenv("CI_PROJECT_ID")
		e.GitLabMRIID = iid
		e.GitLabJobURL = os.Getenv("CI_JOB_URL")
		e.GitLabMRURL = os.Getenv("CI_PROJECT_URL") + "/-/merge_requests/" + iid
		e.GitLabToken = os.Getenv("FLEET_PLAN_BOT")
		e.DiffBaseSHA = os.Getenv("CI_MERGE_REQUEST_DIFF_BASE_SHA")
		e.TargetBranch = os.Getenv("CI_MERGE_REQUEST_TARGET_BRANCH_NAME")
		return e
	}

	// GitHub: GITHUB_EVENT_NAME is set for all GitHub Actions runs.
	if event := os.Getenv("GITHUB_EVENT_NAME"); event == "pull_request" || event == "pull_request_target" {
		e.Platform = PlatformGitHub
		e.GitHubAPIURL = os.Getenv("GITHUB_API_URL")
		if e.GitHubAPIURL == "" {
			e.GitHubAPIURL = "https://api.github.com"
		}
		e.GitHubRepo = os.Getenv("GITHUB_REPOSITORY")
		e.GitHubPRNumber = os.Getenv("PR_NUMBER")
		if e.GitHubPRNumber == "" {
			e.GitHubPRNumber = os.Getenv("GITHUB_PR_NUMBER")
		}
		e.GitHubServerURL = os.Getenv("GITHUB_SERVER_URL")
		e.GitHubToken = os.Getenv("GITHUB_TOKEN")
		e.DiffBaseSHA = os.Getenv("GITHUB_BASE_SHA")
		e.TargetBranch = os.Getenv("GITHUB_BASE_REF")
		return e
	}

	return e
}

// JobURL returns the CI job URL for embedding in comments.
func (e Env) JobURL() string {
	switch e.Platform {
	case PlatformGitLab:
		return e.GitLabJobURL
	case PlatformGitHub:
		if e.GitHubServerURL != "" && e.GitHubRepo != "" {
			runID := os.Getenv("GITHUB_RUN_ID")
			if runID != "" {
				return fmt.Sprintf("%s/%s/actions/runs/%s", e.GitHubServerURL, e.GitHubRepo, runID)
			}
		}
	}
	return ""
}

// ChangedFiles returns the list of files changed in the MR/PR.
// Priority: MR/PR API > git diff > empty (triggers full diff).
func (e Env) ChangedFiles() ([]string, error) {
	switch e.Platform {
	case PlatformGitLab:
		files, err := e.gitLabChangedFiles()
		if err == nil && len(files) > 0 {
			return files, nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: GitLab MR API unavailable (%v), falling back to git diff\n", err)
		}
	case PlatformGitHub:
		files, err := e.gitHubChangedFiles()
		if err == nil && len(files) > 0 {
			return files, nil
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: GitHub PR API unavailable (%v), falling back to git diff\n", err)
		}
	}

	return e.gitDiffChangedFiles()
}

func (e Env) gitLabChangedFiles() ([]string, error) {
	if e.GitLabAPIURL == "" || e.GitLabProjectID == "" || e.GitLabMRIID == "" || e.GitLabToken == "" {
		return nil, fmt.Errorf("missing GitLab API env vars")
	}
	apiURL := fmt.Sprintf("%s/projects/%s/merge_requests/%s/changes",
		e.GitLabAPIURL, url.PathEscape(e.GitLabProjectID), e.GitLabMRIID)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", e.GitLabToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Changes []struct {
			NewPath string `json:"new_path"`
			OldPath string `json:"old_path"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var files []string
	for _, c := range result.Changes {
		p := c.NewPath
		if p == "" {
			p = c.OldPath
		}
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

func (e Env) gitHubChangedFiles() ([]string, error) {
	if e.GitHubAPIURL == "" || e.GitHubRepo == "" || e.GitHubPRNumber == "" || e.GitHubToken == "" {
		return nil, fmt.Errorf("missing GitHub API env vars")
	}
	apiURL := fmt.Sprintf("%s/repos/%s/pulls/%s/files", e.GitHubAPIURL, e.GitHubRepo, e.GitHubPRNumber)

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result []struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var files []string
	for _, f := range result {
		if f.Filename != "" {
			files = append(files, f.Filename)
		}
	}
	return files, nil
}

func (e Env) gitDiffChangedFiles() ([]string, error) {
	// Try to fetch the target branch if needed.
	if e.TargetBranch != "" {
		_ = exec.Command("git", "fetch", "origin", e.TargetBranch, "--depth=200").Run()
	}

	var ref string
	if e.DiffBaseSHA != "" {
		// Verify the commit is available.
		if err := exec.Command("git", "cat-file", "-e", e.DiffBaseSHA+"^{commit}").Run(); err == nil {
			ref = e.DiffBaseSHA + "...HEAD"
		}
	}
	if ref == "" && e.TargetBranch != "" {
		ref = "origin/" + e.TargetBranch + "...HEAD"
	}
	if ref == "" {
		return nil, fmt.Errorf("no base SHA or target branch available for git diff")
	}

	out, err := exec.Command("git", "diff", "--name-only", ref).Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// PostOrUpdateComment posts or idempotently updates an MR/PR comment containing marker.
// If a comment with marker already exists it is replaced; otherwise a new one is created.
// Returns the comment URL on success.
func (e Env) PostOrUpdateComment(body, marker string) (string, error) {
	switch e.Platform {
	case PlatformGitLab:
		return e.gitLabPostOrUpdate(body, marker)
	case PlatformGitHub:
		return e.gitHubPostOrUpdate(body, marker)
	}
	return "", fmt.Errorf("unknown CI platform")
}

func (e Env) gitLabPostOrUpdate(body, marker string) (string, error) {
	if e.GitLabAPIURL == "" || e.GitLabProjectID == "" || e.GitLabMRIID == "" || e.GitLabToken == "" {
		return "", fmt.Errorf("FLEET_PLAN_BOT not set; skipping MR note")
	}

	notesURL := fmt.Sprintf("%s/projects/%s/merge_requests/%s/notes",
		e.GitLabAPIURL, url.PathEscape(e.GitLabProjectID), e.GitLabMRIID)

	client := &http.Client{Timeout: 15 * time.Second}

	// Find existing note by marker.
	noteID, err := e.gitLabFindNote(client, notesURL, marker)
	if err != nil {
		return "", fmt.Errorf("listing MR notes: %w", err)
	}

	params := url.Values{}
	params.Set("body", body)
	encoded := params.Encode()

	var method, reqURL string
	if noteID != "" {
		method = "PUT"
		reqURL = notesURL + "/" + noteID
	} else {
		method = "POST"
		reqURL = notesURL
	}

	req, err := http.NewRequest(method, reqURL, strings.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", e.GitLabToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitLab API %s %d: %s", method, resp.StatusCode, string(respBody))
	}

	var note struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(respBody, &note)
	commentURL := fmt.Sprintf("%s#note_%d", e.GitLabMRURL, note.ID)
	return commentURL, nil
}

func (e Env) gitLabFindNote(client *http.Client, notesURL, marker string) (string, error) {
	req, err := http.NewRequest("GET", notesURL+"?per_page=100&sort=desc&order_by=updated_at", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", e.GitLabToken)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var notes []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &notes); err != nil {
		return "", err
	}

	needle := fmt.Sprintf("<!-- %s -->", marker)
	for _, n := range notes {
		if strings.Contains(n.Body, needle) {
			return fmt.Sprintf("%d", n.ID), nil
		}
	}
	return "", nil
}

func (e Env) gitHubPostOrUpdate(body, marker string) (string, error) {
	if e.GitHubAPIURL == "" || e.GitHubRepo == "" || e.GitHubPRNumber == "" || e.GitHubToken == "" {
		return "", fmt.Errorf("GITHUB_TOKEN not set; skipping PR comment")
	}

	commentsURL := fmt.Sprintf("%s/repos/%s/issues/%s/comments",
		e.GitHubAPIURL, e.GitHubRepo, e.GitHubPRNumber)

	client := &http.Client{Timeout: 15 * time.Second}

	commentID, err := e.gitHubFindComment(client, commentsURL, marker)
	if err != nil {
		return "", fmt.Errorf("listing PR comments: %w", err)
	}

	payload, _ := json.Marshal(map[string]string{"body": body})

	var method, reqURL string
	if commentID != "" {
		method = "PATCH"
		reqURL = fmt.Sprintf("%s/repos/%s/issues/comments/%s", e.GitHubAPIURL, e.GitHubRepo, commentID)
	} else {
		method = "POST"
		reqURL = commentsURL
	}

	req, err := http.NewRequest(method, reqURL, strings.NewReader(string(payload)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("GitHub API %s %d: %s", method, resp.StatusCode, string(respBody))
	}

	var comment struct {
		HTMLURL string `json:"html_url"`
	}
	_ = json.Unmarshal(respBody, &comment)
	return comment.HTMLURL, nil
}

func (e Env) gitHubFindComment(client *http.Client, commentsURL, marker string) (string, error) {
	req, err := http.NewRequest("GET", commentsURL+"?per_page=100", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var comments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &comments); err != nil {
		return "", err
	}

	needle := fmt.Sprintf("<!-- %s -->", marker)
	for _, c := range comments {
		if strings.Contains(c.Body, needle) {
			return fmt.Sprintf("%d", c.ID), nil
		}
	}
	return "", nil
}
