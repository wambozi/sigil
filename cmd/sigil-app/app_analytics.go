package main

import (
	"encoding/json"
	"fmt"
)

// AnalyticsResult holds aggregated suggestion analytics.
type AnalyticsResult struct {
	DailyCounts        []DailyCount        `json:"daily_counts"`
	CategoryBreakdown  []CategoryBreakdown `json:"category_breakdown"`
	HourlyDistribution [24]int             `json:"hourly_distribution"`
	StreakDays          int                 `json:"streak_days"`
}

// DailyCount is a single day's suggestion counts.
type DailyCount struct {
	Date      string `json:"date"`
	Total     int    `json:"total"`
	Accepted  int    `json:"accepted"`
	Dismissed int    `json:"dismissed"`
	Pending   int    `json:"pending"`
}

// CategoryBreakdown is a single category's stats.
type CategoryBreakdown struct {
	Category       string  `json:"category"`
	Count          int     `json:"count"`
	AcceptanceRate float64 `json:"acceptance_rate"`
}

// ExportResult holds the exported data.
type ExportResult struct {
	Data string `json:"data"`
}

// GetAnalytics returns aggregated suggestion analytics for the given number of days.
func (a *App) GetAnalytics(days int) (AnalyticsResult, error) {
	if days <= 0 {
		days = 30
	}
	resp, err := a.call("analytics", map[string]any{"days": days})
	if err != nil {
		return AnalyticsResult{}, err
	}
	if !resp.OK {
		return AnalyticsResult{}, fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result AnalyticsResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return AnalyticsResult{}, fmt.Errorf("unmarshal analytics: %w", err)
	}
	return result, nil
}

// ExportSuggestions exports suggestion data in the requested format.
func (a *App) ExportSuggestions(format, from, to string) (string, error) {
	resp, err := a.call("export-suggestions", map[string]any{
		"format": format,
		"from":   from,
		"to":     to,
	})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", fmt.Errorf("daemon error: %s", resp.Error)
	}
	var result ExportResult
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return "", fmt.Errorf("unmarshal export: %w", err)
	}
	return result.Data, nil
}
