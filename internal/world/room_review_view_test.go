package world

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// roomInstance runs a real aiagent service as a room's instance on its socket,
// the way aiagent-user@<room> does, and returns it.
func roomInstance(t *testing.T, o *OSUsers, name string) *env {
	t.Helper()
	inst := newEnv(t)
	tok, err := inst.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.TokenDir, name+".token"), []byte(tok+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(o.SocketDir, name+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = inst.app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = inst.app.Shutdown() })
	return inst
}

// A page a room's agent filed — through the spool, as a sandboxed agent
// does, or straight with FileReview — opens for the admin and for the room's
// user with its sections ready to mark, like one of the admin's own.
func TestRoomReviewOpensForAdminAndUser(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, user, err := e.store.AddUser("room-a", "room-a")
	if err != nil {
		t.Fatal(err)
	}
	inst := roomInstance(t, o, "room-a")
	page := []byte("<!doctype html><html><body><main><h1>x</h1><h2>one</h2><p>a</p><h2>two</h2></main></body>\n</html>\n")
	spooled, err := fileSpooledReview(inst.home, []byte(`{"title":"spooled page","summary":"s","sessionId":"95126f47-94c8-456d-9334-ab3a9746951d","runtime":"claudeCode","cwd":"`+filepath.Join(inst.home, "work")+`","page":"`+base64.StdEncoding.EncodeToString(page)+`"}`), inst.now)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := FileReview(inst.home, "direct page", "", "95126f47-94c8-456d-9334-ab3a9746951d", "claudeCode", inst.home, nil, page, inst.now.Add(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, who := range []struct{ name, tok string }{{"admin", admin}, {"user", user}} {
		code, _, body := e.do(t, "GET", apiPrefix+"/reviews", who.tok, "")
		if code != 200 || !strings.Contains(body, spooled.ID) || !strings.Contains(body, direct.ID) {
			t.Fatalf("%s: list = %d %s", who.name, code, body)
		}
		for _, id := range []string{spooled.ID, direct.ID} {
			code, m, body := e.do(t, "POST", apiPrefix+"/reviews/"+id+"/view", who.tok, "")
			e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
			if code != 200 {
				t.Fatalf("%s: ticket for %s = %d %s", who.name, id, code, body)
			}
			url, _ := m["details"].(map[string]any)["url"].(string)
			if !strings.HasPrefix(url, "/aiagent/review-view/") {
				t.Fatalf("%s: ticket url %q", who.name, url)
			}
			// the frame loads without the token, as an iframe does
			code, _, body = e.do(t, "GET", url, "", "")
			if code != 200 || !strings.Contains(body, "<h2>one</h2>") || !strings.Contains(body, "aiagentReview") {
				t.Fatalf("%s: view of %s = %d %.200s", who.name, id, code, body)
			}
			// one ticket, one load
			if code, _, _ = e.do(t, "GET", url, "", ""); code == 200 {
				t.Fatalf("%s: ticket of %s served twice", who.name, id)
			}
			// the marks are answered by the room the page lives in
			items, _ := json.Marshal([]ReviewItem{{Title: "one", Verdict: "approve"}, {Title: "two", Verdict: "approve"}})
			code, _, body = e.do(t, "POST", apiPrefix+"/reviews/"+id+"/compose", who.tok, `{"items":`+string(items)+`}`)
			e.svc.lastWrite = e.svc.lastWrite.Add(-writeSpacing)
			if code != 200 || !strings.Contains(body, "1. one") {
				t.Fatalf("%s: compose for %s = %d %s", who.name, id, code, body)
			}
		}
	}
	// a ticket the room never gave opens nothing
	if code, _, _ := e.do(t, "GET", "/aiagent/review-view/"+strings.Repeat("ab", 24), "", ""); code == 200 {
		t.Fatal("made-up ticket served a page")
	}
}

// Only a room's answer to a view request registers a ticket.
func TestRoomReviewTicketOnlyFromViewAnswer(t *testing.T) {
	ok := `{"code":"SUCCESS","details":{"url":"/aiagent/review-view/` + strings.Repeat("ab", 24) + `"}}`
	if got := roomReviewTicket("POST", "/reviews/r1/view", 200, []byte(ok)); got != strings.Repeat("ab", 24) {
		t.Fatalf("view answer: %q", got)
	}
	for _, c := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", "/reviews/r1/view", 200, ok},
		{"POST", "/reviews/r1/decision", 200, ok},
		{"POST", "/sessions/x/view", 200, ok},
		{"POST", "/reviews/r1/view", 500, ok},
		{"POST", "/reviews/r1/view", 200, `{"details":{"url":"/aiagent/review-view/../x"}}`},
		{"POST", "/reviews/r1/view", 200, `{"details":{"url":"https://elsewhere/aiagent/review-view/` + strings.Repeat("ab", 24) + `"}}`},
		{"POST", "/reviews/r1/view", 200, `not json`},
	} {
		if got := roomReviewTicket(c.method, c.path, c.status, []byte(c.body)); got != "" {
			t.Errorf("%s %s %d %s: %q", c.method, c.path, c.status, c.body, got)
		}
	}
}
