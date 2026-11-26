package world

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func spoolReq(title string) SpoolRequest {
	return SpoolRequest{Title: title, Summary: "s", SessionID: "abc-123", Runtime: "claudeCode", Cwd: "/elsewhere", Page: []byte("<h1>done</h1>")}
}

func spoolEnv(t *testing.T) (*env, string) {
	t.Helper()
	e := newEnv(t)
	if _, err := e.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{".claude", "work"} {
		if err := os.MkdirAll(filepath.Join(e.home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return e, e.home
}

func reply(t *testing.T, home, id string) *SpoolReply {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, err := SpoolWait(ctx, home, id)
	if err != nil {
		return nil
	}
	return r
}

// A page left in the work folder is filed by the instance and every reader
// sees it, next to one filed the old way.
func TestSpoolReviewIsFiledByTheInstance(t *testing.T) {
	e, home := spoolEnv(t)
	old, err := FileReview(home, "old way", "", "", "", "", nil, []byte("<p>x</p>"), e.now)
	if err != nil {
		t.Fatal(err)
	}
	id, err := SpoolReview(home, spoolReq("from the sandbox"))
	if err != nil {
		t.Fatal(err)
	}
	e.svc.collectSpool(e.now)
	r := reply(t, home, id)
	if r == nil || r.Error != "" || !reviewIDRE.MatchString(r.ReviewID) {
		t.Fatalf("reply %+v", r)
	}
	if left, _ := os.ReadDir(filepath.Join(home, "work", spoolDir, "in")); len(left) != 0 {
		t.Errorf("left in the spool: %v", left)
	}
	rs := e.col.Reviews()
	if len(rs) != 2 {
		t.Fatalf("reviews: %+v", rs)
	}
	var got *Review
	for i := range rs {
		if rs[i].ID == r.ReviewID {
			got = &rs[i]
		}
	}
	if got == nil || got.Title != "from the sandbox" || got.SessionID != "abc-123" || got.Cwd != filepath.Join(home, "work") {
		t.Fatalf("filed: %+v", got)
	}
	if page, err := e.col.ReviewPage(got.ID); err != nil || string(page) != "<h1>done</h1>" {
		t.Errorf("page %q %v", page, err)
	}
	for _, id := range []string{got.ID, old.ID} {
		if _, _, err := e.col.DecideReview(context.Background(), id, "approve", "", nil, nil); err != nil {
			t.Errorf("decide %s: %v", id, err)
		}
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), `"event":"intent","rpc":"aiagent.spoolReview"`) || !strings.Contains(string(b), `"event":"result","rpc":"aiagent.spoolReview"`) {
		t.Errorf("not audited: %s", b)
	}
	// the admin's own service collects nothing
	e.svc.OSUsers = &OSUsers{}
	id2, _ := SpoolReview(home, spoolReq("x"))
	ctx, cancel := context.WithCancel(context.Background())
	e.svc.StartSpool(ctx)
	time.Sleep(1300 * time.Millisecond)
	cancel()
	if _, err := os.Lstat(filepath.Join(home, "work", spoolDir, "in", id2+".review")); err != nil {
		t.Errorf("the admin service touched a spool")
	}
}

func TestSpoolReviewRefusesWhatIsNotAPlainPage(t *testing.T) {
	e, home := spoolEnv(t)
	bad := map[string]func(*SpoolRequest){
		"no tags":   func(r *SpoolRequest) { r.Page = []byte("just text") },
		"not utf-8": func(r *SpoolRequest) { r.Page = []byte("<p>\xff</p>") },
		"nul":       func(r *SpoolRequest) { r.Page = []byte("<p>\x00</p>") },
		"too large": func(r *SpoolRequest) { r.Page = []byte("<p>" + strings.Repeat("a", reviewMaxHTML) + "</p>") },
		"no title":  func(r *SpoolRequest) { r.Title = " " },
	}
	for what, f := range bad {
		r := spoolReq("x")
		f(&r)
		if _, err := SpoolReview(home, r); err == nil {
			t.Errorf("the CLI took %s", what)
		}
		// and the instance does not trust the CLI
		b, _ := json.Marshal(r)
		id := newSpoolID()
		in := filepath.Join(home, "work", spoolDir, "in")
		_ = os.MkdirAll(in, 0o700)
		_ = os.WriteFile(filepath.Join(in, id+".review"), b, 0o600)
		e.svc.collectSpool(e.now)
		if got := reply(t, home, id); got == nil || got.Error == "" {
			t.Errorf("%s: %+v", what, got)
		}
	}
	id := newSpoolID()
	_ = os.WriteFile(filepath.Join(home, "work", spoolDir, "in", id+".review"), []byte(`{"title":"x","page":"PGI+","extra":1}`), 0o600)
	e.svc.collectSpool(e.now)
	if got := reply(t, home, id); got == nil || got.Error == "" {
		t.Errorf("unknown field: %+v", got)
	}
	if rs := e.col.Reviews(); len(rs) != 0 {
		t.Errorf("filed: %+v", rs)
	}
}

// The instance may write ~/.claude, the sandboxed agent may not: nothing the
// agent puts in the spool may lead the instance there.
func TestSpoolNeverFollowsLinks(t *testing.T) {
	e, home := spoolEnv(t)
	claude := filepath.Join(home, ".claude")
	secret := filepath.Join(claude, "settings.json")
	_ = os.WriteFile(secret, []byte(`{"keep":1}`), 0o600)
	valid, _ := json.Marshal(spoolReq("x"))
	sp := filepath.Join(home, "work", spoolDir)
	in := filepath.Join(sp, "in")
	reset := func() {
		_ = os.RemoveAll(sp)
		for _, d := range []string{in, filepath.Join(sp, "out"), filepath.Join(sp, "captures")} {
			_ = os.MkdirAll(d, 0o700)
		}
	}
	untouched := func(what string) {
		t.Helper()
		if b, _ := os.ReadFile(secret); string(b) != `{"keep":1}` {
			t.Errorf("%s: settings.json is now %q", what, b)
		}
		ents, _ := os.ReadDir(claude)
		for _, x := range ents {
			if x.Name() != "settings.json" && x.Name() != reviewDir {
				t.Errorf("%s: wrote %s into .claude", what, x.Name())
			}
		}
		if rs := e.col.Reviews(); len(rs) != 0 {
			t.Errorf("%s: filed %+v", what, rs)
		}
	}
	id := "0123456789abcdef0123456789abcdef"
	cases := map[string]func(){
		"request is a link": func() {
			real := filepath.Join(t.TempDir(), "r")
			_ = os.WriteFile(real, valid, 0o600)
			_ = os.Symlink(real, filepath.Join(in, id+".review"))
		},
		"request is a hard link": func() {
			real := filepath.Join(sp, "r")
			_ = os.WriteFile(real, valid, 0o600)
			_ = os.Link(real, filepath.Join(in, id+".review"))
		},
		"request is a fifo": func() { _ = syscall.Mkfifo(filepath.Join(in, id+".review"), 0o600) },
		"request is writable by others": func() {
			_ = os.WriteFile(filepath.Join(in, id+".review"), valid, 0o600)
			_ = os.Chmod(filepath.Join(in, id+".review"), 0o622)
		},
		"in is a link": func() {
			_ = os.RemoveAll(in)
			d := t.TempDir()
			_ = os.WriteFile(filepath.Join(d, id+".review"), valid, 0o600)
			_ = os.Symlink(d, in)
		},
		"in is world-writable": func() {
			_ = os.WriteFile(filepath.Join(in, id+".review"), valid, 0o600)
			_ = os.Chmod(in, 0o777)
		},
		"spool is a link to .claude": func() {
			_ = os.RemoveAll(sp)
			_ = os.Symlink(claude, sp)
			_ = os.MkdirAll(filepath.Join(claude, "in"), 0o700)
			_ = os.WriteFile(filepath.Join(claude, "in", id+".review"), valid, 0o600)
		},
		"work is a link": func() {
			_ = os.RemoveAll(filepath.Join(home, "work"))
			_ = os.Symlink(claude, filepath.Join(home, "work"))
		},
	}
	for what, setup := range cases {
		_ = os.RemoveAll(filepath.Join(claude, "in"))
		_ = os.RemoveAll(filepath.Join(claude, "out"))
		if fi, err := os.Lstat(filepath.Join(home, "work")); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(filepath.Join(home, "work"))
		}
		_ = os.MkdirAll(filepath.Join(home, "work"), 0o700)
		reset()
		setup()
		done := make(chan struct{})
		go func() { e.svc.collectSpool(e.now); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the instance hung", what)
		}
		_ = os.RemoveAll(filepath.Join(claude, "in"))
		untouched(what)
	}
	// an answer whose name is already a link replaces the link, not its target
	_ = os.Remove(filepath.Join(home, "work"))
	_ = os.MkdirAll(filepath.Join(home, "work"), 0o700)
	reset()
	_ = os.WriteFile(filepath.Join(in, id+".review"), []byte(`{}`), 0o600)
	_ = os.Symlink(secret, filepath.Join(sp, "out", id+".json"))
	e.svc.collectSpool(e.now)
	untouched("answer over a link")
	if fi, err := os.Lstat(filepath.Join(sp, "out", id+".json")); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("the answer: %v %v", fi, err)
	}
	// out/ as a link: no answer is written through it
	reset()
	_ = os.WriteFile(filepath.Join(in, id+".review"), []byte(`{}`), 0o600)
	_ = os.RemoveAll(filepath.Join(sp, "out"))
	_ = os.Symlink(claude, filepath.Join(sp, "out"))
	e.svc.collectSpool(e.now)
	if _, err := os.Lstat(filepath.Join(claude, id+".json")); err == nil {
		t.Error("an answer was written into .claude")
	}
	untouched("out is a link")
}

