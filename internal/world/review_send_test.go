package world

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const workingJob = "cccc3333-0000-0000-0000-000000000000"

// workingJobEnv is a background job the daemon is running right now.
func workingJobEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.now = time.Now()
	e.runner.agentsJSON = `[{"id":"cccc3333","sessionId":"` + workingJob + `","name":"wobbloo","kind":"background","state":"working","cwd":"/root"}]`
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", workingJob+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	return e
}

// The review answer to a running job failed with "trust this folder" for ever:
// an earlier try had left a `claude --resume` relay sitting on the trust
// dialog, and every send reused that pane. A pane on the trust dialog never
// loaded the conversation, so it is replaced; a running job is attached to.
func TestSendReplacesARelayStuckOnTheTrustDialog(t *testing.T) {
	e := workingJobEnv(t)
	e.runner.relayUp = true
	e.runner.trustPrompt = true
	e.runner.paneStart = "new-session -d -s aiagent-cccc3333 -x 220 -y 50 -c /root claude --resume " + workingJob
	if _, err := e.col.SendToSession(context.Background(), workingJob, "ตรวจแล้ว ผ่าน"); err != nil {
		t.Fatalf("send past a relay stuck on the trust dialog: %v", err)
	}
	starts := relayStarts(e)
	if len(starts) != 1 || !strings.Contains(starts[0], "claude attach cccc3333") {
		t.Fatalf("relay starts = %q, want one claude attach cccc3333", starts)
	}
	if timesTyped(e, "ตรวจแล้ว ผ่าน") != 1 {
		t.Fatal("the answer was not typed")
	}
}

// The trust error names the folder, so the owner knows where to say Yes.
func TestTrustErrorNamesTheFolder(t *testing.T) {
	e := doneJobEnv(t)
	e.runner.trustPrompt = true
	_, err := e.col.SendToSession(context.Background(), doneJob, "ตรวจแล้ว ผ่าน")
	if err == nil || !strings.Contains(err.Error(), "trust") || !strings.Contains(err.Error(), "/root") {
		t.Fatalf("err = %v, want the trust problem and the folder /root", err)
	}
}

func reviewFor(t *testing.T, e *env, sid, runtime string) *Review {
	t.Helper()
	r, err := FileReview(e.home, "WOBBLOO build 7", "", sid, runtime, "/root/repo", []byte("<h2>ข้อ 1</h2>"), e.now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

const oneItem = `{"items":[{"title":"ข้อ 1","verdict":"revise","note":"ปุ่มเล็กไป"}],"note":"","send":true}`

// The send button types the composed answer into the review's own session
// and records on the review that, how and when it was sent.
func TestReviewSendReachesTheSessionAndIsRecorded(t *testing.T) {
	e := workingJobEnv(t)
	tok, _ := e.store.Enable("test")
	r := reviewFor(t, e, workingJob, "claudeCode")
	code, m, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, oneItem)
	if code != 200 {
		t.Fatalf("send: %d %v", code, m)
	}
	d := m["details"].(map[string]any)
	if via, _ := d["via"].(string); !strings.Contains(via, "relay") {
		t.Fatalf("via = %v", d["via"])
	}
	if timesTyped(e, d["reply"].(string)) != 1 {
		t.Fatal("the composed answer was not typed into the session")
	}
	got := e.col.Reviews()[0]
	if got.Status != "revise" || got.SentAt == nil || !strings.Contains(got.SentVia, "relay") || got.Delivery == "" {
		t.Fatalf("review not recorded as sent: %+v", got)
	}
}

// A session that is no longer running is resumed in its folder and given the
// answer, the way the web resumes a stopped session.
func TestReviewSendResumesAStoppedSession(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	tok, _ := e.store.Enable("test")
	const sid = "dddd4444-0000-0000-0000-000000000000"
	writeFile(t, filepath.Join(e.home, ".claude", "projects", "-root", sid+".jsonl"),
		`{"type":"user","cwd":"/root","timestamp":"2026-01-01T03:00:00Z","message":{"content":"go"}}`+"\n")
	r := reviewFor(t, e, sid, "claudeCode")
	code, m, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, oneItem)
	if code != 200 {
		t.Fatalf("send to a stopped session: %d %v", code, m)
	}
	starts := relayStarts(e)
	if len(starts) != 1 || !strings.Contains(starts[0], "-c /root claude --resume "+sid) {
		t.Fatalf("relay starts = %q, want claude --resume %s in /root", starts, sid)
	}
	if got := e.col.Reviews()[0]; got.SentAt == nil || !strings.Contains(got.SentVia, "resume") {
		t.Fatalf("review not recorded as sent by resume: %+v", got)
	}
}

// A busy agent gets the answer from the outbox when it is free; the review is
// answered and says so.
func TestReviewSendToABusySessionIsQueued(t *testing.T) {
	e := workingJobEnv(t)
	tok, _ := e.store.Enable("test")
	e.runner.relayBusy = true
	r := reviewFor(t, e, workingJob, "claudeCode")
	code, m, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, oneItem)
	if code != 200 {
		t.Fatalf("send to a busy session: %d %v", code, m)
	}
	if q, _ := m["details"].(map[string]any)["queued"].(bool); !q {
		t.Fatalf("not queued: %v", m)
	}
	if len(e.svc.PendingFor(workingJob)) != 1 {
		t.Fatal("the answer is not in the outbox")
	}
	if got := e.col.Reviews()[0]; got.SentVia != "queued" || got.Status != "revise" {
		t.Fatalf("review = %+v", got)
	}
}

// Nothing to send to: the review stays open, so it can still be copied, and
// the error says what to do.
func TestReviewSendWithNoReachableSessionStaysOpen(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	tok, _ := e.store.Enable("test")
	r := reviewFor(t, e, "eeee5555-0000-0000-0000-000000000000", "codexCli")
	code, m, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, oneItem)
	if code == 200 || !strings.Contains(m["message"].(string), "copy") {
		t.Fatalf("send with nothing to reach: %d %v", code, m)
	}
	if got := e.col.Reviews()[0]; got.Status != "open" || got.SentAt != nil {
		t.Fatalf("a failed send answered the review: %+v", got)
	}
}
