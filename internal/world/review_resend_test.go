package world

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// roomWithJob is the admin's service and a real room instance on its socket
// whose one session is a running background job, reached through a relay.
func roomWithJob(t *testing.T) (e, inst *env, admin, user string) {
	t.Helper()
	e, _, o := osUserEnv(t)
	admin, _ = e.store.Enable("t")
	_, user, _ = e.store.AddUser("room-a", "room-a")
	inst = roomInstance(t, o, "room-a")
	inst.now = time.Now()
	inst.runner.agentsJSON = `[{"id":"cccc3333","sessionId":"` + workingJob + `","name":"wobbloo","kind":"background","state":"working","cwd":"/root"}]`
	writeFile(t, filepath.Join(inst.home, ".claude", "projects", "-root", workingJob+".jsonl"),
		`{"type":"user","timestamp":"2026-11-04T03:00:00Z","message":{"content":"go"}}`+"\n")
	return e, inst, admin, user
}

func setDialog(inst *env, screen string) {
	inst.runner.mu.Lock()
	inst.runner.dialogScreen = screen
	inst.runner.mu.Unlock()
}

// both services allow the next write at once
func nextWrites(es ...*env) {
	for _, x := range es {
		nextWrite(x)
	}
}

const askDialog = `● Which layout?
────────────────────────────────
 ☐ Layout  ☐ Filters  ✔ Submit  →
Which layout should the list use?
❯ 1. Cards
  2. Table
  3. Type something.
────────────────────────────────
  4. Chat about this
Enter to select · Tab/Arrow keys to navigate · Esc to cancel`

// The owner's case: a send refused because the agent was showing a question
// reached nobody, so once the question is answered the same words go through
// — with the draft's own sendId or a new one — and neither the admin's
// service nor the room's instance holds the refused try against them.
func TestASendRefusedForADialogIsNoDuplicate(t *testing.T) {
	for _, sameID := range []bool{true, false} {
		e, inst, _, user := roomWithJob(t)
		p := apiPrefix + "/sessions/" + workingJob + "/message"
		setDialog(inst, askDialog)
		code, m, _ := e.do(t, "POST", p, user, `{"message":"ตรวจแล้ว ผ่าน","sendId":"draft-dlg1-aaaa"}`)
		if code != 409 || m["code"] != "AIAGENT_RELAY_DIALOG" {
			t.Fatalf("on a dialog = %d %v", code, m)
		}
		if timesTyped(inst, "ตรวจแล้ว ผ่าน") != 0 {
			t.Fatal("typed into the dialog")
		}
		setDialog(inst, "")
		nextWrites(e, inst)
		id := "draft-dlg1-bbbb"
		if sameID {
			id = "draft-dlg1-aaaa"
		}
		code, m, _ = e.do(t, "POST", p, user, `{"message":"ตรวจแล้ว ผ่าน","sendId":"`+id+`"}`)
		if code != 200 || details(m)["replayed"] != nil {
			t.Fatalf("sameID=%v: after the dialog = %d %v, want it typed now", sameID, code, m)
		}
		if n := timesTyped(inst, "ตรวจแล้ว ผ่าน"); n != 1 {
			t.Fatalf("sameID=%v: typed %d times, want 1", sameID, n)
		}
		// a real second press is still refused, and "send anyway" passes
		// both layers: the room's instance does not refuse what the admin's
		// service let through
		nextWrites(e, inst)
		code, m, _ = e.do(t, "POST", p, user, `{"message":"ตรวจแล้ว ผ่าน","sendId":"draft-dlg1-cccc"}`)
		if code != 409 || m["code"] != dupSendCode {
			t.Fatalf("sameID=%v: second press = %d %v, want %s", sameID, code, m, dupSendCode)
		}
		nextWrites(e, inst)
		code, m, _ = e.do(t, "POST", p, user, `{"message":"ตรวจแล้ว ผ่าน","sendId":"draft-dlg1-cccc","resend":true}`)
		if code != 200 || timesTyped(inst, "ตรวจแล้ว ผ่าน") != 2 {
			t.Fatalf("sameID=%v: resend = %d %v, typed %d", sameID, code, m, timesTyped(inst, "ตรวจแล้ว ผ่าน"))
		}
	}
}

// The review dialog's "send to agent" for a page filed in a room: refused for
// a question, the review stays open and the next press types it, once.
func TestARoomReviewRefusedForADialogIsSentOnTheNextPress(t *testing.T) {
	e, inst, admin, _ := roomWithJob(t)
	r := reviewFor(t, inst, workingJob, "claudeCode")
	path := apiPrefix + "/reviews/" + r.ID + "/decision"
	setDialog(inst, askDialog)
	if code, m, _ := e.do(t, "POST", path, admin, oneItem); code != 409 || m["code"] != "AIAGENT_RELAY_DIALOG" {
		t.Fatalf("on a dialog = %d %v", code, m)
	}
	if got := inst.col.Reviews()[0]; got.Status != "open" || got.SentAt != nil {
		t.Fatalf("a refused send answered the review: %+v", got)
	}
	setDialog(inst, "")
	nextWrites(e, inst)
	code, m, _ := e.do(t, "POST", path, admin, oneItem)
	if code != 200 {
		t.Fatalf("after the dialog = %d %v", code, m)
	}
	if n := timesTyped(inst, details(m)["reply"].(string)); n != 1 {
		t.Fatalf("typed %d times, want 1", n)
	}
}

