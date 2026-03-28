//go:build linux

package socket

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// cpuSample holds a snapshot of process CPU ticks and wall-clock time.
type cpuSample struct {
	ticks int64
	when  time.Time
}

var cpuCache sync.Map // pid → cpuSample

// ProcRSS reads VmRSS from /proc/{pid}/status and returns the value in bytes.
func ProcRSS(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, fmt.Errorf("procmetrics: read status: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0, errors.New("procmetrics: malformed VmRSS line")
			}
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("procmetrics: parse VmRSS: %w", err)
			}
			return kb * 1024, nil // convert kB to bytes
		}
	}
	return 0, errors.New("procmetrics: VmRSS not found in /proc/status")
}

// ProcCPUPercent returns the CPU usage percentage of a process since the last
// call for the same PID. The first call for a given PID returns 0.0 and caches
// the initial sample. Subsequent calls compute the delta.
func ProcCPUPercent(pid int) (float64, error) {
	ticks, err := readProcStatTicks(pid)
	if err != nil {
		return 0, err
	}

	now := time.Now()
	key := pid
	prev, loaded := cpuCache.Swap(key, cpuSample{ticks: ticks, when: now})
	if !loaded {
		return 0.0, nil // first sample — no delta yet
	}

	prevSample := prev.(cpuSample)
	elapsed := now.Sub(prevSample.when).Seconds()
	if elapsed <= 0 {
		return 0.0, nil
	}

	// Clock ticks per second (sysconf SC_CLK_TCK) is almost always 100 on Linux.
	const clkTck = 100.0
	deltaTicks := float64(ticks - prevSample.ticks)
	pct := (deltaTicks / clkTck / elapsed) * 100.0
	if pct < 0 {
		pct = 0
	}
	if pct > 100*16 { // sanity cap (16 cores max)
		pct = 100 * 16
	}
	return pct, nil
}

// readProcStatTicks reads /proc/{pid}/stat and returns the sum of utime + stime
// (fields 14 and 15, 1-indexed) in clock ticks.
func readProcStatTicks(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("procmetrics: read stat: %w", err)
	}
	// /proc/pid/stat format: pid (comm) state ... — comm may contain spaces/parens,
	// so find the last ')' to skip it.
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, errors.New("procmetrics: malformed /proc/stat")
	}
	// Fields after ')' start at index 3 (1-indexed: state is field 3).
	// utime is field 14, stime is field 15 → offsets 11 and 12 from the fields after ')'.
	fields := strings.Fields(s[idx+2:])
	if len(fields) < 13 {
		return 0, errors.New("procmetrics: too few fields in /proc/stat")
	}
	utime, err := strconv.ParseInt(fields[11], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("procmetrics: parse utime: %w", err)
	}
	stime, err := strconv.ParseInt(fields[12], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("procmetrics: parse stime: %w", err)
	}
	return utime + stime, nil
}
