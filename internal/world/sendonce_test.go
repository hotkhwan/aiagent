package world

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func sendPath() string { return apiPrefix + "/sessions/" + outboxSession + "/message" }

// nextWrite lets the test send again without waiting out the one-write-a-
// second limit, which is a different rule.
func nextWrite(e *env) {
	e.svc.mu.Lock()
	e.svc.lastWrite = time.Time{}
	e.svc.mu.Unlock()
}

func auditText(t *testing.T, e *env) string {
	t.Helper()
	b, err := os.ReadFile(e.auditFP)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func details(m map[string]any) map[string]any {
	d, _ := m["details"].(map[string]any)
	return d
}

// The page sends the same sendId again when the same draft is sent again. The
// agent must get it once, and the second answer must be the first one.
func TestARepeatedSendIDIsTypedOnce(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	body := `{"message":"แก้ปุ่มส่งให้กดได้ครั้งเดียว","sendId":"draft-0001-aaaa"}`
	code, m, _ := e.do(t, "POST", sendPath(), tok, body)
	if code != 200 || details(m)["replayed"] != nil {
		t.Fatalf("first = %d %v", code, m)
	}
	nextWrite(e)
	code, m, _ = e.do(t, "POST", sendPath(), tok, body)
	if code != 200 || details(m)["replayed"] != true {
		t.Fatalf("repeat = %d %v, want the first answer marked replayed", code, m)
	}
	if n := timesTyped(e, "แก้ปุ่มส่งให้กดได้ครั้งเดียว"); n != 1 {
		t.Fatalf("typed %d times, want 1", n)
	}
	a := auditText(t, e)
	if !strings.Contains(a, `"rpc":"aiagent.duplicateSend"`) || !strings.Contains(a, `"result":"replayed"`) {
		t.Errorf("the replay is not in the audit log:\n%s", a)
	}
	if strings.Contains(a, "แก้ปุ่มส่ง") || strings.Contains(a, "draft-0001") {
		t.Errorf("the audit log holds the text or the sendId:\n%s", a)
	}
}

// A double click sends twice before the first has answered. The second waits
// for the first and gets its answer; nothing is typed twice.
func TestASecondSendWhileTheFirstRunsWaitsForIt(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	release := make(chan struct{})
	e.runner.afterType = func() {
		select {
		case <-release:
		case <-time.After(400 * time.Millisecond):
		}
	}
	body := `{"message":"กดสองที","sendId":"draft-0002-bbbb"}`
	var wg sync.WaitGroup
	codes := make([]int, 2)
	marks := make([]any, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			code, m, _ := e.do(t, "POST", sendPath(), tok, body)
			codes[i], marks[i] = code, details(m)["replayed"]
		}(i)
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()
	close(release)
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("codes = %v", codes)
	}
	if marks[0] != nil || marks[1] != true {
		t.Fatalf("replayed = %v, want [nil true]", marks)
	}
	if n := timesTyped(e, "กดสองที"); n != 1 {
		t.Fatalf("typed %d times, want 1", n)
	}
}

// The same words to the same agent a few seconds later, from a page that made
// a new id (another tab, an old page), are refused until the user says so.
func TestTheSameTextAgainSoonIsRefusedUnlessResent(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	if code, m, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ข้อความยาว\nหลายบรรทัด","sendId":"draft-0003-aaaa"}`); code != 200 {
		t.Fatalf("first = %d %v", code, m)
	}
	nextWrite(e)
	code, m, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ข้อความยาว\nหลายบรรทัด","sendId":"draft-0003-bbbb"}`)
	if code != 409 || m["code"] != dupSendCode || m["message"] != dupSendMessage {
		t.Fatalf("again = %d %v, want 409 %s", code, m, dupSendCode)
	}
	nextWrite(e)
	if code, _, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ข้อความยาว\nหลายบรรทัด"}`); code != 409 {
		t.Fatalf("again without an id = %d, want 409", code)
	}
	if n := timesTyped(e, "ข้อความยาว\nหลายบรรทัด"); n != 1 {
		t.Fatalf("typed %d times before resend, want 1", n)
	}
	if a := auditText(t, e); !strings.Contains(a, `"result":"refused"`) {
		t.Errorf("the refusal is not in the audit log:\n%s", a)
	}
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ข้อความยาว\nหลายบรรทัด","sendId":"draft-0003-bbbb","resend":true}`); code != 200 {
		t.Fatalf("resend = %d %v", code, m)
	}
	if n := timesTyped(e, "ข้อความยาว\nหลายบรรทัด"); n != 2 {
		t.Fatalf("typed %d times after resend, want 2", n)
	}
	// other words go through at once
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", sendPath(), tok, `{"message":"อีกเรื่อง","sendId":"draft-0003-cccc"}`); code != 200 {
		t.Fatalf("other text = %d %v", code, m)
	}
}

// The window is short: the same words later are a new message.
func TestTheSameTextAfterTheWindowGoesThrough(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	if code, _, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ok","sendId":"draft-0004-aaaa"}`); code != 200 {
		t.Fatal(code)
	}
	e.svc.sends.mu.Lock()
	for _, x := range e.svc.sends.texts {
		x.at = x.at.Add(-dupTextWindow - time.Second)
	}
	e.svc.sends.mu.Unlock()
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", sendPath(), tok, `{"message":"ok","sendId":"draft-0004-bbbb"}`); code != 200 {
		t.Fatalf("after the window = %d %v", code, m)
	}
}