// Two presses at once (two tabs, the admin and the room's user): the reply is
// typed once and the other press is told it was answered.
func TestTwoReviewSendsAtOnceTypeOnce(t *testing.T) {
	e := workingJobEnv(t)
	r := reviewFor(t, e, workingJob, "claudeCode")
	e.runner.afterType = func() { time.Sleep(150 * time.Millisecond) }
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var body struct {
				Items []ReviewItem `json:"items"`
				Send  bool         `json:"send"`
			}
			_ = json.Unmarshal([]byte(oneItem), &body)
			_, _, err := e.col.DecideReview(context.Background(), r.ID, "", "", body.Items, e.svc.deliverReview)
			if err == nil {
				codes[i] = 200
			} else {
				codes[i] = 409
			}
		}(i)
	}
	wg.Wait()
	if codes[0]+codes[1] != 609 {
		t.Fatalf("results = %v, want one sent and one refused", codes)
	}
	reply := e.col.Reviews()[0].Reply
	if reply == "" || timesTyped(e, reply) != 1 {
		t.Fatalf("typed %d times, want 1", timesTyped(e, reply))
	}
}

// A reply held for a busy agent that the agent later refuses (a question on
// its screen) reached nobody: the review opens again and can be sent.
func TestAQueuedReviewReplyRefusedLaterReopensTheReview(t *testing.T) {
	e := workingJobEnv(t)
	tok, _ := e.store.Enable("test")
	r := reviewFor(t, e, workingJob, "claudeCode")
	e.runner.relayBusy = true
	path := "/aiagent/api/reviews/" + r.ID + "/decision"
	if code, m, _ := e.do(t, "POST", path, tok, oneItem); code != 200 || details(m)["queued"] != true {
		t.Fatalf("send to a busy agent = %d %v", code, m)
	}
	if got := e.col.Reviews()[0]; got.Status != "revise" {
		t.Fatalf("queued review = %+v", got)
	}
	e.runner.mu.Lock()
	e.runner.relayBusy = false
	e.runner.dialogScreen = askDialog
	e.runner.mu.Unlock()
	e.svc.FlushOutbox(context.Background(), time.Now())
	if got := e.col.Reviews()[0]; got.Status != "open" || got.SentAt != nil || got.SentVia != "" {
		t.Fatalf("a reply the agent never got still answers the review: %+v", got)
	}
	setDialog(e, "")
	nextWrite(e)
	code, m, _ := e.do(t, "POST", path, tok, oneItem)
	if code != 200 || details(m)["queued"] == true {
		t.Fatalf("send again = %d %v", code, m)
	}
	if n := timesTyped(e, details(m)["reply"].(string)); n != 1 {
		t.Fatalf("typed %d times, want 1", n)
	}
}

// A reply typed but never seen in the transcript may not have arrived. A
// second press is refused with a code the page turns into "send again", and
// only that request sends it again; a confirmed one is never sent twice.
func TestAnUnconfirmedReviewReplyIsSentAgainOnlyOnPurpose(t *testing.T) {
	e := workingJobEnv(t)
	tok, _ := e.store.Enable("test")
	r := reviewFor(t, e, workingJob, "claudeCode")
	path := "/aiagent/api/reviews/" + r.ID + "/decision"
	code, m, _ := e.do(t, "POST", path, tok, oneItem)
	if code != 200 || details(m)["delivery"] != DeliveryUnknown {
		t.Fatalf("first = %d %v, want sent and not confirmed", code, m)
	}
	reply := details(m)["reply"].(string)
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", path, tok, oneItem); code != 409 || m["code"] != reviewUnconfirmedCode {
		t.Fatalf("second press = %d %v, want %s", code, m, reviewUnconfirmedCode)
	}
	if timesTyped(e, reply) != 1 {
		t.Fatal("a refused press typed the reply")
	}
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", path, tok, `{"send":true,"resend":true}`); code != 200 || details(m)["reply"] != reply {
		t.Fatalf("resend = %d %v", code, m)
	}
	if n := timesTyped(e, reply); n != 2 {
		t.Fatalf("typed %d times after resend, want 2", n)
	}
	// confirmed: never again
	got := e.col.Reviews()[0]
	got.Delivery = DeliveryAcked
	if err := writeReviewMeta(got.home, &got); err != nil {
		t.Fatal(err)
	}
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", path, tok, `{"send":true,"resend":true}`); code != 409 || m["code"] == reviewUnconfirmedCode || !strings.Contains(m["message"].(string), "ตอบไปแล้ว") {
		t.Fatalf("resend of a confirmed reply = %d %v", code, m)
	}
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", path, tok, oneItem); code != 409 || m["code"] == reviewUnconfirmedCode {
		t.Fatalf("press on a confirmed reply = %d %v", code, m)
	}
}