// ---------- capture, the room's instance ----------

func TestSpoolCaptureInTheInstance(t *testing.T) {
	e, home := spoolEnv(t)
	tok, _ := e.store.Enable("t")
	get := func() string {
		code, m, body := e.do(t, "GET", apiPrefix+"/preview/capture/wanted", tok, "")
		if code != 200 {
			t.Fatalf("wanted = %d %s", code, body)
		}
		return m["details"].(map[string]any)["id"].(string)
	}
	if id := get(); id != "" {
		t.Errorf("nothing was asked, got %q", id)
	}
	id, err := SpoolCapture(home)
	if err != nil {
		t.Fatal(err)
	}
	if got := get(); got != id {
		t.Fatalf("wanted %q, asked %q", got, id)
	}
	enc := func(w, h int) captureImage {
		return captureImage{w, base64.StdEncoding.EncodeToString(pic(t, w, h))}
	}
	post := func(body any) int {
		b, _ := json.Marshal(body)
		e.svc.lastWrite = time.Time{}
		code, _, _ := e.do(t, "POST", apiPrefix+"/preview/capture/result", tok, string(b))
		return code
	}
	for what, body := range map[string]captureResult{
		"one picture":   {ID: id, Images: []captureImage{enc(390, 800)}},
		"wrong order":   {ID: id, Images: []captureImage{enc(1440, 900), enc(390, 800)}},
		"not a picture": {ID: id, Images: []captureImage{{390, base64.StdEncoding.EncodeToString([]byte("<html>"))}, enc(1440, 900)}},
		"bad id":        {ID: "../../.claude/x", Images: []captureImage{enc(390, 800), enc(1440, 900)}},
	} {
		if code := post(body); code != 400 {
			t.Errorf("%s = %d", what, code)
		}
	}
	if code := post(captureResult{ID: newSpoolID(), Images: []captureImage{enc(390, 800), enc(1440, 900)}}); code != 404 {
		t.Errorf("an answer nobody asked for = %d", code)
	}
	if code := post(captureResult{ID: id, Images: []captureImage{enc(390, 800), enc(1440, 900)}}); code != 200 {
		t.Fatalf("result = %d", code)
	}
	r := reply(t, home, id)
	if r == nil || r.Error != "" || len(r.Files) != 2 {
		t.Fatalf("reply %+v", r)
	}
	for i, f := range r.Files {
		fi, err := os.Lstat(f)
		if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || filepath.Dir(f) != filepath.Join(home, "work", spoolDir, "captures") || !strings.HasSuffix(f, []string{"-390.jpg", "-1440.jpg"}[i]) {
			t.Errorf("file %s: %v %v", f, fi, err)
		}
	}
	if got := get(); got != "" {
		t.Errorf("answered twice: %q", got)
	}
	// an error is passed on as the answer
	id, _ = SpoolCapture(home)
	if code := post(captureResult{ID: id, Error: "capture is not available on this machine"}); code != 200 {
		t.Fatal(code)
	}
	if r := reply(t, home, id); r == nil || r.Error != "capture is not available on this machine" || len(r.Files) != 0 {
		t.Errorf("reply %+v", r)
	}
	// pictures are never written through a link
	id, _ = SpoolCapture(home)
	caps := filepath.Join(home, "work", spoolDir, "captures")
	_ = os.RemoveAll(caps)
	_ = os.Symlink(filepath.Join(home, ".claude"), caps)
	_ = post(captureResult{ID: id, Images: []captureImage{enc(390, 800), enc(1440, 900)}})
	if ents, _ := os.ReadDir(filepath.Join(home, ".claude")); len(ents) != 0 {
		t.Errorf("wrote into .claude: %v", ents)
	}
	if r := reply(t, home, id); r == nil || r.Error == "" {
		t.Errorf("reply %+v", r)
	}
	// a request nobody answers expires
	_ = os.Remove(caps)
	id, err = SpoolCapture(home)
	if err != nil {
		t.Fatal(err)
	}
	e.svc.collectSpool(time.Now().Add(spoolCaptureTTL + time.Minute))
	if r := reply(t, home, id); r == nil || r.Error == "" {
		t.Errorf("expired: %+v", r)
	}
	// a user token forwarded by the admin service may not call these
	if userMayPath("GET", "/preview/capture/wanted") || userMayPath("POST", "/preview/capture/result") {
		t.Error("a user may call the capture routes")
	}
}

