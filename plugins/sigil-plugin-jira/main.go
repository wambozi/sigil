// Command sigil-plugin-jira polls the Jira REST API for stories assigned to
// the current user and pushes structured data to sigild's plugin ingest endpoint.
//
// It fetches: assigned stories with acceptance criteria, sprint context,
// status transitions, linked issues, and comments — all data that doesn't
// exist locally.
//
// Auth: JIRA_URL, JIRA_EMAIL, JIRA_TOKEN env vars (or from sigild config).
// Install: ships with sigild (make build / make install).
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

const (
	defaultIngestURL = "http://127.0.0.1:7775/api/v1/ingest"
	pluginName       = "jira"
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
	jiraURL      string
	jiraEmail    string
	jiraToken    string
)

func main() {
	flag.StringVar(&ingestURL, "sigil-ingest-url", defaultIngestURL, "Sigil ingest URL")
	flag.DurationVar(&pollInterval, "poll-interval", 5*time.Minute, "Poll interval")
	flag.Parse()

	jiraURL = os.Getenv("JIRA_URL")
	jiraEmail = os.Getenv("JIRA_EMAIL")
	jiraToken = os.Getenv("JIRA_TOKEN")

	if jiraURL == "" || jiraEmail == "" || jiraToken == "" {
		fmt.Fprintln(os.Stderr, "sigil-plugin-jira: JIRA_URL, JIRA_EMAIL, and JIRA_TOKEN must be set")
		os.Exit(1)
	}

	// Normalize URL.
	jiraURL = strings.TrimRight(jiraURL, "/")

	fmt.Fprintf(os.Stderr, "sigil-plugin-jira: polling %s every %s\n", jiraURL, pollInterval)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)

	pollAll()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			fmt.Fprintln(os.Stderr, "sigil-plugin-jira: shutting down")
			return
		case <-ticker.C:
			pollAll()
		}
	}
}

func pollAll() {
	// Fetch issues assigned to the current user that are in progress.
	issues := fetchAssignedIssues()
	for _, issue := range issues {
		emitStory(issue)
		fetchComments(issue)
		fetchTransitions(issue)
	}

	// Fetch current sprint info.
	fetchActiveSprint()
}

// --- Jira API types ---

type jiraSearchResult struct {
	Issues []jiraIssue `json:"issues"`
	Total  int         `json:"total"`
}

type jiraIssue struct {
	Key    string `json:"key"` // e.g. "PROJ-123"
	Fields struct {
		Summary     string `json:"summary"`
		Description string `json:"description"`
		Status      struct {
			Name string `json:"name"`
		} `json:"status"`
		Priority struct {
			Name string `json:"name"`
		} `json:"priority"`
		IssueType struct {
			Name string `json:"name"`
		} `json:"issuetype"`
		Assignee struct {
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
		} `json:"assignee"`
		Reporter struct {
			DisplayName string `json:"displayName"`
		} `json:"reporter"`
		Sprint *struct {
			Name  string `json:"name"`
			State string `json:"state"`
			Goal  string `json:"goal"`
		} `json:"sprint"`
		Labels      []string `json:"labels"`
		StoryPoints *float64 `json:"story_points"`
		Parent      *struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		} `json:"parent"`
		Subtasks []struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
				Status  struct {
					Name string `json:"name"`
				} `json:"status"`
			} `json:"fields"`
		} `json:"subtasks"`
		Created string `json:"created"`
		Updated string `json:"updated"`
	} `json:"fields"`
}

func fetchAssignedIssues() []jiraIssue {
	// JQL: assigned to current user, not done, ordered by priority.
	jql := url.QueryEscape("assignee = currentUser() AND statusCategory != Done ORDER BY priority ASC, updated DESC")
	endpoint := fmt.Sprintf("%s/rest/api/3/search?jql=%s&maxResults=20&fields=summary,description,status,priority,issuetype,assignee,reporter,sprint,labels,story_points,parent,subtasks,created,updated", jiraURL, jql)

	body, err := jiraGet(endpoint)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-jira: search failed: %v\n", err)
		return nil
	}

	var result jiraSearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Fprintf(os.Stderr, "sigil-plugin-jira: parse search: %v\n", err)
		return nil
	}

	return result.Issues
}

func emitStory(issue jiraIssue) {
	f := issue.Fields

	subtasks := make([]map[string]any, 0, len(f.Subtasks))
	for _, st := range f.Subtasks {
		subtasks = append(subtasks, map[string]any{
			"key":     st.Key,
			"summary": st.Fields.Summary,
			"status":  st.Fields.Status.Name,
		})
	}

	payload := map[string]any{
		"key":         issue.Key,
		"summary":     f.Summary,
		"description": truncate(f.Description, 500),
		"status":      f.Status.Name,
		"priority":    f.Priority.Name,
		"type":        f.IssueType.Name,
		"reporter":    f.Reporter.DisplayName,
		"labels":      f.Labels,
		"subtasks":    subtasks,
		"created":     f.Created,
		"updated":     f.Updated,
		"url":         fmt.Sprintf("%s/browse/%s", jiraURL, issue.Key),
	}

	if f.StoryPoints != nil {
		payload["story_points"] = *f.StoryPoints
	}
	if f.Sprint != nil {
		payload["sprint_name"] = f.Sprint.Name
		payload["sprint_state"] = f.Sprint.State
		payload["sprint_goal"] = f.Sprint.Goal
	}
	if f.Parent != nil {
		payload["epic_key"] = f.Parent.Key
		payload["epic_summary"] = f.Parent.Fields.Summary
	}

	// Try to extract acceptance criteria from description.
	if ac := extractAcceptanceCriteria(f.Description); len(ac) > 0 {
		payload["acceptance_criteria"] = ac
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "story",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"story_id": issue.Key,
		},
		Payload: payload,
	})
}

