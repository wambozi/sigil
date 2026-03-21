// Command sigil-plugin-github polls GitHub for PR status, CI checks, reviews,
// and issue links, then pushes structured events to sigild's plugin ingest endpoint.
//
// It discovers git repos by scanning common project directories, reads all
// GitHub remotes (origin + upstream), and polls the API for open PRs on the
// current branch.
//
// Auth: uses GITHUB_TOKEN env var, falls back to gh CLI auth token.
// Install: ships with sigild (make build / make install)
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

// ghRepo represents a discovered GitHub repo with its local path and remote info.
type ghRepo struct {
	LocalPath string
	Owner     string
	Repo      string
	Remote    string // "origin" or "upstream"
	Branch    string
}

var (
	ingestURL    string
	pollInterval time.Duration
	token        string
	watchDirs    string
)

func main() {
	flag.StringVar(&ingestURL, "sigil-ingest-url", defaultIngestURL, "Sigil ingest URL")
	flag.DurationVar(&pollInterval, "poll-interval", 2*time.Minute, "Poll interval")
	flag.StringVar(&watchDirs, "watch-dirs", "", "Comma-separated directories to scan for git repos")
	flag.Parse()

	token = os.Getenv("GITHUB_TOKEN")
	if token == "" {
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token = strings.TrimSpace(string(out))
		}
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-github: no auth — set GITHUB_TOKEN or run 'gh auth login'")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "sigil-plugin-github: polling every %s\n", pollInterval)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

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

func pollAll() {
	repos := discoverGHRepos()
	if len(repos) == 0 {
		return
	}

	seen := make(map[string]bool) // dedupe by owner/repo/branch
	for _, r := range repos {
		key := r.Owner + "/" + r.Repo + "/" + r.Branch
		if seen[key] {
			continue
		}
		seen[key] = true

		pollPRs(r)
		pollCIStatus(r)
	}
}

// discoverGHRepos finds git repos and extracts all GitHub remotes.
func discoverGHRepos() []ghRepo {
	var dirs []string

	if watchDirs != "" {
		dirs = strings.Split(watchDirs, ",")
	} else {
		// Default: scan home directory common project locations.
		home, _ := os.UserHomeDir()
		for _, d := range []string{"PycharmProjects", "projects", "code", "src", "workspace", "dev"} {
			p := filepath.Join(home, d)
			if info, err := os.Stat(p); err == nil && info.IsDir() {
				dirs = append(dirs, p)
			}
		}
		// Also check cwd.
		if cwd, err := os.Getwd(); err == nil {
			dirs = append(dirs, cwd)
		}
	}

	var repos []ghRepo
	seen := make(map[string]bool)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			repoPath := filepath.Join(dir, e.Name())
			gitDir := filepath.Join(repoPath, ".git")
			if _, err := os.Stat(gitDir); err != nil {
				continue
			}
			if seen[repoPath] {
				continue
			}
			seen[repoPath] = true

			branch := readBranch(repoPath)
			for _, remote := range []string{"upstream", "origin"} {
				owner, repo := parseRemoteURL(repoPath, remote)
				if owner != "" && repo != "" {
					repos = append(repos, ghRepo{
						LocalPath: repoPath,
						Owner:     owner,
						Repo:      repo,
						Remote:    remote,
						Branch:    branch,
					})
				}
			}
		}
	}
	return repos
}