func userMayPath(method, path string) bool {
	for _, re := range userRoutes[method] {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// ---------- capture, the admin service ----------

type roomCapRig struct {
	v      *pvEnv
	run    *captureRunner
	wanted map[string]string
	result map[string][]captureResult
	saw    string
	other  string
}

func roomCaptureEnv(t *testing.T) *roomCapRig {
	t.Helper()
	v := previewEnv(t)
	rg := &roomCapRig{v: v, wanted: map[string]string{}, result: map[string][]captureResult{}}
	for _, n := range []string{"anna", "bob"} {
		_ = os.Remove(filepath.Join(v.o.SocketDir, n+".sock"))
		instance(t, v.o, n, func(w http.ResponseWriter, r *http.Request) {
			switch strings.TrimPrefix(r.URL.Path, apiPrefix) {
			case "/preview/state":
				_ = json.NewEncoder(w).Encode(okEnv(v.source[n]))
			case "/preview/file":
				_, _ = io.WriteString(w, v.files[n+":"+r.URL.Query().Get("path")])
			case "/preview/capture/wanted":
				_ = json.NewEncoder(w).Encode(okEnv(map[string]string{"id": rg.wanted[n]}))
			case "/preview/capture/result":
				var res captureResult
				_ = json.NewDecoder(r.Body).Decode(&res)
				rg.result[n] = append(rg.result[n], res)
				rg.wanted[n] = ""
			default:
				w.WriteHeader(404)
			}
		})
	}
	browser := filepath.Join(t.TempDir(), "chrome")
	_ = os.WriteFile(browser, nil, 0o755)
	v.p.cfg.Chromium = browser
	dir := t.TempDir()
	rg.run = &captureRunner{t: t, dir: dir}
	if !v.p.EnableCapture(dir, rg.run) {
		t.Fatal("no capture")
	}
	rg.run.do = func(_ context.Context, job string) ([]byte, error) {
		base, _ := os.ReadFile(filepath.Join(job, "path"))
		c := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return net.Dial("unix", filepath.Join(job, "s"))
		}}}
		fetch := func(p string) string {
			resp, err := c.Get("http://box" + p)
			if err != nil {
				return err.Error()
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.Status + string(b)
		}
		rg.saw = fetch(string(base))
		rg.other = ""
		for _, p := range []string{"/", "/dev/bob/", strings.Replace(string(base), "/anna/", "/bob/", 1), strings.Replace(string(base), "/bob/", "/anna/", 1)} {
			if p == string(base) {
				continue
			}
			if r := fetch(p); !strings.HasPrefix(r, "404") {
				rg.other += p + " " + r
			}
		}
		_ = os.WriteFile(filepath.Join(job, "390.jpg"), pic(t, 390, 844), 0o600)
		_ = os.WriteFile(filepath.Join(job, "1440.jpg"), pic(t, 1440, 900), 0o600)
		return nil, nil
	}
	return rg
}