func fetchComments(issue jiraIssue) {
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s/comment?maxResults=10&orderBy=-created", jiraURL, issue.Key)
	body, err := jiraGet(endpoint)
	if err != nil {
		return
	}

	var result struct {
		Comments []struct {
			Author struct {
				DisplayName string `json:"displayName"`
			} `json:"author"`
			Body    string `json:"body"`
			Created string `json:"created"`
			Updated string `json:"updated"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Comments) == 0 {
		return
	}

	type commentSummary struct {
		Author  string `json:"author"`
		Body    string `json:"body"`
		Created string `json:"created"`
	}
	summaries := make([]commentSummary, 0, len(result.Comments))
	for _, c := range result.Comments {
		// Extract plain text from Atlassian Document Format.
		plainBody := extractPlainText(c.Body)
		summaries = append(summaries, commentSummary{
			Author:  c.Author.DisplayName,
			Body:    truncate(plainBody, 300),
			Created: c.Created,
		})
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "story_comments",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"story_id": issue.Key,
		},
		Payload: map[string]any{
			"key":           issue.Key,
			"comment_count": len(result.Comments),
			"comments":      summaries,
		},
	})
}

func fetchTransitions(issue jiraIssue) {
	endpoint := fmt.Sprintf("%s/rest/api/3/issue/%s/transitions", jiraURL, issue.Key)
	body, err := jiraGet(endpoint)
	if err != nil {
		return
	}

	var result struct {
		Transitions []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			To   struct {
				Name string `json:"name"`
			} `json:"to"`
		} `json:"transitions"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Transitions) == 0 {
		return
	}

	transitions := make([]map[string]any, 0, len(result.Transitions))
	for _, t := range result.Transitions {
		transitions = append(transitions, map[string]any{
			"id":   t.ID,
			"name": t.Name,
			"to":   t.To.Name,
		})
	}

	send(PluginEvent{
		Plugin:    pluginName,
		Kind:      "story_transitions",
		Timestamp: time.Now(),
		Correlation: map[string]any{
			"story_id": issue.Key,
		},
		Payload: map[string]any{
			"key":         issue.Key,
			"transitions": transitions,
		},
	})
}

func fetchActiveSprint() {
	// Fetch boards the user has access to, then get the active sprint.
	endpoint := fmt.Sprintf("%s/rest/agile/1.0/board?maxResults=5", jiraURL)
	body, err := jiraGet(endpoint)
	if err != nil {
		return
	}

	var boards struct {
		Values []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.Unmarshal(body, &boards); err != nil || len(boards.Values) == 0 {
		return
	}

	for _, board := range boards.Values {
		sprintEndpoint := fmt.Sprintf("%s/rest/agile/1.0/board/%d/sprint?state=active", jiraURL, board.ID)
		sprintBody, err := jiraGet(sprintEndpoint)
		if err != nil {
			continue
		}

		var sprints struct {
			Values []struct {
				ID        int    `json:"id"`
				Name      string `json:"name"`
				State     string `json:"state"`
				Goal      string `json:"goal"`
				StartDate string `json:"startDate"`
				EndDate   string `json:"endDate"`
			} `json:"values"`
		}
		if err := json.Unmarshal(sprintBody, &sprints); err != nil || len(sprints.Values) == 0 {
			continue
		}

		for _, sprint := range sprints.Values {
			send(PluginEvent{
				Plugin:    pluginName,
				Kind:      "sprint",
				Timestamp: time.Now(),
				Payload: map[string]any{
					"board":      board.Name,
					"sprint":     sprint.Name,
					"state":      sprint.State,
					"goal":       sprint.Goal,
					"start_date": sprint.StartDate,
					"end_date":   sprint.EndDate,
				},
			})
		}
	}
}

// --- Helpers ---

func jiraGet(endpoint string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	// Basic auth: email:token base64-encoded.
	auth := base64.StdEncoding.EncodeToString([]byte(jiraEmail + ":" + jiraToken))
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jira API %d for %s", resp.StatusCode, endpoint)
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractAcceptanceCriteria tries to find AC in a description string.
// Looks for lines starting with "- [ ]", "- [x]", "* ", or numbered items
// after a heading containing "acceptance" or "criteria".
func extractAcceptanceCriteria(desc string) []string {
	if desc == "" {
		return nil
	}
	lines := strings.Split(desc, "\n")
	var ac []string
	inSection := false
	for _, line := range lines {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "acceptance") || strings.Contains(lower, "criteria") || strings.Contains(lower, "definition of done") {
			inSection = true
			continue
		}
		if inSection {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				if len(ac) > 0 {
					break // end of section
				}
				continue
			}
			// Strip markdown list markers.
			trimmed = strings.TrimLeft(trimmed, "- *[]x0123456789.)")
			trimmed = strings.TrimSpace(trimmed)
			if trimmed != "" {
				ac = append(ac, trimmed)
			}
		}
	}
	return ac
}

// extractPlainText attempts to get plain text from Jira's Atlassian Document Format.
// ADF is JSON; for simple comments we just extract text nodes.
func extractPlainText(body string) string {
	// If it's not JSON (older Jira or plain text), return as-is.
	if !strings.HasPrefix(strings.TrimSpace(body), "{") {
		return body
	}
	var doc struct {
		Content []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return body
	}
	var parts []string
	for _, block := range doc.Content {
		for _, inline := range block.Content {
			if inline.Text != "" {
				parts = append(parts, inline.Text)
			}
		}
	}
	if len(parts) == 0 {
		return body
	}
	return strings.Join(parts, " ")
}
