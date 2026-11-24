package world

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// Claude Code keeps a background job's state at "done" from its last finished
// turn until the job says otherwise, which can be many minutes into the next
// command. The transcript knows sooner: a command the agent has not answered
// yet, or a tool it is still running, is work in progress.
func TestDoneBackgroundSessionWithAnOpenTurnIsRunning(t *testing.T) {
	const id = "abab1212-0000-0000-0000-000000000000"
	now := time.Date(2026, 11, 24, 3, 10, 0, 0, time.UTC)
	finished := `{"type":"user","timestamp":"2026-11-24T02:00:00Z","message":{"role":"user","content":"do it"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-11-24T02:05:00Z","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"result: done"}]}}` + "\n"
	tests := []struct {
		name string
		tail string
		want string
	}{
		{name: "the last turn ended", tail: "", want: StatusCompleted},
		{
			name: "a new command the agent has not answered yet",
			tail: `{"type":"user","timestamp":"2026-11-24T03:07:00Z","message":{"role":"user","content":"one more thing"}}` + "\n",
			want: StatusRunning,
		},
		{
			name: "a tool is still running",
			tail: `{"type":"user","timestamp":"2026-11-24T02:30:00Z","message":{"role":"user","content":"one more thing"}}` + "\n" +
				`{"type":"assistant","timestamp":"2026-11-24T02:31:00Z","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n",
			want: StatusRunning,
		},
		{
			name: "the new command was answered",
			tail: `{"type":"user","timestamp":"2026-11-24T03:07:00Z","message":{"role":"user","content":"one more thing"}}` + "\n" +
				`{"type":"assistant","timestamp":"2026-11-24T03:08:00Z","message":{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"ok"}]}}` + "\n",
			want: StatusCompleted,
		},
		{
			name: "an unanswered line from long ago is not work in progress",
			tail: `{"type":"user","timestamp":"2026-11-24T02:10:00Z","message":{"role":"user","content":"one more thing"}}` + "\n",
			want: StatusCompleted,
		},
		{
			name: "a meta line is not a command",
			tail: `{"type":"user","isMeta":true,"timestamp":"2026-11-24T03:07:00Z","message":{"role":"user","content":"<local-command-stdout>ok</local-command-stdout>"}}` + "\n",
			want: StatusCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.now = now
			e.runner.agentsJSON = `[{"id":"abab1212","sessionId":"` + id + `","name":"bg job","kind":"background","state":"done"}]`
			writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", id+".jsonl"), finished+tt.tail)
			var got Session
			for _, s := range e.col.Snapshot(context.Background()).Sessions {
				if s.SessionID == id {
					got = s
				}
			}
			if got.Status != tt.want {
				t.Fatalf("status = %q, want %q", got.Status, tt.want)
			}
		})
	}
}