func TestRoomCaptureGoesOnlyToTheRoomThatAsked(t *testing.T) {
	rg := roomCaptureEnv(t)
	v, ctx := rg.v, context.Background()
	id := newSpoolID()
	rg.wanted["bob"] = id
	for _, n := range []string{"anna", "bob"} {
		v.svc.serveRoomCapture(ctx, n, time.Now())
	}
	if len(rg.run.calls) != 1 || len(rg.result["anna"]) != 0 || len(rg.result["bob"]) != 1 {
		t.Fatalf("calls %v results %v", rg.run.calls, rg.result)
	}
	res := rg.result["bob"][0]
	if res.ID != id || res.Error != "" || len(res.Images) != 2 || res.Images[0].Width != 390 || res.Images[1].Width != 1440 {
		t.Errorf("result %+v", res.Error)
	}
	if !strings.Contains(rg.saw, "hello bob") || rg.other != "" {
		t.Errorf("the box saw %q, reached %q", rg.saw, rg.other)
	}
	if b, _ := os.ReadFile(v.auditFP); !strings.Contains(string(b), `"rpc":"aiagent.roomCapture"`) || !strings.Contains(string(b), "room-agent:bob") {
		t.Errorf("not audited")
	}
	// what a room names is an id or nothing
	for _, bad := range []string{"../../etc/x", "anna", strings.Repeat("a", 33)} {
		rg.run.calls = nil
		v.svc.roomCap.Lock()
		v.svc.roomCap.backoff = map[string]time.Time{}
		v.svc.roomCap.Unlock()
		rg.wanted["anna"] = bad
		v.svc.serveRoomCapture(ctx, "anna", time.Now())
		if len(rg.run.calls) != 0 || len(rg.result["anna"]) != 0 {
			t.Errorf("%q started a capture", bad)
		}
	}
}

