package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wambozi/sigil/internal/event"
	"github.com/wambozi/sigil/internal/store"
)

// --- stub StoreReader --------------------------------------------------------

// stubStore is a controllable implementation of StoreReader for tests.
type stubStore struct {
	currentTask       *store.TaskRecord
	currentTaskErr    error
	taskHistory       []store.TaskRecord
	taskHistoryErr    error
	tasksByDate       []store.TaskRecord
	tasksByDateErr    error
	latestPrediction  *store.PredictionRecord
	latestPredErr     error
	predictions       []store.PredictionRecord
	predictionsErr    error
	suggestions       []store.Suggestion
	suggestionsErr    error
	topFiles          []store.FileEditCount
	topFilesErr       error
	terminalEvents    []event.Event
	terminalEventsErr error
	pluginEvents      []store.PluginEventRecord
	pluginEventsErr   error
}

func (s *stubStore) QueryCurrentTask(_ context.Context) (*store.TaskRecord, error) {
	return s.currentTask, s.currentTaskErr
}

func (s *stubStore) QueryTaskHistory(_ context.Context, _ time.Time, _ int) ([]store.TaskRecord, error) {
	return s.taskHistory, s.taskHistoryErr
}

func (s *stubStore) QueryTasksByDate(_ context.Context, _ time.Time) ([]store.TaskRecord, error) {
	return s.tasksByDate, s.tasksByDateErr
}

func (s *stubStore) QueryLatestPrediction(_ context.Context, _ string) (*store.PredictionRecord, error) {
	return s.latestPrediction, s.latestPredErr
}

func (s *stubStore) QueryPredictions(_ context.Context, _ string, _ time.Time) ([]store.PredictionRecord, error) {
	return s.predictions, s.predictionsErr
}

func (s *stubStore) QuerySuggestions(_ context.Context, _ store.SuggestionStatus, _ int) ([]store.Suggestion, error) {
	return s.suggestions, s.suggestionsErr
}

func (s *stubStore) QueryTopFiles(_ context.Context, _ time.Time, _ int) ([]store.FileEditCount, error) {
	return s.topFiles, s.topFilesErr
}

func (s *stubStore) QueryTerminalEvents(_ context.Context, _ time.Time) ([]event.Event, error) {
	return s.terminalEvents, s.terminalEventsErr
}

func (s *stubStore) QueryPluginEvents(_ context.Context, _ string, _ time.Time, _ int) ([]store.PluginEventRecord, error) {
	return s.pluginEvents, s.pluginEventsErr
}

// helper: build a registry with all store tools registered against s.
func registryWith(s StoreReader) *Registry {
	r := NewRegistry()
	RegisterStoreTools(r, s)
	return r
}

// helper: execute a named tool and return its JSON output as a map.
func execJSON(t *testing.T, r *Registry, toolName, argsJSON string) map[string]any {
	t.Helper()
	out, err := r.Execute(context.Background(), toolName, argsJSON)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	return m
}

// helper: execute a named tool and return its raw string output.
func execRaw(t *testing.T, r *Registry, toolName, argsJSON string) string {
	t.Helper()
	out, err := r.Execute(context.Background(), toolName, argsJSON)
	require.NoError(t, err)
	return out
}

// --- RegisterStoreTools counts -----------------------------------------------

func TestRegisterStoreTools_RegistersExpectedCount(t *testing.T) {
	r := registryWith(&stubStore{})
	// 12 tools defined in tools.go.
	assert.Len(t, r.Tools(), 12)
}

// --- get_current_task --------------------------------------------------------

func TestTool_GetCurrentTask_NilTask(t *testing.T) {
	r := registryWith(&stubStore{currentTask: nil})
	out := execRaw(t, r, "get_current_task", `{}`)
	assert.JSONEq(t, `{"task": null}`, out)
}

func TestTool_GetCurrentTask_WithTask(t *testing.T) {
	task := &store.TaskRecord{
		ID:     "task-1",
		Branch: "main",
		Phase:  "build",
	}
	r := registryWith(&stubStore{currentTask: task})
	m := execJSON(t, r, "get_current_task", `{}`)
	assert.Equal(t, "task-1", m["ID"])
	assert.Equal(t, "main", m["Branch"])
}

func TestTool_GetCurrentTask_StoreError(t *testing.T) {
	r := registryWith(&stubStore{currentTaskErr: errors.New("db offline")})
	_, err := r.Execute(context.Background(), "get_current_task", `{}`)
	require.Error(t, err)
	assert.Equal(t, "db offline", err.Error())
}

// --- get_task_history --------------------------------------------------------

