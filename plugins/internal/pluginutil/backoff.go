package pluginutil

import (
	"context"
	"math"
	"math/rand"
	"time"
)

// Backoff returns an exponential backoff duration with jitter, capped at maxDelay.
func Backoff(attempt int, maxDelay time.Duration) time.Duration {
	base := float64(time.Second) * math.Pow(2, float64(attempt))
	jitter := rand.Float64() * float64(time.Second)
	d := time.Duration(base + jitter)
	if d > maxDelay {
		return maxDelay
	}
	return d
}

// RetryDo executes fn with exponential backoff. Returns the last error after maxAttempts.
func RetryDo(ctx context.Context, maxAttempts int, maxDelay time.Duration, fn func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < maxAttempts-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(Backoff(i, maxDelay)):
			}
		}
	}
	return err
}
