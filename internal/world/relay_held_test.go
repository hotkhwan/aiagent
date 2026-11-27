package world

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A session a terminal or VS Code Claude Code process holds must not get a
// relay: the relay's `claude attach` would be a second client on a session
// the operator has open, and the PC then refuses to open it ("this session is
// running in another terminal"). The registry names the holder by pid and
// kind; only a live interactive entry counts.
func TestRelayDoesNotStartWhileAnotherClaudeProcessHoldsTheSession(t *testing.T) {
	cases := []struct {
		name     string
		registry string // written as <home>/.claude/sessions/7777.json; "" for none
		procLive bool   // whether /proc/7777 carries the recorded start time
		wantErr  bool
	}{
		{
			name:     "live terminal process holds the session",
			registry: `{"pid":7777,"sessionId":"` + doneJob + `","cwd":"/root","procStart":"555","kind":"interactive","status":"idle"}`,
			procLive: true,
			wantErr:  true,
		},
		{
			name:     "registry entry whose process is gone is stale",
			registry: `{"pid":7777,"sessionId":"` + doneJob + `","cwd":"/root","procStart":"555","kind":"interactive","status":"idle"}`,
			procLive: false,
		},
		{
			name:     "recycled pid (other start time) is not the holder",
			registry: `{"pid":7777,"sessionId":"` + doneJob + `","cwd":"/root","procStart":"111","kind":"interactive","status":"idle"}`,
			procLive: true,
		},
		{
			name:     "a live entry for another session is not the holder",
			registry: `{"pid":7777,"sessionId":"cccc3333-0000-0000-0000-000000000000","cwd":"/root","procStart":"555","kind":"interactive","status":"idle"}`,
			procLive: true,
		},
		{
			name:     "no other process at all keeps today's behaviour",
			registry: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := doneJobEnv(t)
			if tc.registry != "" {
				writeFile(t, filepath.Join(e.home, ".claude", "sessions", "7777.json"), tc.registry)
			}
			if tc.procLive {
				fakeProc(t, e.proc, "7777", "555")
			}
			ss := &Session{SessionID: doneJob, Kind: "bg", RuntimeType: "claudeCode", home: e.home}
			err := e.col.relaySend(context.Background(), ss, "ตรวจแล้ว ผ่าน")
			starts := relayStarts(e)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("send: %v", err)
				}
				if len(starts) != 1 {
					t.Fatalf("relay starts = %q, want one attach", starts)
				}
				return
			}
			var api *APIError
			if !errors.As(err, &api) || api.Status != 409 {
				t.Fatalf("err = %v, want a 409 APIError saying the session is open elsewhere", err)
			}
			if !strings.Contains(api.Msg, "เทอร์มินัล") {
				t.Errorf("message %q does not say the session is open in a terminal", api.Msg)
			}
			if len(starts) != 0 {
				t.Fatalf("relay started while another process holds the session: %q", starts)
			}
			if timesTyped(e, "ตรวจแล้ว ผ่าน") != 0 {
				t.Fatalf("message was typed although the session is held elsewhere")
			}
		})
	}
}
