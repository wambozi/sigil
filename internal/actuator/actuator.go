// Package actuator provides the active actuation layer for sigild.
// Actuators observe system state and emit reversible Actions that reshape
// the developer environment.
package actuator

import (
	"context"
	"time"
)

// Action is a single reversible actuation emitted by an Actuator.
type Action struct {
	ID          string    // unique within this daemon run; e.g. UUID or counter
	Description string    // human-readable
	ExecuteCmd  string    // shell command to execute; empty if action is informational only
	UndoCmd     string    // shell command to reverse this action; empty if irreversible
	ExpiresAt   time.Time // undo window (30s from creation)
}

// Actuator is implemented by each active actuation type.
type Actuator interface {
	Name() string
	// Check inspects current state and returns any Actions that should be taken.
	// Returning nil, nil means no action needed right now.
	Check(ctx context.Context) ([]Action, error)
}
