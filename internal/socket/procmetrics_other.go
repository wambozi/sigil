//go:build !linux

package socket

import "errors"

// ErrUnsupported is returned by process metrics functions on non-Linux platforms.
var ErrUnsupported = errors.New("procmetrics: not supported on this platform")

// ProcRSS is not supported on non-Linux platforms.
func ProcRSS(_ int) (int64, error) {
	return 0, ErrUnsupported
}

// ProcCPUPercent is not supported on non-Linux platforms.
func ProcCPUPercent(_ int) (float64, error) {
	return 0, ErrUnsupported
}