// A send that failed is not remembered: trying the same draft again tries
// again, and is not refused as a duplicate of itself.
func TestAFailedSendCanBeTriedAgain(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	body := `{"workspace":"nope","message":"ลองใหม่","sendId":"draft-0005-aaaa"}`
	code, m, _ := e.do(t, "POST", apiPrefix+"/messages", tok, body)
	if code < 400 || m["code"] == dupSendCode {
		t.Fatalf("first = %d %v, want the handler's own failure", code, m)
	}
	nextWrite(e)
	code2, m2, _ := e.do(t, "POST", apiPrefix+"/messages", tok, body)
	if code2 != code || m2["code"] != m["code"] {
		t.Fatalf("retry = %d %v, want the handler run again (%d %v)", code2, m2, code, m)
	}
	if n := strings.Count(auditText(t, e), `"rpc":"aiagent.message"`); n != 4 {
		t.Fatalf("aiagent.message audit lines = %d, want 4 (intent+result twice)", n)
	}
	// nor does a failed send hold its words against a fresh draft of them
	nextWrite(e)
	code3, m3, _ := e.do(t, "POST", apiPrefix+"/messages", tok, `{"workspace":"nope","message":"ลองใหม่","sendId":"draft-0005-bbbb"}`)
	if code3 != code || m3["code"] != m["code"] {
		t.Fatalf("a new draft after a failure = %d %v, want the handler run again", code3, m3)
	}
}

// A number picked on the agent's question may be the same for two questions
// in a row; only the sendId guards it.
func TestOptionAnswersAreNotRefusedForTheSameNumber(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	var b sendBody
	if err := json.Unmarshal([]byte(`{"option":1,"sendId":"draft-0006-aaaa"}`), &b); err != nil || b.text() != "" || b.SendID == "" {
		t.Fatalf("an option answer reads as text %q (%v)", b.text(), err)
	}
	p := apiPrefix + "/sessions/" + outboxSession + "/answer"
	e.do(t, "POST", p, tok, `{"option":1,"sendId":"draft-0006-aaaa"}`)
	nextWrite(e)
	if code, m, _ := e.do(t, "POST", p, tok, `{"option":1,"sendId":"draft-0006-bbbb"}`); m["code"] == dupSendCode {
		t.Fatalf("second answer refused as duplicate: %d %v", code, m)
	}
}

func TestASendIDMustLookLikeOne(t *testing.T) {
	e := outboxEnv(t)
	tok, _ := e.store.Enable("test")
	if code, _, _ := e.do(t, "POST", sendPath(), tok, `{"message":"x","sendId":"a b"}`); code != 400 {
		t.Fatalf("bad sendId = %d, want 400", code)
	}
}

// A room's send is checked before it leaves for the room's instance, so a
// double send from the admin's page or a user's page reaches the room once.
func TestARoomGetsARepeatedSendOnce(t *testing.T) {
	e, _, o := osUserEnv(t)
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	_, a, _ := e.store.AddUser("anna", "anna")
	var mu sync.Mutex
	var posts []string
	instance(t, o, "anna", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			b := make([]byte, 512)
			n, _ := r.Body.Read(b)
			mu.Lock()
			posts = append(posts, string(b[:n]))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{"sessions":[{"sessionId":"anna-sess","live":true,"messageVia":"agcmd"}],"via":"agcmd"}}`))
	})
	body := `{"message":"สวัสดีห้อง","sendId":"draft-0007-aaaa"}`
	p := apiPrefix + "/sessions/anna-sess/message"
	if code, m, _ := e.do(t, "POST", p, a, body); code != 200 {
		t.Fatalf("first = %d %v", code, m)
	}
	code, m, _ := e.do(t, "POST", p, a, body)
	if code != 200 || details(m)["replayed"] != true {
		t.Fatalf("repeat = %d %v", code, m)
	}
	if code, m, _ := e.do(t, "POST", p, a, `{"message":"สวัสดีห้อง","sendId":"draft-0007-bbbb"}`); code != 409 || m["code"] != dupSendCode {
		t.Fatalf("same text, new id = %d %v", code, m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || !strings.Contains(posts[0], `"sendId":"draft-0007-aaaa"`) {
		t.Fatalf("the room got %d sends %q, want one carrying the sendId", len(posts), posts)
	}
}

// Memory stays bounded however many sends there are.
func TestSendMemoryIsBounded(t *testing.T) {
	var o sendOnce
	o.ids, o.texts = map[string]*sendResult{}, map[string]*sendText{}
	now := time.Now()
	for i := range sendOnceMax + 50 {
		o.prune(now)
		k := digest("k", string(rune(i)), time.Duration(i).String())
		o.ids[k] = &sendResult{done: make(chan struct{}), at: now.Add(-time.Duration(sendOnceMax-i) * time.Millisecond)}
		o.texts[k] = &sendText{at: now.Add(-time.Duration(sendOnceMax-i) * time.Millisecond)}
	}
	if len(o.ids) > sendOnceMax || len(o.texts) > sendOnceMax {
		t.Fatalf("ids %d texts %d, cap %d", len(o.ids), len(o.texts), sendOnceMax)
	}
	o.prune(now.Add(sendIDTTL + time.Minute))
	if len(o.ids) != 0 || len(o.texts) != 0 {
		t.Fatalf("expired entries kept: ids %d texts %d", len(o.ids), len(o.texts))
	}
}
