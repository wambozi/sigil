// Command sigil-plugin-github polls GitHub for PR status, CI checks, reviews,
// and issue links, then pushes events to sigild's plugin ingest endpoint.
//
// It discovers repos from the git sources sigild is already watching and polls
// the GitHub API for each repo's open PRs on the current branch.
//
// Install: ships with sigild (make build / make install)
// Config:  set GITHUB_TOKEN env var or configure in [plugins.github.env]
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultIngestURL = "http://127.0.0.1:7775/api/v1/ingest"
	pluginName       = "github"
	githubAPI        = "https://api.github.com"
)

type PluginEvent struct {
	Plugin      string         `json:"plugin"`
	Kind        string         `json:"kind"`
	Timestamp   time.Time      `json:"timestamp"`
	Correlation map[string]any `json:"correlation,omitempty"`
	Payload     map[string]any `json:"payload"`
}

var (
	ingestURL    string
	pollInterval time.Duration
	token        string
)

func main() {
	flag.StringVar(&ingestURL, "sigil-ingest-url", defaultIngestURL, "Sigil ingest URL")
	flag.DurationVar(&pollInterval, "poll-interval", 2*time.Minute, "Poll interval")
	flag.Parse()

	token = os.Getenv("GITHUB_TOKEN")
	if token == "" {
		// Try gh CLI token as fallback.
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token = strings.TrimSpace(string(out))
		}
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-github: GITHUB_TOKEN not set and gh auth not available")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "sigil-plugin-github: polling every %s\n", pollInterval)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	// Poll immediately, then on interval.
	pollAll()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "sigil-plugin-github: shutting down")
			return
		case <-ticker.C:
			pollAll()
		}
	}
}

// pollAll discovers local git repos and polls GitHub for each.
func pollAll() {
	repos := discoverRepos()
	for _, repo := range repos {
		owner, name := parseRemote(repo)
		if owner == "" || name == "" {
			continue
		}
		branch := currentBranch(repo)
		pollPRs(repo, owner, name, branch)
		pollCIStatus(repo, owner, name, branch)
	}
}

// discoverRepos finds git repos in common locations.
func discoverRepos() []string {
	// Read sigild's config to find repo_dirs, or fall back to cwd.
	cwd, _ := os.Getwd()
	candidates := []string{cwd}

	// Walk up from cwd looking for .git.
	dir := cwd
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && info.IsDir() {
			candidates = []string{dir}
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return candidates
}

// parseRemote extracts owner/repo from a git remote URL.
func parseRemote(repoPath string) (owner, repo string) {
	out, err := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", ""
	}
	url := strings.TrimSpace(string(out))

	// Handle SSH: git@github.com:owner/repo.git
	if strings.HasPrefix(url, "git@github.com:") {
		parts := strings.TrimPrefix(url, "git@github.com:")
		parts = strings.TrimSuffix(parts, ".git")
		split := strings.SplitN(parts, "/", 2)
		if len(split) == 2 {
			return split[0], split[1]
		}
	}

	// Handle HTTPS: https://github.com/owner/repo.git
	if strings.Contains(url, "github.com/") {
		idx := strings.Index(url, "github.com/")
		parts := url[idx+len("github.com/"):]
		parts = strings.TrimSuffix(parts, ".git")
		split := strings.SplitN(parts, "/", 2)
		if len(split) == 2 {
			return split[0], split[1]
		}
	}

	return "", ""
}

// currentBranch reads the current branch from .git/HEAD.
func currentBranch(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	const prefix = "ref: refs/heads/"
	if strings.HasPrefix(line, prefix) {
		return line[len(prefix):]
	}
	return ""
}