func TestTool_GetTaskHistory_DefaultLimit(t *testing.T) {
	tasks := []store.TaskRecord{
		{ID: "t1", Branch: "feature-a"},
		{ID: "t2", Branch: "feature-b"},
	}
	r := registryWith(&stubStore{taskHistory: tasks})
	out := execRaw(t, r, "get_task_history", `{}`)
	var got []store.TaskRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 2)
}

func TestTool_GetTaskHistory_CustomLimit(t *testing.T) {
	r := registryWith(&stubStore{taskHistory: []store.TaskRecord{{ID: "t1"}}})
	out := execRaw(t, r, "get_task_history", `{"limit": 3}`)
	var got []store.TaskRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 1)
}

func TestTool_GetTaskHistory_NegativeLimitDefaultsToFive(t *testing.T) {
	// Negative / zero limit should coerce to 5 internally — the stub just
	// returns whatever is configured; we only verify no error and valid JSON.
	r := registryWith(&stubStore{taskHistory: []store.TaskRecord{}})
	out := execRaw(t, r, "get_task_history", `{"limit": -1}`)
	var got []store.TaskRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetTaskHistory_StoreError(t *testing.T) {
	r := registryWith(&stubStore{taskHistoryErr: errors.New("timeout")})
	_, err := r.Execute(context.Background(), "get_task_history", `{}`)
	require.Error(t, err)
}

// --- get_predictions ---------------------------------------------------------

func TestTool_GetPredictions_NoModel_ReturnsCombined(t *testing.T) {
	pred := store.PredictionRecord{
		ID:    1,
		Model: "activity",
		Result: map[string]any{
			"state": "deep_work",
		},
		Confidence: 0.9,
		CreatedAt:  time.Now(),
	}
	r := registryWith(&stubStore{predictions: []store.PredictionRecord{pred}})
	out := execRaw(t, r, "get_predictions", `{}`)
	// May be null array or populated — just confirm valid JSON array.
	var got []store.PredictionRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
}

func TestTool_GetPredictions_WithModel_FoundPred(t *testing.T) {
	pred := &store.PredictionRecord{
		ID:         2,
		Model:      "quality",
		Result:     map[string]any{"score": 0.85},
		Confidence: 0.7,
		CreatedAt:  time.Now(),
	}
	r := registryWith(&stubStore{latestPrediction: pred})
	out := execRaw(t, r, "get_predictions", `{"model": "quality"}`)
	var got []store.PredictionRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "quality", got[0].Model)
}

func TestTool_GetPredictions_WithModel_NilPred(t *testing.T) {
	r := registryWith(&stubStore{latestPrediction: nil})
	out := execRaw(t, r, "get_predictions", `{"model": "quality"}`)
	assert.JSONEq(t, `{"predictions": []}`, out)
}

func TestTool_GetPredictions_WithModel_StoreError(t *testing.T) {
	r := registryWith(&stubStore{latestPredErr: errors.New("query failed")})
	_, err := r.Execute(context.Background(), "get_predictions", `{"model": "quality"}`)
	require.Error(t, err)
}

func TestTool_GetPredictions_NoModel_QueryErrorSkipped(t *testing.T) {
	// QueryPredictions returning an error per model is swallowed — overall
	// result is an empty (but valid) JSON array.
	r := registryWith(&stubStore{predictionsErr: errors.New("db error")})
	out := execRaw(t, r, "get_predictions", `{}`)
	// null marshals as "null" — valid JSON; or an empty array.
	assert.NotEmpty(t, out)
}

// --- get_quality_score -------------------------------------------------------

func TestTool_GetQualityScore_WithPred(t *testing.T) {
	pred := &store.PredictionRecord{
		ID:         3,
		Model:      "quality",
		Result:     map[string]any{"score": 0.9},
		Confidence: 0.95,
		CreatedAt:  time.Now(),
	}
	r := registryWith(&stubStore{latestPrediction: pred})
	out := execRaw(t, r, "get_quality_score", `{}`)
	var got store.PredictionRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "quality", got.Model)
}

func TestTool_GetQualityScore_NilPred(t *testing.T) {
	r := registryWith(&stubStore{latestPrediction: nil})
	out := execRaw(t, r, "get_quality_score", `{}`)
	assert.JSONEq(t, `{"quality": null}`, out)
}

func TestTool_GetQualityScore_StoreError(t *testing.T) {
	r := registryWith(&stubStore{latestPredErr: errors.New("fail")})
	_, err := r.Execute(context.Background(), "get_quality_score", `{}`)
	require.Error(t, err)
}

