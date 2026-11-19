package worker

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A worker that ignores SIGTERM and has a child still holding its pipe open is
// the case that needs SIGKILL on the whole group. KillAfter is shortened so the
// test does not wait the production 15 seconds.
func TestCancelEndsTheWholeGroupEvenIfItIgnoresTERM(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	out, err := Run(ctx, Process{
		Argv:      []string{"/bin/sh", "-c", "trap '' TERM; sleep 30 & wait"},
		KillAfter: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Canceled {
		t.Fatal("a cancelled run must report Canceled")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("cancel took %v; SIGKILL on the group did not happen", took)
	}
}

func TestCancelSendsTERMFirst(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	if _, err := Run(ctx, Process{Argv: []string{"/bin/sleep", "30"}, KillAfter: 15 * time.Second}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("a worker that honours TERM should exit at once, took %v", took)
	}
}

func TestCrashIsReportedNotHidden(t *testing.T) {
	out, err := Run(context.Background(), Process{Argv: []string{"/bin/sh", "-c", "kill -9 $$"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitErr == nil || out.Canceled {
		t.Fatalf("a process killed by a signal must surface as ExitErr, got %+v", out)
	}
}

func TestStdinIsThePromptAndStderrIsKept(t *testing.T) {
	var got []Event
	out, err := Run(context.Background(), Process{
		Argv:    []string{"/bin/sh", "-c", "cat; echo oops >&2"},
		Stdin:   "ทำ task นี้",
		Env:     []string{"PATH=/usr/bin:/bin"},
		Dir:     t.TempDir(),
		OnEvent: func(e Event) { got = append(got, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Stderr, "oops") {
		t.Fatalf("stderr = %q", out.Stderr)
	}
	if len(got) != 1 || got[0].Kind != "unknownEvent" {
		t.Fatalf("stdout lines should reach OnEvent, got %+v", got)
	}
}