// pollPRs fetches open PRs for the current branch.
func pollPRs(repoPath, owner, repo, branch string) {
	if branch == "" || branch == "main" || branch == "master" {
		return // don't poll for default branch PRs
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls?head=%s:%s&state=open", githubAPI, owner, repo, owner, branch)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var prs []struct {
		Number    int    `json:"number"`
		Title     string `json:"title"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		HTMLURL   string `json:"html_url"`
		Mergeable *bool  `json:"mergeable"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(body, &prs); err != nil {
		return
	}

	for _, pr := range prs {
		labels := make([]string, len(pr.Labels))
		for i, l := range pr.Labels {
			labels[i] = l.Name
		}
		reviewers := make([]string, len(pr.RequestedReviewers))
		for i, r := range pr.RequestedReviewers {
			reviewers[i] = r.Login
		}

		send(PluginEvent{
			Plugin:    pluginName,
			Kind:      "pr_status",
			Timestamp: time.Now(),
			Correlation: map[string]any{
				"repo_root": repoPath,
				"branch":    branch,
				"pr_id":     fmt.Sprintf("%d", pr.Number),
			},
			Payload: map[string]any{
				"number":     pr.Number,
				"title":      pr.Title,
				"state":      pr.State,
				"draft":      pr.Draft,
				"url":        pr.HTMLURL,
				"author":     pr.User.Login,
				"mergeable":  pr.Mergeable,
				"reviewers":  reviewers,
				"labels":     labels,
				"created_at": pr.CreatedAt.Format(time.RFC3339),
				"updated_at": pr.UpdatedAt.Format(time.RFC3339),
			},
		})

		// Fetch reviews for this PR.
		pollReviews(repoPath, owner, repo, branch, pr.Number)
	}
}

// pollReviews fetches review status for a PR.
func pollReviews(repoPath, owner, repo, branch string, prNumber int) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", githubAPI, owner, repo, prNumber)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var reviews []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State       string    `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, PENDING
		SubmittedAt time.Time `json:"submitted_at"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil {
		return
	}

	if len(reviews) == 0 {
		return
	}

	// Summarize: count by state.
	states := make(map[string]int)
	for _, r := range reviews {
		states[r.State]++
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "pr_reviews",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": repoPath,
			"branch":    branch,
			"pr_id":     fmt.Sprintf("%d", prNumber),
		},
		Payload: map[string]any{
			"pr_number":    prNumber,
			"review_count": len(reviews),
			"approved":     states["APPROVED"],
			"changes_requested": states["CHANGES_REQUESTED"],
			"commented":    states["COMMENTED"],
		},
	})
}

// pollCIStatus fetches the combined CI status for the branch HEAD.
func pollCIStatus(repoPath, owner, repo, branch string) {
	if branch == "" {
		return
	}

	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s/status", githubAPI, owner, repo, branch)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var status struct {
		State    string `json:"state"` // success, failure, pending
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			Description string `json:"description"`
			TargetURL   string `json:"target_url"`
		} `json:"statuses"`
		TotalCount int `json:"total_count"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		return
	}

	if status.TotalCount == 0 {
		// Try check runs instead (GitHub Actions uses check runs, not statuses).
		pollCheckRuns(repoPath, owner, repo, branch)
		return
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "ci_status",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": repoPath,
			"branch":    branch,
		},
		Payload: map[string]any{
			"state":       status.State,
			"total_count": status.TotalCount,
		},
	})
}

// pollCheckRuns fetches GitHub Actions check runs for the branch HEAD.
func pollCheckRuns(repoPath, owner, repo, branch string) {
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s/check-runs", githubAPI, owner, repo, branch)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var result struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Name       string  `json:"name"`
			Status     string  `json:"status"`     // queued, in_progress, completed
			Conclusion *string `json:"conclusion"` // success, failure, neutral, etc.
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return
	}

	if result.TotalCount == 0 {
		return
	}

	// Summarize conclusions.
	conclusions := make(map[string]int)
	allComplete := true
	for _, cr := range result.CheckRuns {
		if cr.Status != "completed" {
			allComplete = false
			conclusions["in_progress"]++
		} else if cr.Conclusion != nil {
			conclusions[*cr.Conclusion]++
		}
	}

	overallState := "pending"
	if allComplete {
		if conclusions["failure"] > 0 {
			overallState = "failure"
		} else {
			overallState = "success"
		}
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "ci_status",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": repoPath,
			"branch":    branch,
		},
		Payload: map[string]any{
			"state":       overallState,
			"total_count": result.TotalCount,
			"conclusions": conclusions,
		},
	})
}

// ghGet makes an authenticated GET request to the GitHub API.
func ghGet(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API %d for %s", resp.StatusCode, url)
	}

	return io.ReadAll(resp.Body)
}

func send(event PluginEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		return
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(ingestURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return
	}
	resp.Body.Close()
}