// --- get_suggestions ---------------------------------------------------------

func TestTool_GetSuggestions_Default(t *testing.T) {
	sgs := []store.Suggestion{
		{ID: 1, Title: "focus up", Category: "pattern", Status: store.StatusPending},
		{ID: 2, Title: "take a break", Category: "reminder", Status: store.StatusShown},
	}
	r := registryWith(&stubStore{suggestions: sgs})
	out := execRaw(t, r, "get_suggestions", `{}`)
	var got []store.Suggestion
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 2)
}

func TestTool_GetSuggestions_CustomLimit(t *testing.T) {
	r := registryWith(&stubStore{suggestions: []store.Suggestion{}})
	out := execRaw(t, r, "get_suggestions", `{"limit": 20}`)
	var got []store.Suggestion
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetSuggestions_ZeroLimitDefaultsTen(t *testing.T) {
	r := registryWith(&stubStore{suggestions: []store.Suggestion{}})
	out := execRaw(t, r, "get_suggestions", `{"limit": 0}`)
	var got []store.Suggestion
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetSuggestions_StoreError(t *testing.T) {
	r := registryWith(&stubStore{suggestionsErr: errors.New("err")})
	_, err := r.Execute(context.Background(), "get_suggestions", `{}`)
	require.Error(t, err)
}

// --- get_top_files -----------------------------------------------------------

func TestTool_GetTopFiles_WithResults(t *testing.T) {
	files := []store.FileEditCount{
		{Path: "main.go", Count: 10},
		{Path: "handler.go", Count: 7},
	}
	r := registryWith(&stubStore{topFiles: files})
	out := execRaw(t, r, "get_top_files", `{}`)
	var got []store.FileEditCount
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "main.go", got[0].Path)
}

func TestTool_GetTopFiles_Empty(t *testing.T) {
	r := registryWith(&stubStore{topFiles: []store.FileEditCount{}})
	out := execRaw(t, r, "get_top_files", `{}`)
	var got []store.FileEditCount
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetTopFiles_StoreError(t *testing.T) {
	r := registryWith(&stubStore{topFilesErr: errors.New("io error")})
	_, err := r.Execute(context.Background(), "get_top_files", `{}`)
	require.Error(t, err)
}

// --- get_pr_status -----------------------------------------------------------

func TestTool_GetPRStatus_FiltersByKind(t *testing.T) {
	evts := []store.PluginEventRecord{
		{ID: 1, Plugin: "github", Kind: "pr_status", Payload: map[string]any{"number": float64(42)}},
		{ID: 2, Plugin: "github", Kind: "ci_status", Payload: map[string]any{"run": "passed"}},
		{ID: 3, Plugin: "github", Kind: "pr_status", Payload: map[string]any{"number": float64(43)}},
	}
	r := registryWith(&stubStore{pluginEvents: evts})
	out := execRaw(t, r, "get_pr_status", `{}`)
	var got []store.PluginEventRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
	for _, g := range got {
		assert.Equal(t, "pr_status", g.Kind)
	}
}

func TestTool_GetPRStatus_NoMatches(t *testing.T) {
	evts := []store.PluginEventRecord{
		{ID: 1, Plugin: "github", Kind: "ci_status"},
	}
	r := registryWith(&stubStore{pluginEvents: evts})
	out := execRaw(t, r, "get_pr_status", `{}`)
	var got []store.PluginEventRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetPRStatus_StoreError(t *testing.T) {
	r := registryWith(&stubStore{pluginEventsErr: errors.New("store err")})
	_, err := r.Execute(context.Background(), "get_pr_status", `{}`)
	require.Error(t, err)
}

// --- get_ci_status -----------------------------------------------------------

func TestTool_GetCIStatus_FiltersByKind(t *testing.T) {
	evts := []store.PluginEventRecord{
		{ID: 1, Plugin: "github", Kind: "ci_status", Payload: map[string]any{"status": "pass"}},
		{ID: 2, Plugin: "github", Kind: "pr_status", Payload: map[string]any{"number": float64(1)}},
	}
	r := registryWith(&stubStore{pluginEvents: evts})
	out := execRaw(t, r, "get_ci_status", `{}`)
	var got []store.PluginEventRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "ci_status", got[0].Kind)
}

func TestTool_GetCIStatus_Empty(t *testing.T) {
	r := registryWith(&stubStore{pluginEvents: []store.PluginEventRecord{}})
	out := execRaw(t, r, "get_ci_status", `{}`)
	var got []store.PluginEventRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetCIStatus_StoreError(t *testing.T) {
	r := registryWith(&stubStore{pluginEventsErr: errors.New("err")})
	_, err := r.Execute(context.Background(), "get_ci_status", `{}`)
	require.Error(t, err)
}

// --- get_recent_commands -----------------------------------------------------

func TestTool_GetRecentCommands_WithEvents(t *testing.T) {
	evts := []event.Event{
		{ID: 1, Kind: event.KindTerminal, Source: "terminal", Timestamp: time.Now()},
		{ID: 2, Kind: event.KindTerminal, Source: "terminal", Timestamp: time.Now()},
	}
	r := registryWith(&stubStore{terminalEvents: evts})
	out := execRaw(t, r, "get_recent_commands", `{}`)
	var got []event.Event
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 2)
}

func TestTool_GetRecentCommands_Empty(t *testing.T) {
	r := registryWith(&stubStore{terminalEvents: []event.Event{}})
	out := execRaw(t, r, "get_recent_commands", `{}`)
	var got []event.Event
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetRecentCommands_StoreError(t *testing.T) {
	r := registryWith(&stubStore{terminalEventsErr: errors.New("err")})
	_, err := r.Execute(context.Background(), "get_recent_commands", `{}`)
	require.Error(t, err)
}

// --- get_day_summary ---------------------------------------------------------

func TestTool_GetDaySummary_WithTasks(t *testing.T) {
	tasks := []store.TaskRecord{
		{ID: "t1", Branch: "feat-x"},
		{ID: "t2", Branch: "feat-y"},
	}
	r := registryWith(&stubStore{tasksByDate: tasks})
	out := execRaw(t, r, "get_day_summary", `{}`)
	var got []store.TaskRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Len(t, got, 2)
}

func TestTool_GetDaySummary_Empty(t *testing.T) {
	r := registryWith(&stubStore{tasksByDate: []store.TaskRecord{}})
	out := execRaw(t, r, "get_day_summary", `{}`)
	var got []store.TaskRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Empty(t, got)
}

func TestTool_GetDaySummary_StoreError(t *testing.T) {
	r := registryWith(&stubStore{tasksByDateErr: errors.New("db err")})
	_, err := r.Execute(context.Background(), "get_day_summary", `{}`)
	require.Error(t, err)
}

// --- get_workflow_state ------------------------------------------------------

func TestTool_GetWorkflowState_WithPred(t *testing.T) {
	pred := &store.PredictionRecord{
		ID:         10,
		Model:      "suggest",
		Result:     map[string]any{"state": "deep_work", "momentum": 0.7},
		Confidence: 0.88,
		CreatedAt:  time.Now(),
	}
	r := registryWith(&stubStore{latestPrediction: pred})
	out := execRaw(t, r, "get_workflow_state", `{}`)
	var got store.PredictionRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "suggest", got.Model)
}

func TestTool_GetWorkflowState_NilPred(t *testing.T) {
	r := registryWith(&stubStore{latestPrediction: nil})
	out := execRaw(t, r, "get_workflow_state", `{}`)
	m := make(map[string]any)
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Nil(t, m["workflow_state"])
	assert.NotEmpty(t, m["note"])
}

func TestTool_GetWorkflowState_StoreError(t *testing.T) {
	r := registryWith(&stubStore{latestPredErr: errors.New("err")})
	_, err := r.Execute(context.Background(), "get_workflow_state", `{}`)
	require.Error(t, err)
}

// --- get_activity_stream -----------------------------------------------------

func TestTool_GetActivityStream_WithPred(t *testing.T) {
	pred := &store.PredictionRecord{
		ID:         11,
		Model:      "activity",
		Result:     map[string]any{"activity": "creating"},
		Confidence: 0.75,
		CreatedAt:  time.Now(),
	}
	r := registryWith(&stubStore{latestPrediction: pred})
	out := execRaw(t, r, "get_activity_stream", `{}`)
	var got store.PredictionRecord
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "activity", got.Model)
}

func TestTool_GetActivityStream_NilPred(t *testing.T) {
	r := registryWith(&stubStore{latestPrediction: nil})
	out := execRaw(t, r, "get_activity_stream", `{}`)
	m := make(map[string]any)
	require.NoError(t, json.Unmarshal([]byte(out), &m))
	assert.Nil(t, m["activity_stream"])
	assert.NotEmpty(t, m["note"])
}

func TestTool_GetActivityStream_StoreError(t *testing.T) {
	r := registryWith(&stubStore{latestPredErr: errors.New("err")})
	_, err := r.Execute(context.Background(), "get_activity_stream", `{}`)
	require.Error(t, err)
}
