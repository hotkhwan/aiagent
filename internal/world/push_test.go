package world

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	handleA = "aph_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	handleB = "aph_BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
)

// fakeRelay records every notify and answers with status[handle] (202 by default).
type fakeRelay struct {
	mu     sync.Mutex
	sent   []map[string]string
	status map[string]int
	srv    *httptest.Server
}

func newFakeRelay(t *testing.T) *fakeRelay {
	r := &fakeRelay{status: map[string]int{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/notify" || req.Method != "POST" {
			w.WriteHeader(404)
			return
		}
		b, _ := io.ReadAll(req.Body)
		var m map[string]string
		_ = json.Unmarshal(b, &m)
		r.mu.Lock()
		r.sent = append(r.sent, m)
		code := r.status[m["handle"]]
		r.mu.Unlock()
		if code == 0 {
			code = 202
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *fakeRelay) take() []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.sent
	r.sent = nil
	return out
}

func TestPushItemsAreWhatWaitsForAPerson(t *testing.T) {
	snap := Snapshot{Sessions: []Session{
		{SessionID: "aaaa1111", DisplayName: "littledays", Prompt: &Prompt{Question: "Deploy?", Options: []PromptOption{{N: 1, Label: "Yes"}}}},
		{SessionID: "bbbb2222", DisplayName: "pm", AgentState: "blocked"},
		{SessionID: "cccc3333", DisplayName: "busy", AgentState: "working"},
		{SessionID: "dddd4444", DisplayName: "admin", AgentState: "blocked", Protected: true, MessageBlocked: "admin only"},
	}}
	reviews := []Review{
		{ID: "r1-open", Title: "Title screen", Status: "open"},
		{ID: "r2-done", Title: "Old", Status: "approved"},
	}
	got := pushItems(snap, reviews)
	var kinds []string
	for _, it := range got {
		kinds = append(kinds, it.Kind+":"+it.Ref+":"+it.Name)
	}
	want := "answer:aaaa1111:littledays|waiting:bbbb2222:pm|review:r1-open:Title screen"
	if strings.Join(kinds, "|") != want {
		t.Fatalf("items = %v", kinds)
	}
	// a different question on the same session is a new item
	snap.Sessions[0].Prompt.Question = "Deploy again?"
	if pushItems(snap, reviews)[0].Key == got[0].Key {
		t.Fatal("a new question must have a new key")
	}
}

func TestDeviceRegistrationIsBoundToTheToken(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, err := e.store.AddUser("nok", "")
	if err != nil {
		t.Fatal(err)
	}
	if code, m, _ := e.do(t, "POST", "/aiagent/api/push/devices", user, `{"handle":"`+handleA+`"}`); code != 200 {
		t.Fatalf("user registers: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	for _, bad := range []string{`{"handle":"aph_short"}`, `{"handle":"` + handleA + `/../x"}`, `{}`, `nope`} {
		if code, _, _ := e.do(t, "POST", "/aiagent/api/push/devices", user, bad); code != 400 {
			t.Fatalf("bad handle %s accepted: %d", bad, code)
		}
		time.Sleep(writeSpacing)
	}
	devs := e.svc.pushDevices()
	if len(devs) != 1 || devs[0].Handle != handleA || devs[0].TokenHash == "" || strings.Contains(devs[0].TokenHash, user) {
		t.Fatalf("devices = %+v", devs)
	}
	fi, err := os.Stat(filepath.Join(e.store.dir, pushFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("device file %v %v", fi, err)
	}
	// another token cannot remove this device; its own token can
	_, other, _ := e.store.AddUser("other", "")
	if code, _, _ := e.do(t, "POST", "/aiagent/api/push/devices/remove", other, `{"handle":"`+handleA+`"}`); code != 200 {
		t.Fatalf("remove by another: %d", code)
	}
	if len(e.svc.pushDevices()) != 1 {
		t.Fatal("another user's token removed the device")
	}
	time.Sleep(writeSpacing)
	if code, _, _ := e.do(t, "POST", "/aiagent/api/push/devices/remove", user, `{"handle":"`+handleA+`"}`); code != 200 {
		t.Fatalf("remove by owner: %d", code)
	}
	if len(e.svc.pushDevices()) != 0 {
		t.Fatal("the owner's remove did not remove")
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/push/devices", "", `{"handle":"`+handleA+`"}`); code != 401 {
		t.Fatal("registration without a token")
	}
	_ = admin
}

func TestPushSendsEachNewItemOnceToTheDevicesThatMaySeeIt(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	relay := newFakeRelay(t)
	e.svc.PushRelay = relay.srv.URL
	items := map[string][]PushItem{}
	e.svc.pushSource = func(_ context.Context, id Identity) ([]PushItem, error) {
		return items[id.Role], nil
	}
	e.svc.addPushDevice(handleA, admin)
	e.svc.addPushDevice(handleB, user)
	ctx := context.Background()

	items[RoleAdmin] = []PushItem{{Kind: "review", Key: "r|old", Name: "old", Ref: "r-old"}}
	e.svc.PushTick(ctx)
	if got := relay.take(); len(got) != 0 {
		t.Fatalf("what was already waiting when a device is first seen is not pushed: %v", got)
	}

	items[RoleAdmin] = append(items[RoleAdmin], PushItem{Kind: "answer", Key: "a|s1|q", Name: "job", Ref: "s1"})
	items[RoleUser] = []PushItem{{Kind: "waiting", Key: "w|s2", Name: "mine", Ref: "s2"}}
	e.svc.PushTick(ctx)
	got := relay.take()
	if len(got) != 2 {
		t.Fatalf("sent %v", got)
	}
	byHandle := map[string]map[string]string{}
	for _, m := range got {
		byHandle[m["handle"]] = m
	}
	if m := byHandle[handleA]; m["kind"] != "answer" || m["name"] != "job" || m["ref"] != "s1" {
		t.Fatalf("admin got %v", m)
	}
	if m := byHandle[handleB]; m["kind"] != "waiting" || m["ref"] != "s2" {
		t.Fatalf("user got %v", m)
	}

	e.svc.PushTick(ctx)
	if got := relay.take(); len(got) != 0 {
		t.Fatalf("an item is pushed once: %v", got)
	}
	// answered, then asked again: a new push
	items[RoleAdmin] = items[RoleAdmin][:1]
	e.svc.PushTick(ctx)
	items[RoleAdmin] = append(items[RoleAdmin], PushItem{Kind: "answer", Key: "a|s1|q", Name: "job", Ref: "s1"})
	e.svc.PushTick(ctx)
	if got := relay.take(); len(got) != 1 || got[0]["handle"] != handleA {
		t.Fatalf("a question asked again is pushed again: %v", got)
	}
}

func TestPushStopsWhenTheTokenIsRevokedOrTheDeviceIsGone(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	u, user, _ := e.store.AddUser("nok", "")
	relay := newFakeRelay(t)
	e.svc.PushRelay = relay.srv.URL
	n := 0
	e.svc.pushSource = func(_ context.Context, id Identity) ([]PushItem, error) {
		return []PushItem{{Kind: "waiting", Key: "w|" + string(rune('a'+n)), Name: "x", Ref: "s1"}}, nil
	}
	e.svc.addPushDevice(handleA, admin)
	e.svc.addPushDevice(handleB, user)
	ctx := context.Background()
	e.svc.PushTick(ctx) // seen

	if err := e.store.RevokeUser(u.ID); err != nil {
		t.Fatal(err)
	}
	relay.status[handleA] = 410
	n++
	e.svc.PushTick(ctx)
	got := relay.take()
	if len(got) != 1 || got[0]["handle"] != handleA {
		t.Fatalf("a revoked token's device is never pushed: %v", got)
	}
	if devs := e.svc.pushDevices(); len(devs) != 0 {
		t.Fatalf("revoked and gone devices are dropped: %+v", devs)
	}
}

func TestPushIsOffWithoutARelay(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	called := false
	e.svc.pushSource = func(context.Context, Identity) ([]PushItem, error) { called = true; return nil, nil }
	e.svc.addPushDevice(handleA, admin)
	e.svc.PushTick(context.Background())
	if called {
		t.Fatal("no relay: nothing is read or sent")
	}
}

func TestPushRelayAddress(t *testing.T) {
	for _, ok := range []string{"https://aiagent-push.example.workers.dev", "http://127.0.0.1:9", ""} {
		if _, err := ParsePushRelay(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"http://relay.example", "https://u:p@relay.example", "ftp://x", "https://"} {
		if _, err := ParsePushRelay(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, _ := ParsePushRelay("off"); got != "" {
		t.Errorf("off = %q", got)
	}
}
