package worker

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// defaultKillAfter is how long a cancelled worker has to exit after SIGTERM.
const defaultKillAfter = 15 * time.Second

// stderrCap keeps the tail of a worker's stderr for the reason it failed.
const stderrCap = 16 << 10

// Process is one worker run. Argv is run as given, never through a shell.
type Process struct {
	Argv      []string
	Dir       string
	Env       []string
	Stdin     string
	KillAfter time.Duration
	OnEvent   func(Event)
}

// Outcome is how a run ended. Canceled means the context ended it; ExitErr is
// a non-zero exit or a signal.
type Outcome struct {
	Canceled bool
	ExitErr  error
	Stderr   string
}

// Run starts p in its own process group. When ctx ends it sends SIGTERM to the
// group and, if the group is still there after KillAfter, SIGKILL. Stdout is
// parsed as stream-json and each event goes to OnEvent.
func Run(ctx context.Context, p Process) (Outcome, error) {
	if len(p.Argv) == 0 {
		return Outcome{}, errors.New("worker: empty argv")
	}
	cmd := exec.Command(p.Argv[0], p.Argv[1:]...)
	cmd.Dir = p.Dir
	cmd.Env = p.Env
	cmd.Stdin = strings.NewReader(p.Stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr tailBuffer
	stderr.limit = stderrCap
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Outcome{}, err
	}
	if err := cmd.Start(); err != nil {
		return Outcome{}, err
	}

	done := make(chan struct{})
	var canceled atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			canceled.Store(true)
			signalGroup(cmd, syscall.SIGTERM)
			killAfter := p.KillAfter
			if killAfter <= 0 {
				killAfter = defaultKillAfter
			}
			t := time.NewTimer(killAfter)
			defer t.Stop()
			select {
			case <-done:
			case <-t.C:
				signalGroup(cmd, syscall.SIGKILL)
			}
		case <-done:
		}
	}()

	streamErr := ParseStream(stdout, func(e Event) {
		if p.OnEvent != nil {
			p.OnEvent(e)
		}
	})
	if streamErr != nil {
		// The stream is unreadable; stop the worker rather than wait on it.
		signalGroup(cmd, syscall.SIGKILL)
	}
	waitErr := cmd.Wait()
	close(done)
	if streamErr != nil {
		return Outcome{}, streamErr
	}
	out := Outcome{Canceled: canceled.Load(), Stderr: stderr.String()}
	if !out.Canceled {
		out.ExitErr = waitErr
	}
	return out, nil
}

func signalGroup(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf.Write(p)
	if over := t.buf.Len() - t.limit; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return t.buf.String() }
