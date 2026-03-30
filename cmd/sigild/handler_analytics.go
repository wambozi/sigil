package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wambozi/sigil/internal/socket"
	"github.com/wambozi/sigil/internal/store"
)

// registerAnalyticsHandlers adds the analytics and export-suggestions socket methods.
func registerAnalyticsHandlers(srv *socket.Server, db *store.Store) {
	srv.Handle("analytics", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Days int `json:"days"`
		}
		if req.Payload != nil {
			_ = json.Unmarshal(req.Payload, &p)
		}
		if p.Days <= 0 {
			p.Days = 30
		}

		since := time.Now().AddDate(0, 0, -p.Days)
		sinceMS := since.UnixMilli()

		result, err := computeAnalytics(ctx, db, sinceMS, p.Days)
		if err != nil {
			return socket.Response{Error: err.Error()}
		}

		payload, _ := json.Marshal(result)
		return socket.Response{OK: true, Payload: payload}
	})

	srv.Handle("export-suggestions", func(ctx context.Context, req socket.Request) socket.Response {
		var p struct {
			Format string `json:"format"`
			From   string `json:"from"`
			To     string `json:"to"`
		}
		if req.Payload != nil {
			_ = json.Unmarshal(req.Payload, &p)
		}
		if p.Format == "" {
			p.Format = "json"
		}

		suggestions, err := db.QuerySuggestions(ctx, "", 1<<30)
		if err != nil {
			return socket.Response{Error: fmt.Sprintf("query suggestions: %v", err)}
		}

		// Filter by date range if provided.
		if p.From != "" || p.To != "" {
			suggestions = filterByDateRange(suggestions, p.From, p.To)
		}

		var data string
		switch p.Format {
		case "csv":
			data, err = suggestionsToCSV(suggestions)
		default:
			var b []byte
			b, err = json.MarshalIndent(suggestions, "", "  ")
			data = string(b)
		}
		if err != nil {
			return socket.Response{Error: err.Error()}
		}

		payload, _ := json.Marshal(map[string]string{"data": data})
		return socket.Response{OK: true, Payload: payload}
	})
}

type analyticsResult struct {
	DailyCounts        []dailyCount        `json:"daily_counts"`
	CategoryBreakdown  []categoryBreakdown `json:"category_breakdown"`
	HourlyDistribution [24]int             `json:"hourly_distribution"`
	StreakDays          int                 `json:"streak_days"`
}

type dailyCount struct {
	Date      string `json:"date"`
	Total     int    `json:"total"`
	Accepted  int    `json:"accepted"`
	Dismissed int    `json:"dismissed"`
	Pending   int    `json:"pending"`
}

type categoryBreakdown struct {
	Category       string  `json:"category"`
	Count          int     `json:"count"`
	AcceptanceRate float64 `json:"acceptance_rate"`
}

func computeAnalytics(ctx context.Context, db *store.Store, sinceMS int64, days int) (analyticsResult, error) {
	var result analyticsResult

	// Fetch all suggestions in the time range.
	allSuggestions, err := db.QuerySuggestions(ctx, "", 1<<30)
	if err != nil {
		return result, fmt.Errorf("query suggestions: %w", err)
	}

	// Filter to the date range.
	sinceTime := time.UnixMilli(sinceMS)
	var suggestions []store.Suggestion
	for _, sg := range allSuggestions {
		if !sg.CreatedAt.Before(sinceTime) {
			suggestions = append(suggestions, sg)
		}
	}

	// Daily counts.
	dailyMap := make(map[string]*dailyCount)
	for _, sg := range suggestions {
		date := sg.CreatedAt.Format("2006-01-02")
		dc, ok := dailyMap[date]
		if !ok {
			dc = &dailyCount{Date: date}
			dailyMap[date] = dc
		}
		dc.Total++
		switch sg.Status {
		case store.StatusAccepted:
			dc.Accepted++
		case store.StatusDismissed:
			dc.Dismissed++
		default:
			dc.Pending++
		}
	}

	// Build sorted daily counts for the full range.
	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -days+1+i).Format("2006-01-02")
		dc, ok := dailyMap[date]
		if !ok {
			dc = &dailyCount{Date: date}
		}
		result.DailyCounts = append(result.DailyCounts, *dc)
	}

	// Category breakdown.
	catMap := make(map[string]*categoryBreakdown)
	for _, sg := range suggestions {
		cb, ok := catMap[sg.Category]
		if !ok {
			cb = &categoryBreakdown{Category: sg.Category}
			catMap[sg.Category] = cb
		}
		cb.Count++
	}
	// Compute acceptance rates per category.
	catAccepted := make(map[string]int)
	catResolved := make(map[string]int)
	for _, sg := range suggestions {
		if sg.Status == store.StatusAccepted {
			catAccepted[sg.Category]++
			catResolved[sg.Category]++
		} else if sg.Status == store.StatusDismissed {
			catResolved[sg.Category]++
		}
	}
	for cat, cb := range catMap {
		if catResolved[cat] > 0 {
			cb.AcceptanceRate = float64(catAccepted[cat]) / float64(catResolved[cat])
		}
		result.CategoryBreakdown = append(result.CategoryBreakdown, *cb)
	}

	// Hourly distribution.
	for _, sg := range suggestions {
		hour := sg.CreatedAt.Hour()
		result.HourlyDistribution[hour]++
	}

	// Streak: consecutive days with at least one accepted suggestion.
	result.StreakDays = computeStreak(dailyMap)

	return result, nil
}

// computeStreak counts consecutive days (ending today) with at least one accepted suggestion.
func computeStreak(dailyMap map[string]*dailyCount) int {
	streak := 0
	for i := 0; ; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		dc, ok := dailyMap[date]
		if !ok || dc.Accepted == 0 {
			break
		}
		streak++
	}
	return streak
}

func filterByDateRange(suggestions []store.Suggestion, from, to string) []store.Suggestion {
	var filtered []store.Suggestion
	for _, sg := range suggestions {
		dateStr := sg.CreatedAt.Format("2006-01-02")
		if from != "" && dateStr < from {
			continue
		}
		if to != "" && dateStr > to {
			continue
		}
		filtered = append(filtered, sg)
	}
	return filtered
}

func suggestionsToCSV(suggestions []store.Suggestion) (string, error) {
	var b strings.Builder
	w := csv.NewWriter(&b)
	if err := w.Write([]string{"id", "category", "confidence", "title", "body", "status", "created_at"}); err != nil {
		return "", err
	}
	for _, sg := range suggestions {
		if err := w.Write([]string{
			fmt.Sprintf("%d", sg.ID),
			sg.Category,
			fmt.Sprintf("%.2f", sg.Confidence),
			sg.Title,
			sg.Body,
			string(sg.Status),
			sg.CreatedAt.Format(time.RFC3339),
		}); err != nil {
			return "", err
		}
	}
	w.Flush()
	return b.String(), w.Error()
}
