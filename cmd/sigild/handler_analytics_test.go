package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wambozi/sigil/internal/store"
)

func TestComputeStreak(t *testing.T) {
	t.Parallel()

	t.Run("no_data", func(t *testing.T) {
		t.Parallel()
		result := computeStreak(map[string]*dailyCount{})
		assert.Equal(t, 0, result)
	})

	t.Run("today_only", func(t *testing.T) {
		t.Parallel()
		today := time.Now().Format("2006-01-02")
		m := map[string]*dailyCount{
			today: {Date: today, Accepted: 1},
		}
		assert.Equal(t, 1, computeStreak(m))
	})

	t.Run("consecutive_days", func(t *testing.T) {
		t.Parallel()
		m := make(map[string]*dailyCount)
		for i := 0; i < 5; i++ {
			date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
			m[date] = &dailyCount{Date: date, Accepted: 1}
		}
		assert.Equal(t, 5, computeStreak(m))
	})

	t.Run("broken_streak", func(t *testing.T) {
		t.Parallel()
		today := time.Now().Format("2006-01-02")
		twoDaysAgo := time.Now().AddDate(0, 0, -2).Format("2006-01-02")
		m := map[string]*dailyCount{
			today:      {Date: today, Accepted: 1},
			twoDaysAgo: {Date: twoDaysAgo, Accepted: 1},
		}
		// Streak should be 1 (only today, yesterday has no accepted).
		assert.Equal(t, 1, computeStreak(m))
	})
}

func TestFilterByDateRange(t *testing.T) {
	t.Parallel()

	suggestions := []store.Suggestion{
		{ID: 1, CreatedAt: time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)},
		{ID: 2, CreatedAt: time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)},
		{ID: 3, CreatedAt: time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC)},
	}

	t.Run("from_only", func(t *testing.T) {
		t.Parallel()
		result := filterByDateRange(suggestions, "2026-03-10", "")
		assert.Len(t, result, 2)
	})

	t.Run("to_only", func(t *testing.T) {
		t.Parallel()
		result := filterByDateRange(suggestions, "", "2026-03-20")
		assert.Len(t, result, 2)
	})

	t.Run("both", func(t *testing.T) {
		t.Parallel()
		result := filterByDateRange(suggestions, "2026-03-10", "2026-03-20")
		assert.Len(t, result, 1)
		assert.Equal(t, int64(2), result[0].ID)
	})

	t.Run("empty_range", func(t *testing.T) {
		t.Parallel()
		result := filterByDateRange(suggestions, "", "")
		assert.Len(t, result, 3)
	})
}

func TestSuggestionsToCSV(t *testing.T) {
	t.Parallel()

	suggestions := []store.Suggestion{
		{
			ID:         1,
			Category:   "workflow",
			Confidence: 0.85,
			Title:      "Test suggestion",
			Body:       "Some body text",
			Status:     store.StatusAccepted,
			CreatedAt:  time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
		},
	}

	result, err := suggestionsToCSV(suggestions)
	assert.NoError(t, err)
	assert.Contains(t, result, "id,category,confidence,title,body,status,created_at")
	assert.Contains(t, result, "1,workflow,0.85,Test suggestion,Some body text,accepted,")
}