func parseRemoteURL(repoPath, remote string) (owner, repo string) {
	out, err := exec.Command("git", "-C", repoPath, "remote", "get-url", remote).Output()
	if err != nil {
		return "", ""
	}
	url := strings.TrimSpace(string(out))

	// SSH: git@github.com:owner/repo.git
	if strings.HasPrefix(url, "git@github.com:") {
		parts := strings.TrimPrefix(url, "git@github.com:")
		parts = strings.TrimSuffix(parts, ".git")
		split := strings.SplitN(parts, "/", 2)
		if len(split) == 2 {
			return split[0], split[1]
		}
	}

	// HTTPS: https://github.com/owner/repo.git
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

func readBranch(repoPath string) string {
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

func pollPRs(r ghRepo) {
	if r.Branch == "" || r.Branch == "main" || r.Branch == "master" {
		return
	}

	// For upstream remotes, the head is owner:branch where owner is the fork owner.
	head := r.Owner + ":" + r.Branch

	// If this is upstream, we need the fork owner (from origin remote).
	if r.Remote == "upstream" {
		forkOwner, _ := parseRemoteURL(r.LocalPath, "origin")
		if forkOwner != "" {
			head = forkOwner + ":" + r.Branch
		}
	}

	url := fmt.Sprintf("%s/repos/%s/%s/pulls?head=%s&state=open", githubAPI, r.Owner, r.Repo, head)
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
		for i, rv := range pr.RequestedReviewers {
			reviewers[i] = rv.Login
		}

		send(PluginEvent{
			Plugin:    pluginName,
			Kind:      "pr_status",
			Timestamp: time.Now(),
			Correlation: map[string]any{
				"repo_root": r.LocalPath,
				"branch":    r.Branch,
				"pr_id":     fmt.Sprintf("%d", pr.Number),
			},
			Payload: map[string]any{
				"number":      pr.Number,
				"title":       pr.Title,
				"state":       pr.State,
				"draft":       pr.Draft,
				"url":         pr.HTMLURL,
				"author":      pr.User.Login,
				"mergeable":   pr.Mergeable,
				"reviewers":   reviewers,
				"labels":      labels,
				"repo":        r.Owner + "/" + r.Repo,
				"remote":      r.Remote,
				"created_at":  pr.CreatedAt.Format(time.RFC3339),
				"updated_at":  pr.UpdatedAt.Format(time.RFC3339),
			},
		})

		pollReviews(r, pr.Number)
		pollPRComments(r, pr.Number)
		pollLinkedIssues(r, pr.Number, pr.Title)
	}
}

// pollPRComments fetches review comments and issue comments on a PR.
func pollPRComments(r ghRepo, prNumber int) {
	// Review comments (inline code comments).
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/comments?per_page=20&sort=updated&direction=desc",
		githubAPI, r.Owner, r.Repo, prNumber)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var comments []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Body      string    `json:"body"`
		Path      string    `json:"path"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.Unmarshal(body, &comments); err != nil || len(comments) == 0 {
		return
	}

	// Send a summary event with recent comments.
	type commentSummary struct {
		Author    string `json:"author"`
		Body      string `json:"body"`
		Path      string `json:"path,omitempty"`
		CreatedAt string `json:"created_at"`
	}
	summaries := make([]commentSummary, 0, len(comments))
	for _, c := range comments {
		summaries = append(summaries, commentSummary{
			Author:    c.User.Login,
			Body:      truncate(c.Body, 300),
			Path:      c.Path,
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		})
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "pr_comments",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": r.LocalPath,
			"branch":    r.Branch,
			"pr_id":     fmt.Sprintf("%d", prNumber),
		},
		Payload: map[string]any{
			"pr_number":     prNumber,
			"comment_count": len(comments),
			"comments":      summaries,
			"repo":          r.Owner + "/" + r.Repo,
		},
	})

	// Also fetch issue-level comments (general PR discussion, not inline).
	issueURL := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=20&sort=updated&direction=desc",
		githubAPI, r.Owner, r.Repo, prNumber)
	issueBody, err := ghGet(issueURL)
	if err != nil {
		return
	}

	var issueComments []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(issueBody, &issueComments); err != nil || len(issueComments) == 0 {
		return
	}

	issueSummaries := make([]commentSummary, 0, len(issueComments))
	for _, c := range issueComments {
		issueSummaries = append(issueSummaries, commentSummary{
			Author:    c.User.Login,
			Body:      truncate(c.Body, 300),
			CreatedAt: c.CreatedAt.Format(time.RFC3339),
		})
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "pr_discussion",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": r.LocalPath,
			"branch":    r.Branch,
			"pr_id":     fmt.Sprintf("%d", prNumber),
		},
		Payload: map[string]any{
			"pr_number":     prNumber,
			"comment_count": len(issueComments),
			"comments":      issueSummaries,
			"repo":          r.Owner + "/" + r.Repo,
		},
	})
}

// pollLinkedIssues extracts issue references from PR title/body and fetches their details.
func pollLinkedIssues(r ghRepo, prNumber int, prTitle string) {
	// Extract issue numbers from title (e.g. "fix #123", "closes #456").
	issueNums := extractIssueRefs(prTitle)
	if len(issueNums) == 0 {
		return
	}

	for _, num := range issueNums {
		url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", githubAPI, r.Owner, r.Repo, num)
		body, err := ghGet(url)
		if err != nil {
			continue
		}

		var issue struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
			Body   string `json:"body"`
			State  string `json:"state"`
			User   struct {
				Login string `json:"login"`
			} `json:"user"`
			Labels []struct {
				Name string `json:"name"`
			} `json:"labels"`
			Assignees []struct {
				Login string `json:"login"`
			} `json:"assignees"`
			Milestone *struct {
				Title string `json:"title"`
			} `json:"milestone"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		if err := json.Unmarshal(body, &issue); err != nil {
			continue
		}

		labels := make([]string, len(issue.Labels))
		for i, l := range issue.Labels {
			labels[i] = l.Name
		}
		assignees := make([]string, len(issue.Assignees))
		for i, a := range issue.Assignees {
			assignees[i] = a.Login
		}

		milestone := ""
		if issue.Milestone != nil {
			milestone = issue.Milestone.Title
		}

		send(PluginEvent{
			Plugin:    pluginName,
			Kind:      "linked_issue",
			Timestamp: time.Now(),
			Correlation: map[string]any{
				"repo_root": r.LocalPath,
				"branch":    r.Branch,
				"pr_id":     fmt.Sprintf("%d", prNumber),
			},
			Payload: map[string]any{
				"issue_number": issue.Number,
				"title":        issue.Title,
				"body":         truncate(issue.Body, 500),
				"state":        issue.State,
				"author":       issue.User.Login,
				"labels":       labels,
				"assignees":    assignees,
				"milestone":    milestone,
				"repo":         r.Owner + "/" + r.Repo,
				"created_at":   issue.CreatedAt.Format(time.RFC3339),
				"updated_at":   issue.UpdatedAt.Format(time.RFC3339),
			},
		})
	}
}

