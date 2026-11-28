package world

import (
	"context"
	"testing"
)

// TestAStoppedJobIsNotOnline: `claude stop` ends a background job's process,
// but the agent view keeps listing it as "done" (it can be resumed). With no
// live process behind it, it is not online: it leaves the village and the
// online list. Seen on ata-core: "netra cut sop" stayed in the lounge after
// "หยุด agent".
func TestAStoppedJobIsNotOnline(t *testing.T) {
	c := newEnv(t).col
	c.daemonCache = []daemonEntry{
		{ID: "5a75a2cb", SessionID: "5a75a2cb-d8c8-4bf5-844e-cd8ee30c8f08", Name: "netra cut sop", Kind: "background", State: "done"},
		{ID: "95126f47", SessionID: "95126f47-94c8-456d-9334-ab3a9746951d", Name: "khwan", Kind: "background", State: "working"},
		{ID: "aaaa1111", SessionID: "aaaa1111-0000-0000-0000-000000000000", Name: "idle job", Kind: "background", State: "done"},
	}
	c.daemonAt = c.now()
	live := []Session{
		// a done job whose process is still there (finished its turn, waits)
		{SessionID: "aaaa1111-0000-0000-0000-000000000000", RuntimeType: "claudeCode", Live: true, LiveEvidence: "pid+procStart"},
	}
	got := map[string]Session{}
	for _, s := range c.mergeDaemon(context.Background(), live) {
		got[s.DisplayName] = s
	}
	if s := got["netra cut sop"]; s.Live || s.Status != StatusCompleted {
		t.Fatalf("stopped job: live=%v status=%v", s.Live, s.Status)
	}
	if s := got["khwan"]; !s.Live {
		t.Fatal("a working job must be online")
	}
	if s := got["idle job"]; !s.Live {
		t.Fatal("a done job with its process still running stays online")
	}
}
