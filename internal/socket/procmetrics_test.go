//go:build linux

package socket

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcRSS_ownProcess(t *testing.T) {
	rss, err := ProcRSS(os.Getpid())
	require.NoError(t, err)
	assert.Greater(t, rss, int64(0), "own process RSS should be > 0")
	// Sanity: RSS should be less than 1 GB for a test process.
	assert.Less(t, rss, int64(1<<30), "own process RSS should be < 1 GB")
}

func TestProcRSS_invalidPID(t *testing.T) {
	_, err := ProcRSS(999999999)
	assert.Error(t, err)
}

func TestProcCPUPercent_ownProcess(t *testing.T) {
	pid := os.Getpid()

	// First call: returns 0 (initial sample).
	pct, err := ProcCPUPercent(pid)
	require.NoError(t, err)
	assert.Equal(t, 0.0, pct, "first call should return 0")

	// Do some work to burn CPU ticks.
	sum := 0
	for i := 0; i < 1_000_000; i++ {
		sum += i
	}
	_ = sum

	// Second call: should return a non-negative percentage.
	pct, err = ProcCPUPercent(pid)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pct, 0.0, "CPU percent should be >= 0")
	assert.Less(t, pct, 1700.0, "CPU percent should be < 1700 (sanity cap)")
}

func TestProcCPUPercent_invalidPID(t *testing.T) {
	_, err := ProcCPUPercent(999999999)
	assert.Error(t, err)
}