// extractIssueRefs finds #NNN references in text.
func extractIssueRefs(text string) []int {
	var refs []int
	for i := 0; i < len(text); i++ {
		if text[i] == '#' && i+1 < len(text) && text[i+1] >= '0' && text[i+1] <= '9' {
			j := i + 1
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			num := 0
			for _, c := range text[i+1 : j] {
				num = num*10 + int(c-'0')
			}
			if num > 0 {
				refs = append(refs, num)
			}
		}
	}
	return refs
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func pollReviews(r ghRepo, prNumber int) {
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", githubAPI, r.Owner, r.Repo, prNumber)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var reviews []struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		State       string    `json:"state"`
		SubmittedAt time.Time `json:"submitted_at"`
	}
	if err := json.Unmarshal(body, &reviews); err != nil || len(reviews) == 0 {
		return
	}

	states := make(map[string]int)
	for _, rv := range reviews {
		states[rv.State]++
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "pr_reviews",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": r.LocalPath,
			"branch":    r.Branch,
			"pr_id":     fmt.Sprintf("%d", prNumber),
		},
		Payload: map[string]any{
			"pr_number":         prNumber,
			"review_count":      len(reviews),
			"approved":          states["APPROVED"],
			"changes_requested": states["CHANGES_REQUESTED"],
			"commented":         states["COMMENTED"],
			"repo":              r.Owner + "/" + r.Repo,
		},
	})
}

func pollCIStatus(r ghRepo) {
	if r.Branch == "" {
		return
	}

	// Try check runs first (GitHub Actions).
	url := fmt.Sprintf("%s/repos/%s/%s/commits/%s/check-runs", githubAPI, r.Owner, r.Repo, r.Branch)
	body, err := ghGet(url)
	if err != nil {
		return
	}

	var result struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			Name       string  `json:"name"`
			Status     string  `json:"status"`
			Conclusion *string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &result); err != nil || result.TotalCount == 0 {
		return
	}

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

	state := "pending"
	if allComplete {
		if conclusions["failure"] > 0 {
			state = "failure"
		} else {
			state = "success"
		}
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "ci_status",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"repo_root": r.LocalPath,
			"branch":    r.Branch,
		},
		Payload: map[string]any{
			"state":       state,
			"total_count": result.TotalCount,
			"conclusions": conclusions,
			"repo":        r.Owner + "/" + r.Repo,
		},
	})
}

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
