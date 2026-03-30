package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/wambozi/sigil/internal/event"
)

func TestSummarizeEvent(t *testing.T) {
	tests := []struct {
		name string
		e    event.Event
		want string
	}{
		{
			name: "terminal event with cmd",
			e:    event.Event{Kind: event.KindTerminal, Payload: map[string]any{"cmd": "make build"}},
			want: "make build",
		},
		{
			name: "file event with path",
			e:    event.Event{Kind: event.KindFile, Payload: map[string]any{"path": "/home/nick/main.go"}},
			want: "/home/nick/main.go",
		},
		{
			name: "git event with message",
			e:    event.Event{Kind: event.KindGit, Payload: map[string]any{"message": "feat: add metrics"}},
			want: "feat: add metrics",
		},
		{
			name: "git event with action fallback",
			e:    event.Event{Kind: event.KindGit, Payload: map[string]any{"action": "push"}},
			want: "push",
		},
		{
			name: "process event with name",
			e:    event.Event{Kind: event.KindProcess, Payload: map[string]any{"name": "node"}},
			want: "node",
		},
		{
			name: "hyprland event with title",
			e:    event.Event{Kind: event.KindHyprland, Payload: map[string]any{"title": "VS Code"}},
			want: "VS Code",
		},
		{
			name: "unknown kind",
			e:    event.Event{Kind: event.KindAI, Payload: map[string]any{}},
			want: "ai event",
		},
		{
			name: "terminal event missing cmd field",
			e:    event.Event{Kind: event.KindTerminal, Payload: map[string]any{}},
			want: "terminal event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.e.Timestamp = time.Now()
			got := summarizeEvent(tt.e)
			assert.Equal(t, tt.want, got)
		})
	}
}
