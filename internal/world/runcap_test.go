package world

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestProcessListIsNotCut: ExecRunner cut every output at 64 KB, and a busy
// Mac's `ps` is ~150 KB, so a live job's process (pid 88519, line 764 of 771)
// was missing and its session showed as ended. The process list and the
// agent view are kept whole; other commands keep the small cap.
func TestProcessListIsNotCut(t *testing.T) {
	if got := runOutputCap("ps", []string{"-axww"}); got < 8<<20 {
		t.Fatalf("ps cap = %d", got)
	}
	if got := runOutputCap("/usr/local/bin/claude", []string{"agents", "--json", "--all"}); got < 8<<20 {
		t.Fatalf("claude agents cap = %d", got)
	}
	if got := runOutputCap("tmux", []string{"capture-pane"}); got != 64<<10 {
		t.Fatalf("tmux cap = %d", got)
	}
	// and a long table still parses to its last row
	var b strings.Builder
	for pid := 1; pid <= 900; pid++ {
		fmt.Fprintf(&b, "%d 1 Sat Nov 28 08:35:01 2026     claude bg-spare --bg-spare /tmp/cc-daemon-501/a25c0c89/spare/%040d.claim.sock\n", pid, pid)
	}
	if b.Len() < 64<<10 {
		t.Fatal("fixture too small to prove anything")
	}
	pt := parsePS([]byte(b.String()), time.UTC)
	if _, ok := pt.startTime(900); !ok {
		t.Fatal("the last process was lost")
	}
}