func TestRoomCaptureLimits(t *testing.T) {
	rg := roomCaptureEnv(t)
	v, ctx := rg.v, context.Background()
	ask := func(n string) captureResult {
		t.Helper()
		rg.wanted[n] = newSpoolID()
		before := len(rg.result[n])
		v.svc.serveRoomCapture(ctx, n, time.Now())
		if len(rg.result[n]) != before+1 {
			t.Fatalf("no answer for %s", n)
		}
		return rg.result[n][before]
	}
	for i := 0; i < roomCapturePerHr; i++ {
		if r := ask("anna"); r.Error != "" {
			t.Fatalf("capture %d: %s", i, r.Error)
		}
	}
	if r := ask("anna"); !strings.Contains(r.Error, "an hour") {
		t.Errorf("over the limit: %+v", r.Error)
	}
	if r := ask("bob"); r.Error != "" {
		t.Errorf("another room is limited too: %s", r.Error)
	}
	// one at a time across the machine: a busy box leaves the request waiting
	v.p.capture.busy <- struct{}{}
	rg.wanted["bob"] = newSpoolID()
	n := len(rg.result["bob"])
	v.svc.serveRoomCapture(ctx, "bob", time.Now())
	<-v.p.capture.busy
	if len(rg.result["bob"]) != n || rg.wanted["bob"] == "" {
		t.Errorf("answered while busy")
	}
	// one at a time per room
	v.svc.roomCap.busy["bob"] = true
	calls := len(rg.run.calls)
	v.svc.pollRoomCaptures(ctx, time.Now())
	time.Sleep(200 * time.Millisecond)
	v.svc.roomCap.Lock()
	v.svc.roomCap.busy["bob"] = false
	v.svc.roomCap.Unlock()
	if len(rg.result["bob"]) != n {
		t.Errorf("a second capture for a busy room")
	}
	_ = calls
	// a revoked room gets no capture
	st, _ := v.store.Load()
	for _, u := range st.Users {
		if u.OSUser == "bob" {
			if err := v.store.RevokeUser(u.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	calls = len(rg.run.calls)
	if r := ask("bob"); r.Error == "" || len(rg.run.calls) != calls {
		t.Errorf("revoked: %+v", r.Error)
	}
	// without a browser it says so
	v.p.capture = nil
	if r := ask("anna"); r.Error != "capture is not available on this machine" {
		t.Errorf("no browser: %q", r.Error)
	}
}
