package pluginutil

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoff(t *testing.T) {
	t.Run("exponential growth", func(t *testing.T) {
		prev := time.Duration(0)
		for i := 0; i < 5; i++ {
			d := Backoff(i, 5*time.Minute)
			// Each attempt's base should be larger than the previous base.
			// With jitter we can't be exact, but base doubles each time.
			if i > 0 && d <= prev/2 {
				t.Errorf("attempt %d: expected growth, got %v (prev %v)", i, d, prev)
			}
			prev = d
		}
	})

	t.Run("capped at maxDelay", func(t *testing.T) {
		maxDelay := 10 * time.Second
		// Attempt 20 would be 2^20 seconds without cap.
		d := Backoff(20, maxDelay)
		if d > maxDelay {
			t.Errorf("expected cap at %v, got %v", maxDelay, d)
		}
	})
}

func TestRetryDo(t *testing.T) {
	t.Run("success on first try", func(t *testing.T) {
		calls := 0
		err := RetryDo(context.Background(), 3, time.Second, func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("success on third try", func(t *testing.T) {
		calls := 0
		err := RetryDo(context.Background(), 5, 10*time.Millisecond, func() error {
			calls++
			if calls < 3 {
				return errors.New("transient")
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("failure after max attempts", func(t *testing.T) {
		calls := 0
		sentinel := errors.New("persistent")
		err := RetryDo(context.Background(), 3, 10*time.Millisecond, func() error {
			calls++
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		err := RetryDo(ctx, 10, 5*time.Second, func() error {
			calls++
			return errors.New("fail")
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})
}
