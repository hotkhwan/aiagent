package world

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pvHost = "preview.test"

type pvEnv struct {
	*env
	o           *OSUsers
	p           *Preview
	admin, a, b string
	now         time.Time
	source      map[string]PreviewState
	seen        []string
	files       map[string]string
}

func previewEnv(t *testing.T) *pvEnv {
	t.Helper()
	e, _, o := osUserEnv(t)
	v := &pvEnv{env: e, o: o, now: time.Now(), source: map[string]PreviewState{}, files: map[string]string{}}
	v.admin, _ = e.store.Enable("t")
	_, v.a, _ = e.store.AddUser("anna", "anna")
	_, v.b, _ = e.store.AddUser("bob", "bob")
	v.p = NewPreview(e.svc, PreviewConfig{Host: pvHost, Listen: "127.0.0.1:0", Reserved: []string{"taken"}})
	v.p.now = func() time.Time { return v.now }
	e.svc.Preview = v.p
	for _, n := range []string{"anna", "bob"} {
		v.source[n] = PreviewState{Kind: "static", Folder: "x/site", Version: "1"}
		instance(t, o, n, func(w http.ResponseWriter, r *http.Request) {
			v.seen = append(v.seen, n+" "+r.Method+" "+r.URL.Path)
			switch strings.TrimPrefix(r.URL.Path, apiPrefix) {
			case "/preview/state":
				_ = json.NewEncoder(w).Encode(okEnv(v.source[n]))
			case "/preview/file":
				q := r.URL.Query().Get("path")
				body, ok := v.files[n+":"+q]
				if !ok {
					w.WriteHeader(404)
					return
				}
				if q == "" {
					w.Header().Set("X-Preview-Index", "1")
				}
				// what a hostile instance would try
				w.Header().Set("Set-Cookie", "evil=1")
				w.Header().Set("Service-Worker-Allowed", "/")
				_, _ = io.WriteString(w, body)
			case "/preview/review":
				_, _ = io.WriteString(w, `{"code":"SUCCESS","details":{"reviewId":"r20260101-000000-aabbccdd"}}`)
			}
		})
	}
	v.files["anna:"] = "<html><head></head><body>hello anna</body></html>"
	v.files["bob:"] = "hello bob"
	return v
}

func (v *pvEnv) req(method, path string, body string, set ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://"+pvHost+path, strings.NewReader(body))
	r.Host = pvHost
	for i := 0; i+1 < len(set); i += 2 {
		if set[i] == "Host" {
			r.Host = set[i+1]
		} else {
			r.Header.Set(set[i], set[i+1])
		}
	}
	w := httptest.NewRecorder()
	v.p.ServeHTTP(w, r)
	return w
}

func (v *pvEnv) ticket(t *testing.T, token, room string) (int, string) {
	t.Helper()
	v.svc.lastWrite, v.svc.userWrites = time.Time{}, nil
	code, _, body := v.do(t, "POST", apiPrefix+"/preview/ticket", token, `{"room":"`+room+`"}`)
	_, tk, _ := strings.Cut(body, "#t=")
	tk, _, _ = strings.Cut(tk, `"`)
	return code, tk
}

func (v *pvEnv) enter(room, ticket string) *httptest.ResponseRecorder {
	return v.req("POST", "/dev/"+room+"/-/enter", `{"ticket":"`+ticket+`"}`, "X-Aiagent-Preview", "1", "Origin", "https://"+pvHost)
}

// open signs in and returns the cookie and the content base.
func (v *pvEnv) open(t *testing.T, token, room string) (string, string) {
	t.Helper()
	code, tk := v.ticket(t, token, room)
	if code != 200 || len(tk) != 48 {
		t.Fatalf("ticket = %d %q", code, tk)
	}
	w := v.enter(room, tk)
	if w.Code != 200 {
		t.Fatalf("enter = %d %s", w.Code, w.Body)
	}
	cookie, _, _ := strings.Cut(w.Header().Get("Set-Cookie"), ";")
	return cookie, v.base(t, room, cookie)
}

func (v *pvEnv) state(room, cookie string) *httptest.ResponseRecorder {
	return v.req("GET", "/dev/"+room+"/-/state", "", "X-Aiagent-Preview", "1", "Cookie", cookie)
}

func (v *pvEnv) base(t *testing.T, room, cookie string) string {
	t.Helper()
	w := v.state(room, cookie)
	var env struct {
		Details struct {
			Base string `json:"base"`
		} `json:"details"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &env) != nil {
		t.Fatalf("state = %d %s", w.Code, w.Body)
	}
	return env.Details.Base
}

func TestPreviewRoomIsolation(t *testing.T) {
	v := previewEnv(t)
	for _, c := range []struct {
		token, room string
		want        int
	}{{v.a, "anna", 200}, {v.b, "bob", 200}, {v.admin, "anna", 200}, {v.admin, "bob", 200}, {v.a, "bob", 403}, {v.b, "anna", 403}, {v.admin, "nobody", 403}, {v.admin, "ci", 403}, {v.a, "", 403}, {v.a, "../bob", 403}, {"wrong", "anna", 401}} {
		if code, _ := v.ticket(t, c.token, c.room); code != c.want {
			t.Errorf("ticket for %q = %d, want %d", c.room, code, c.want)
		}
	}
	cookie, base := v.open(t, v.a, "anna")
	if w := v.req("GET", base, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "hello anna") {
		t.Fatalf("own page = %d %s", w.Code, w.Body)
	}
	// anna's cookie and capability open nothing of bob's
	if w := v.state("bob", cookie); w.Code != 401 {
		t.Errorf("anna's cookie on bob = %d", w.Code)
	}
	v.seen = nil
	if w := v.req("GET", strings.Replace(base, "/dev/anna/", "/dev/bob/", 1), ""); w.Code != 404 || strings.Contains(w.Body.String(), "bob") || len(v.seen) != 0 {
		t.Errorf("anna's capability on bob = %d %v", w.Code, v.seen)
	}
	// a ticket for anna is not one for bob, and is spent by trying
	_, tk := v.ticket(t, v.a, "anna")
	if w := v.enter("bob", tk); w.Code != 401 {
		t.Errorf("anna's ticket on bob = %d", w.Code)
	}
	if w := v.enter("anna", tk); w.Code != 401 {
		t.Errorf("a ticket survived a wrong room: %d", w.Code)
	}
	// a room's user cannot reach the instance's preview routes of any room
	for _, path := range []string{"/preview/state", "/preview/file?path="} {
		if code, _, _ := v.do(t, "GET", apiPrefix+path, v.a, ""); code != 403 {
			t.Errorf("user GET %s = %d", path, code)
		}
	}
	if code, _, _ := v.do(t, "POST", apiPrefix+"/preview/review", v.a, `{}`); code != 403 {
		t.Errorf("user POST review = %d", code)
	}
	// no session, no content
	if w := v.req("GET", "/dev/anna/~"+strings.Repeat("0", 48)+"/", ""); w.Code != 404 {
		t.Errorf("a made-up capability = %d", w.Code)
	}
	if w := v.state("anna", ""); w.Code != 401 {
		t.Errorf("state without a session = %d", w.Code)
	}
}

func TestPreviewTicketIsSingleUseAndExpires(t *testing.T) {
	v := previewEnv(t)
	_, tk := v.ticket(t, v.a, "anna")
	if w := v.enter("anna", tk); w.Code != 200 {
		t.Fatalf("enter = %d", w.Code)
	}
	if w := v.enter("anna", tk); w.Code != 401 {
		t.Errorf("a ticket worked twice: %d", w.Code)
	}
	_, tk = v.ticket(t, v.a, "anna")
	v.now = v.now.Add(previewTicketTTL + time.Second)
	if w := v.enter("anna", tk); w.Code != 401 {
		t.Errorf("an expired ticket = %d", w.Code)
	}
	log, _ := os.ReadFile(v.auditFP)
	if !strings.Contains(string(log), "aiagent.previewTicket") || !strings.Contains(string(log), "aiagent.previewEnter") || strings.Contains(string(log), tk) {
		t.Errorf("audit log: %s", log)
	}
	// the exchange takes a POST from the shell only
	_, tk = v.ticket(t, v.a, "anna")
	body := `{"ticket":"` + tk + `"}`
	for _, h := range [][]string{
		{"Origin", "https://" + pvHost},
		{"X-Aiagent-Preview", "1", "Origin", "null"},
		{"X-Aiagent-Preview", "1", "Origin", "https://evil.test"},
		{"X-Aiagent-Preview", "1", "Origin", "http://" + pvHost},
		{"X-Aiagent-Preview", "1"},
		{"X-Aiagent-Preview", "1", "Origin", "https://" + pvHost, "Sec-Fetch-Site", "cross-site"},
	} {
		if w := v.req("POST", "/dev/anna/-/enter", body, h...); w.Code != 403 {
			t.Errorf("enter with %v = %d", h, w.Code)
		}
	}
	if w := v.req("GET", "/dev/anna/-/enter", "", "X-Aiagent-Preview", "1"); w.Code != 404 {
		t.Errorf("GET enter = %d", w.Code)
	}
}

func TestPreviewCookieAttributes(t *testing.T) {
	v := previewEnv(t)
	_, tk := v.ticket(t, v.a, "anna")
	w := v.enter("anna", tk)
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("cookies: %v", cs)
	}
	c := cs[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/dev/anna" || c.Domain != "" || len(c.Value) != 48 || !strings.HasPrefix(c.Name, "__Secure-") || c.MaxAge <= 0 {
		t.Errorf("cookie: %+v", c)
	}
	if strings.Contains(w.Body.String(), c.Value) {
		t.Error("the cookie is readable by script")
	}
}

func TestPreviewCapabilityExpiresAndEndsWithTheRoom(t *testing.T) {
	v := previewEnv(t)
	cookie, base := v.open(t, v.a, "anna")
	_, adminBase := v.open(t, v.admin, "anna")
	if base == adminBase {
		t.Fatal("two viewers share a capability")
	}
	// idle: the shell closed
	v.now = v.now.Add(previewIdleTTL + time.Second)
	if w := v.req("GET", base, ""); w.Code != 404 {
		t.Errorf("an idle capability = %d", w.Code)
	}
	fresh := v.base(t, "anna", cookie)
	if fresh == base {
		t.Error("an idle capability came back")
	}
	if w := v.req("GET", fresh, ""); w.Code != 200 {
		t.Errorf("the new capability = %d", w.Code)
	}
	// reissuing the room's token ends the user's and the admin's sessions on it
	cookie, base = v.open(t, v.a, "anna")
	adminCookie, adminBase := v.open(t, v.admin, "anna")
	_, bobBase := v.open(t, v.b, "bob")
	st, _ := v.store.Load()
	_, tok, err := v.store.ReissueUser(st.Users[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []string{base, adminBase} {
		if w := v.req("GET", x, ""); w.Code != 404 {
			t.Errorf("content after reissue = %d", w.Code)
		}
	}
	for _, x := range []string{cookie, adminCookie} {
		if w := v.state("anna", x); w.Code != 401 {
			t.Errorf("session after reissue = %d", w.Code)
		}
	}
	if w := v.req("GET", bobBase, ""); w.Code != 200 {
		t.Errorf("another room's session ended too: %d", w.Code)
	}
	// a removal in progress (blocked, then revoked) ends them as well
	cookie, base = v.open(t, tok, "anna")
	if _, err := v.store.BlockUser(st.Users[0].ID); err != nil {
		t.Fatal(err)
	}
	if w := v.req("GET", base, ""); w.Code != 404 {
		t.Errorf("content of a blocked room = %d", w.Code)
	}
	if w := v.state("anna", cookie); w.Code != 401 {
		t.Errorf("session of a blocked room = %d", w.Code)
	}
	// the admin's token rotated; the sessions it opened end
	_, adminBase = v.open(t, v.admin, "bob")
	if _, err := v.store.Enable("t"); err != nil {
		t.Fatal(err)
	}
	if w := v.req("GET", adminBase, ""); w.Code != 404 {
		t.Errorf("content after the admin token rotated = %d", w.Code)
	}
	if w := v.req("GET", bobBase, ""); w.Code != 200 {
		t.Errorf("bob's own session ended with the admin's: %d", w.Code)
	}
	// 8 hours at most, however busy
	for i := 0; i < 100; i++ {
		v.now = v.now.Add(5 * time.Minute)
		for _, ss := range v.p.sessions {
			v.p.touch(ss)
		}
	}
	if w := v.req("GET", bobBase, ""); w.Code != 404 {
		t.Errorf("a session outlived its day: %d", w.Code)
	}
}

func TestPreviewHostSeparation(t *testing.T) {
	v := previewEnv(t)
	_, base := v.open(t, v.a, "anna")
	for _, h := range []string{"aiagent.test", "", "127.0.0.1", pvHost + ".evil.test", pvHost + ":x"} {
		for _, path := range []string{base, "/dev/anna/", "/dev/anna/-/state"} {
			if w := v.req("GET", path, "", "Host", h); w.Code != 404 || w.Header().Get("Content-Security-Policy") == "" {
				t.Errorf("%s on host %q = %d", path, h, w.Code)
			}
		}
	}
	// nothing of the rest of aiagent on the preview listener
	for _, path := range []string{"/", "/aiagent", "/aiagent/api/me", "/aiagent/app.js", "/dev", "/dev/", "/dev/a", "/dev/Anna/", "/dev/%61nna/", "/dev//anna/"} {
		if w := v.req("GET", path, "", TokenHeader, v.admin); w.Code != 404 {
			t.Errorf("preview listener %s = %d", path, w.Code)
		}
	}
	// and the main listener refuses the preview host, however it is spelled
	for _, h := range []string{pvHost, strings.ToUpper(pvHost), pvHost + ":443", pvHost + "."} {
		for _, path := range []string{"/", "/aiagent", "/aiagent/api/me", "/aiagent/app.js", "/aiagent/review-view/x", "/dev/anna/"} {
			r := httptest.NewRequest("GET", "http://"+pvHost+path, nil)
			r.Host = h
			r.Header.Set(TokenHeader, v.admin)
			resp, err := v.app.Test(r)
			if err != nil {
				t.Fatal(err)
			}
			if b, _ := io.ReadAll(resp.Body); resp.StatusCode != 404 || strings.Contains(string(b), "WEBINIT") {
				t.Errorf("main listener %s on %q = %d", path, h, resp.StatusCode)
			}
		}
	}
	r := httptest.NewRequest("GET", "http://aiagent.test/aiagent", nil)
	if resp, err := v.app.Test(r); err != nil || resp.StatusCode != 200 {
		t.Errorf("aiagent on its own host: %v", resp)
	}
	// off means off
	_ = v.store.Disable("t")
	if w := v.req("GET", "/dev/anna/", ""); w.Code != 404 {
		t.Errorf("shell while disabled = %d", w.Code)
	}
}

func TestPreviewShellHeaders(t *testing.T) {
	v := previewEnv(t)
	w := v.req("GET", "/dev/anna/", "")
	csp := w.Header().Get("Content-Security-Policy")
	if w.Code != 200 || !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "script-src "+pvHost+"/dev/anna/-/;") || strings.Contains(csp, "unsafe") || strings.Contains(csp, "'self'") {
		t.Errorf("shell = %d %s", w.Code, csp)
	}
	if !strings.Contains(w.Body.String(), `sandbox="allow-scripts allow-forms"`) {
		t.Error("the frame is not sandboxed as it must be")
	}
	if strings.Contains(w.Body.String(), "style=") {
		t.Error("the shell has an inline style, which its CSP refuses")
	}
	if w := v.req("GET", "/dev/anna", ""); w.Code != 308 || w.Header().Get("Location") != "/dev/anna/" {
		t.Errorf("no slash = %d %s", w.Code, w.Header().Get("Location"))
	}
	for _, f := range []string{"shell.js", "shell.css"} {
		if w := v.req("GET", "/dev/anna/-/"+f, ""); w.Code != 200 || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s = %d", f, w.Code)
		}
	}
	// a room that does not exist looks the same as one that does
	if w := v.req("GET", "/dev/nobody/", ""); w.Code != 200 {
		t.Errorf("unknown room = %d", w.Code)
	}
}

func TestPreviewReservedNames(t *testing.T) {
	v := previewEnv(t)
	// a room made before the name was reserved still has no page
	if _, _, err := v.store.AddUser("taken", "taken"); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ci", "cd", "api", "kapi", "events", "gateway", "dev", "admin", "assets", "static", "ws", "login", "health", "taken"} {
		v.svc.lastWrite, v.svc.userWrites = time.Time{}, nil
		if code, _, body := v.do(t, "POST", apiPrefix+"/tokens", v.admin, `{"name":"`+n+`"}`); code != 400 || !strings.Contains(body, "สงวน") {
			t.Errorf("a room named %s = %d", n, code)
		}
		if w := v.req("GET", "/dev/"+n+"/", ""); w.Code != 404 {
			t.Errorf("a page at /dev/%s/ = %d", n, w.Code)
		}
		if code, _ := v.ticket(t, v.admin, n); code != 403 {
			t.Errorf("a ticket for %s = %d", n, code)
		}
	}
	// reserved without a preview too
	v.svc.Preview = nil
	v.svc.lastWrite, v.svc.userWrites = time.Time{}, nil
	if code, _, _ := v.do(t, "POST", apiPrefix+"/tokens", v.admin, `{"name":"ci"}`); code != 400 {
		t.Errorf("ci without a preview = %d", code)
	}
}

func checkContentHeaders(t *testing.T, what string, h http.Header) {
	t.Helper()
	if csp := h.Values("Content-Security-Policy"); len(csp) != 1 || csp[0] != "sandbox allow-scripts allow-forms" {
		t.Errorf("%s: CSP %q", what, csp)
	}
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("%s: headers %v", what, h)
	}
	if h.Get("Access-Control-Allow-Origin") != "null" || h.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("%s: CORS %v", what, h)
	}
	for _, k := range []string{"Set-Cookie", "Service-Worker-Allowed", "Clear-Site-Data", "Www-Authenticate", "Refresh", "Link", "Alt-Svc", "Strict-Transport-Security", "X-Frame-Options", "Report-To", "Server"} {
		if h.Get(k) != "" {
			t.Errorf("%s: %s reached the browser", what, k)
		}
	}
}

func TestPreviewStaticContentIsAlwaysSandboxed(t *testing.T) {
	v := previewEnv(t)
	_, base := v.open(t, v.a, "anna")
	v.files["anna:app.js"] = "let x"
	v.files["anna:pic.svg"] = "<svg/>"
	for path, ct := range map[string]string{"": "text/html", "app.js": "text/javascript", "pic.svg": "image/svg+xml"} {
		w := v.req("GET", base+path, "")
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), ct) {
			t.Errorf("%q = %d %s", path, w.Code, w.Header().Get("Content-Type"))
		}
		checkContentHeaders(t, path, w.Header())
	}
	for what, w := range map[string]*httptest.ResponseRecorder{
		"missing file":   v.req("GET", base+"nope", ""),
		"bad capability": v.req("GET", "/dev/anna/~"+strings.Repeat("a", 48)+"/", ""),
		"short":          v.req("GET", "/dev/anna/~zz/", ""),
		"POST":           v.req("POST", base, "x"),
		"preflight":      v.req("OPTIONS", base, "", "Access-Control-Request-Method", "PUT", "Origin", "null"),
		"no slash":       v.req("GET", strings.TrimSuffix(base, "/"), ""),
		"wrong host":     v.req("GET", base, "", "Host", "x.test"),
		"service worker": v.req("GET", base+"app.js", "", "Service-Worker", "script"),
	} {
		if w.Code == 200 {
			t.Errorf("%s = 200", what)
		}
		checkContentHeaders(t, what, w.Header())
	}
	// another page of the origin cannot take a room's file as its own script
	for _, c := range []struct {
		site, dest string
		want       int
	}{{"same-origin", "script", 403}, {"same-origin", "empty", 403}, {"same-site", "worker", 403}, {"same-origin", "object", 403}, {"same-origin", "iframe", 200}, {"none", "document", 200}, {"cross-site", "script", 200}} {
		if w := v.req("GET", base+"app.js", "", "Sec-Fetch-Site", c.site, "Sec-Fetch-Dest", c.dest); w.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.site, c.dest, w.Code, c.want)
		}
	}
	v.source["anna"] = PreviewState{Kind: "port", Port: 22}
	v.p.states = map[string]pvState{}
	w := v.req("GET", base, "")
	if w.Code != 404 {
		t.Errorf("a port outside the range = %d", w.Code)
	}
	checkContentHeaders(t, "bad port", w.Header())
}

// ---------- dev server ----------

// fakeNet writes a proc root whose tcp tables list these sockets.
type fakeSock struct {
	v6    bool
	addr  string // as the kernel prints it, with the port
	state string
	uid   int
}

func fakeNet(t *testing.T, socks ...fakeSock) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	head := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	out := map[bool]string{false: head, true: head}
	for i, s := range socks {
		rem := "00000000:0000"
		if s.v6 {
			rem = strings.Repeat("0", 32) + ":0000"
		}
		out[s.v6] += fmt.Sprintf("%4d: %s %s %s 00000000:00000000 00:00000000 00000000 %5d        0 %d 1\n", i, s.addr, rem, s.state, s.uid, 1000+i)
	}
	for v6, name := range map[bool]string{false: "tcp", true: "tcp6"} {
		if err := os.WriteFile(filepath.Join(dir, "net", name), []byte(out[v6]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPreviewPortOwnership(t *testing.T) {
	const own, other = 700001, 700002
	lo4, any4 := "0100007F:0BB8", "00000000:0BB8" // port 3000
	lo6, any6 := "00000000000000000000000001000000:0BB8", strings.Repeat("0", 32)+":0BB8"
	mapped := "0000000000000000FFFF00000100007F:0BB8"
	for name, c := range map[string]struct {
		socks []fakeSock
		want  string // address, "" is refused
	}{
		"own loopback":                     {[]fakeSock{{false, lo4, "0A", own}}, "127.0.0.1:3000"},
		"own wildcard":                     {[]fakeSock{{false, any4, "0A", own}}, "127.0.0.1:3000"},
		"own ::1":                          {[]fakeSock{{true, lo6, "0A", own}}, "[::1]:3000"},
		"own ::":                           {[]fakeSock{{true, any6, "0A", own}}, "127.0.0.1:3000"},
		"own v4-mapped":                    {[]fakeSock{{true, mapped, "0A", own}}, "127.0.0.1:3000"},
		"nothing":                          {nil, ""},
		"another room's":                   {[]fakeSock{{false, lo4, "0A", other}}, ""},
		"root's":                           {[]fakeSock{{false, lo4, "0A", 0}}, ""},
		"another room's wildcard":          {[]fakeSock{{false, any4, "0A", other}}, ""},
		"another room's ::":                {[]fakeSock{{true, any6, "0A", other}}, ""},
		"own loopback beside a foreign ::": {[]fakeSock{{false, lo4, "0A", own}, {true, any6, "0A", other}}, ""},
		"foreign loopback, own wildcard":   {[]fakeSock{{false, lo4, "0A", other}, {false, any4, "0A", own}}, ""},
		"own ::1 beside a foreign v4":      {[]fakeSock{{true, lo6, "0A", own}, {false, lo4, "0A", other}}, ""},
		"own, but only a connection":       {[]fakeSock{{false, lo4, "01", own}}, ""},
		"own, on another port":             {[]fakeSock{{false, "0100007F:0BB9", "0A", own}}, ""},
		"own, on another address":          {[]fakeSock{{false, "0200000A:0BB8", "0A", own}}, ""},
		"a connection of another is fine":  {[]fakeSock{{false, lo4, "0A", own}, {false, lo4, "01", other}}, "127.0.0.1:3000"},
	} {
		got, err := roomListener(fakeNet(t, c.socks...), 3000, own)
		if (err == nil) != (c.want != "") || err == nil && got != netip.MustParseAddrPort(c.want) {
			t.Errorf("%s: %v, %v; want %q", name, got, err, c.want)
		}
	}
	for _, p := range []int{22, 2999, 6000, 8890, 0, -1, 70000} {
		if _, err := roomListener(fakeNet(t, fakeSock{false, fmt.Sprintf("0100007F:%04X", p&0xffff), "0A", own}), p, own); err == nil {
			t.Errorf("port %d accepted", p)
		}
	}
	if _, err := roomListener(t.TempDir(), 3000, own); err == nil {
		t.Error("an unreadable table was taken as permission")
	}
}

// devServer listens on a port in the range, which the fake proc says uid owns.
func (v *pvEnv) devServer(t *testing.T, uid int, h http.HandlerFunc) {
	t.Helper()
	var ln net.Listener
	var err error
	for port := 5900; port < 6000; port++ {
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			v.source["anna"] = PreviewState{Kind: "port", Port: port}
			v.p.procRoot = fakeNet(t, fakeSock{false, fmt.Sprintf("0100007F:%04X", port), "0A", uid})
			break
		}
	}
	if err != nil {
		t.Skip("no free port in the range")
	}
	v.p.states = map[string]pvState{}
	v.p.uidOf = func(string) (uint32, error) { return 700001, nil }
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

func TestPreviewDevServerOfAnotherAccountIsNeverReached(t *testing.T) {
	v := previewEnv(t)
	cookie, base := v.open(t, v.a, "anna")
	hits := 0
	v.devServer(t, 700002, func(w http.ResponseWriter, r *http.Request) { hits++ })
	w := v.req("GET", base, "")
	if w.Code != 502 || hits != 0 {
		t.Errorf("another room's server = %d, %d hits", w.Code, hits)
	}
	checkContentHeaders(t, "squatted port", w.Header())
	if w := v.state("anna", cookie); !strings.Contains(w.Body.String(), `"version":"down"`) {
		t.Errorf("a squatted port is called up: %s", w.Body)
	}
	if w := v.req("GET", base, "", "Connection", "Upgrade", "Upgrade", "websocket"); w.Code != 502 || hits != 0 {
		t.Errorf("a socket to another room's server = %d, %d hits", w.Code, hits)
	}
	// an account outside the rooms' ids owns nothing a room may show
	v.devServer(t, 1000, func(w http.ResponseWriter, r *http.Request) { hits++ })
	v.p.uidOf = func(string) (uint32, error) { return 1000, nil }
	if w := v.req("GET", base, ""); w.Code != 502 || hits != 0 {
		t.Errorf("a uid outside the range = %d, %d hits", w.Code, hits)
	}
}

func TestPreviewDevServerHeadersBothWays(t *testing.T) {
	v := previewEnv(t)
	_, base := v.open(t, v.a, "anna")
	var got http.Header
	var host, uri string
	status := 200
	v.devServer(t, 700001, func(w http.ResponseWriter, r *http.Request) {
		got, host, uri = r.Header.Clone(), r.Host, r.RequestURI
		// early hints would let a proxy that copies them lose its own headers
		w.Header().Set("Link", "</x>; rel=preload")
		w.WriteHeader(http.StatusEarlyHints)
		for k, x := range map[string]string{
			"Set-Cookie": "a=b; Path=/", "Service-Worker-Allowed": "/", "Clear-Site-Data": `"*"`, "Content-Security-Policy": "default-src *", "X-Frame-Options": "DENY",
			"Access-Control-Allow-Origin": "*", "Access-Control-Allow-Credentials": "true", "Www-Authenticate": "Basic", "Refresh": "0;url=/ci", "Alt-Svc": "x", "Report-To": "{}", "Strict-Transport-Security": "max-age=1", "Referrer-Policy": "unsafe-url", "Server": "x",
		} {
			w.Header().Set(k, x)
		}
		w.Header().Set("Trailer", "Set-Cookie")
		ct := "text/html"
		if strings.HasSuffix(r.URL.Path, ".js") {
			ct = "text/javascript"
		}
		w.Header().Set("Content-Type", ct)
		if r.URL.Path == "/go" {
			w.Header().Set("Location", r.URL.Query().Get("to"))
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, "<html><head><script src=\"/main.js\"></script></head></html>")
		w.Header().Set("Set-Cookie", "late=1")
	})
	w := v.req("GET", base+"a%2Fb/app?x=1", "", "Cookie", previewCookie+"=secret; other=1", "Authorization", "Basic x", "Proxy-Authorization", "y", "Referer", "https://"+pvHost+base,
		"X-Forwarded-For", "1.2.3.4", "Cf-Connecting-Ip", "1.2.3.4", TokenHeader, "tok", "X-Custom", "keep", "Origin", "null")
	if w.Code != 200 || uri != "/a%2Fb/app?x=1" || !strings.HasPrefix(host, "localhost:") {
		t.Fatalf("proxied = %d %s %s", w.Code, host, uri)
	}
	for _, k := range []string{"Cookie", "Authorization", "Proxy-Authorization", "Referer", "X-Forwarded-For", "Cf-Connecting-Ip", TokenHeader} {
		if got.Get(k) != "" {
			t.Errorf("%s reached the room: %q", k, got.Get(k))
		}
	}
	if got.Get("X-Custom") != "keep" || !strings.HasPrefix(got.Get("Origin"), "http://localhost:") {
		t.Errorf("request headers: %v", got)
	}
	checkContentHeaders(t, "html", w.Header())
	if !strings.Contains(w.Body.String(), `src="`+base+`main.js"`) || !strings.Contains(w.Body.String(), "importmap") {
		t.Errorf("page: %s", w.Body)
	}
	w = v.req("GET", base+"main.js", "")
	checkContentHeaders(t, "js", w.Header())
	if w.Header().Get("Content-Type") != "text/javascript" {
		t.Errorf("js type: %v", w.Header())
	}
	for _, st := range []int{201, 404, 500, 503} {
		status = st
		w := v.req("GET", base+"x.js", "")
		if w.Code != st {
			t.Errorf("status %d = %d", st, w.Code)
		}
		checkContentHeaders(t, fmt.Sprint(st), w.Header())
	}
	status = 401
	if w := v.req("GET", base, ""); w.Code != 502 {
		t.Errorf("a sign-in prompt = %d", w.Code)
	}
	port := fmt.Sprint(v.source["anna"].Port)
	for to, want := range map[string]string{
		"/next?a=b": base + "next?a=b", "http://localhost:" + port + "/q": base + "q", "rel": "rel",
		"https://evil.test/": "", "//evil.test/": "", "/../ci/": "", "/%2e%2e/ci": "", "../../ci": "", "http://localhost:22/": "", `/\evil`: "", "javascript:alert(1)": "",
	} {
		w := v.req("GET", base+"go?to="+strings.NewReplacer("%", "%25", "?", "%3F", "&", "%26", "\\", "%5C").Replace(to), "")
		if got := w.Header().Get("Location"); got != want || (want == "") != (w.Code == 502) {
			t.Errorf("redirect to %q: %d %q, want %q", to, w.Code, got, want)
		}
		checkContentHeaders(t, "redirect", w.Header())
	}
}

func TestPreviewDevServerWebSocket(t *testing.T) {
	v := previewEnv(t)
	_, base := v.open(t, v.a, "anna")
	var cookie string
	v.devServer(t, 700001, func(w http.ResponseWriter, r *http.Request) {
		cookie = r.Header.Get("Cookie")
		c, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: yes\r\nSet-Cookie: a=b\r\nContent-Security-Policy: x\r\n\r\nhi ")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("got " + line)
		_ = rw.Flush()
		_, _ = rw.ReadString('\n')
	})
	srv := httptest.NewServer(v.p)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET %shmr HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nCookie: x=y\r\nSec-WebSocket-Key: k\r\n\r\nping\n", base, pvHost)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 || resp.Header.Get("Sec-Websocket-Accept") != "yes" {
		t.Fatalf("upgrade: %v %v", resp, err)
	}
	checkContentHeaders(t, "101", resp.Header)
	if line, _ := br.ReadString('\n'); line != "hi got ping\n" {
		t.Errorf("socket said %q", line)
	}
	if cookie != "" {
		t.Errorf("cookie reached the room: %q", cookie)
	}
	// revoking the room ends a socket that is open
	st, _ := v.store.Load()
	if _, err := v.store.BlockUser(st.Users[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := br.ReadByte(); err != io.EOF {
		t.Errorf("the socket stayed open: %v", err)
	}
}

// ---------- the room's side ----------

// roomEnv is a room's own instance with a work folder.
func roomEnv(t *testing.T) (*env, string, string) {
	t.Helper()
	e := newEnv(t)
	tok, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(e.home, "work")
	put(t, work, "app/site/index.html", "<p>one")
	put(t, work, "app/site/a/b.txt", "b")
	put(t, work, "app/site/.env", "SECRET")
	put(t, work, "app/site/.git/config", "SECRET")
	put(t, work, "app/.env", "SECRET")
	put(t, work, "app/secret.txt", "outside the folder")
	put(t, e.home, "home.txt", "SECRET")
	return e, tok, work
}

func put(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPreviewFilesCannotEscape(t *testing.T) {
	e, tok, work := roomEnv(t)
	site := filepath.Join(work, "app", "site")
	for name, to := range map[string]string{
		"abs": filepath.Join(e.home, "home.txt"), "rel": "../../../home.txt", "up": "../../..", "dot": ".env", "dot2": "../.env", "root": "/etc/passwd", "gitdir": ".git", "in": "a/b.txt", "proc": "/proc/self/environ",
	} {
		if err := os.Symlink(to, filepath.Join(site, name)); err != nil {
			t.Fatal(err)
		}
	}
	get := func(p string) (int, string) {
		code, _, body := e.do(t, "GET", apiPrefix+"/preview/file?path="+p, tok, "")
		return code, body
	}
	for _, p := range []string{"", "a/b.txt", "in", "index.html"} {
		if code, _ := get(p); code != 200 {
			t.Errorf("%q = %d", p, code)
		}
	}
	for _, p := range []string{"abs", "rel", "up", "up/home.txt", "dot", "dot2", "root", "gitdir/config", "proc", ".env", ".git/config", "a/../.env", "../secret.txt", "..", "../../../home.txt", "%2e%2e/secret.txt", "/etc/passwd", "a//../../secret.txt", "a%00", "..%5csecret.txt", "a", "a/", "a/b.txt/", "nope"} {
		if code, body := get(p); code != 404 || strings.Contains(body, "SECRET") {
			t.Errorf("%q = %d %s", p, code, body)
		}
	}
	if code, _, _ := e.do(t, "GET", apiPrefix+"/preview/file?path=", "wrong", ""); code != 401 {
		t.Errorf("without the instance's token = %d", code)
	}
	// the service that stands for every room has no files of its own here
	e.svc.OSUsers = &OSUsers{}
	if code, _ := get(""); code != 404 {
		t.Errorf("the admin service served a file: %d", code)
	}
}

func TestPreviewSource(t *testing.T) {
	e, tok, work := roomEnv(t)
	if st := e.svc.previewSource(); st.Kind != "static" || st.Folder != "app/site" {
		t.Errorf("default: %+v", st)
	}
	put(t, work, "new/dist/index.html", "x")
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(work, "app/site/index.html"), old, old)
	if st := e.svc.previewSource(); st.Folder != "new/dist" {
		t.Errorf("newest: %+v", st)
	}
	_ = os.Symlink(e.home, filepath.Join(work, "out"))
	_ = os.Symlink("app/.git", filepath.Join(work, "hid"))
	for conf, want := range map[string]PreviewState{
		`{"folder":"app/site"}`:                   {Kind: "static", Folder: "app/site"},
		`{"folder":"./app/site/"}`:                {Kind: "static", Folder: "app/site"},
		`{"port":5173}`:                           {Kind: "port", Port: 5173},
		`{"port":22}`:                             {Kind: "none"},
		`{"port":6000}`:                           {Kind: "none"},
		`{"folder":"app/site","port":3000}`:       {Kind: "none"},
		`{"folder":"../"}`:                        {Kind: "none"},
		`{"folder":"/etc"}`:                       {Kind: "none"},
		`{"folder":"."}`:                          {Kind: "none"},
		`{"folder":"app/site/.git"}`:              {Kind: "none"},
		`{"folder":"out"}`:                        {Kind: "none"},
		`{"folder":"app/site/index.html"}`:        {Kind: "none"},
		`{"folder":"nope"}`:                       {Kind: "none"},
		`{"url":"http://x"}`:                      {Kind: "none"},
		`{}`:                                      {Kind: "none"},
		`nonsense`:                                {Kind: "none"},
		`{"port":"5173"}`:                         {Kind: "none"},
		`{"folder":"` + strings.Repeat("x", 5000): {Kind: "none"},
	} {
		put(t, work, previewFile, conf)
		got := e.svc.previewSource()
		if want.Kind == "none" && got.Error == "" {
			t.Errorf("%s: nothing said why", conf)
		}
		got.Error = ""
		if got != want {
			t.Errorf("%s: %+v, want %+v", conf, got, want)
		}
	}
	put(t, work, previewFile, `{"folder":"app/site"}`)
	version := func() string {
		_, m, _ := e.do(t, "GET", apiPrefix+"/preview/state", tok, "")
		d, _ := m["details"].(map[string]any)
		s, _ := d["version"].(string)
		return s
	}
	one := version()
	if one == "" || version() != one {
		t.Errorf("version is not stable: %q", one)
	}
	put(t, work, "app/site/a/b.txt", "changed")
	if version() == one {
		t.Error("a changed file is not seen")
	}
}

func pic(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPreviewReviewIsTheRoomsOwn(t *testing.T) {
	e, tok, _ := roomEnv(t)
	body := func(note string, imgs ...captureImage) string {
		b, _ := json.Marshal(previewReviewBody{Note: note, Images: imgs})
		return string(b)
	}
	ok := captureImage{390, base64.StdEncoding.EncodeToString(pic(t, 390, 800))}
	for what, b := range map[string]string{
		"none": body("x"), "wrong width": body("x", captureImage{391, ok.Data}), "not a picture": body("x", captureImage{390, base64.StdEncoding.EncodeToString([]byte("<script>"))}),
		"not base64": body("x", captureImage{390, `"><script>`}), "three": body("x", ok, ok, ok), "too tall": body("x", captureImage{390, base64.StdEncoding.EncodeToString(pic(t, 390, captureMaxHeight+1))}),
	} {
		e.svc.lastWrite, e.svc.userWrites = time.Time{}, nil
		if code, _, _ := e.do(t, "POST", apiPrefix+"/preview/review", tok, b); code != 400 {
			t.Errorf("%s = %d", what, code)
		}
	}
	e.svc.lastWrite, e.svc.userWrites = time.Time{}, nil
	code, _, out := e.do(t, "POST", apiPrefix+"/preview/review", tok, body("<b>look</b>", ok, captureImage{1440, base64.StdEncoding.EncodeToString(pic(t, 1440, 900))}))
	if code != 200 {
		t.Fatalf("review = %d %s", code, out)
	}
	rs := e.col.Reviews()
	if len(rs) != 1 || rs[0].home != e.home {
		t.Fatalf("reviews: %+v", rs)
	}
	page, _ := e.col.ReviewPage(rs[0].ID)
	if strings.Count(string(page), "data-item=") != 3 || strings.Count(string(page), "data:image/jpeg;base64,") != 2 || strings.Contains(string(page), "<b>look") {
		t.Errorf("page: %.600s", page)
	}
}

// ---------- capture ----------

type captureRunner struct {
	t     *testing.T
	calls []string
	do    func(ctx context.Context, job string) ([]byte, error)
	dir   string
}

func (r *captureRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if args[0] != "start" {
		return nil, nil
	}
	id := strings.TrimSuffix(strings.TrimPrefix(args[1], "aiagent-capture@"), ".service")
	return r.do(ctx, filepath.Join(r.dir, id))
}

func TestPreviewCapture(t *testing.T) {
	v := previewEnv(t)
	cookie, _ := v.open(t, v.a, "anna")
	post := func(path, body string, set ...string) *httptest.ResponseRecorder {
		return v.req("POST", "/dev/anna/-/"+path, body, append([]string{"X-Aiagent-Preview", "1", "Origin", "https://" + pvHost, "Cookie", cookie}, set...)...)
	}
	// without a browser it is said, not offered
	if v.p.EnableCapture(t.TempDir(), nil) {
		t.Fatal("capture without a browser")
	}
	if w := v.state("anna", cookie); !strings.Contains(w.Body.String(), `"capture":false`) {
		t.Errorf("state: %s", w.Body)
	}
	if w := post("capture", "{}"); w.Code != 404 {
		t.Errorf("capture without a browser = %d", w.Code)
	}
	browser := filepath.Join(t.TempDir(), "chrome")
	_ = os.WriteFile(browser, nil, 0o755)
	v.p.cfg.Chromium = browser
	dir := shortTempDir(t) // a capture listens on a socket in it
	run := &captureRunner{t: t, dir: dir}
	if !v.p.EnableCapture(dir, run) {
		t.Fatal("a browser is there")
	}
	write := func(job string, w int, b []byte) {
		_ = os.WriteFile(filepath.Join(job, fmt.Sprintf("%d.jpg", w)), b, 0o600)
	}
	var got, other string
	run.do = func(_ context.Context, job string) ([]byte, error) {
		if fi, err := os.Stat(job); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("the job folder: %v", fi.Mode())
		}
		base, _ := os.ReadFile(filepath.Join(job, "path"))
		if !previewCapturePathRE.Match(base) {
			t.Errorf("path %q", base)
		}
		c := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
			return net.Dial("unix", filepath.Join(job, "s"))
		}}}
		fetch := func(p string) string {
			resp, err := c.Get("http://box" + p)
			if err != nil {
				return err.Error()
			}
			defer resp.Body.Close()
			checkContentHeaders(t, "capture "+p, resp.Header)
			b, _ := io.ReadAll(resp.Body)
			return resp.Status + string(b)
		}
		got = fetch(string(base))
		for _, p := range []string{"/", "/dev/anna/", "/dev/anna/-/state", "/aiagent/api/me", strings.Replace(string(base), "anna", "bob", 1)} {
			if r := fetch(p); !strings.HasPrefix(r, "404") {
				other += p + " " + r
			}
		}
		write(job, 390, pic(t, 390, 844))
		write(job, 1440, pic(t, 1440, 900))
		return nil, nil
	}
	w := post("capture", "{}")
	if w.Code != 200 || len(run.calls) != 1 {
		t.Fatalf("capture = %d %s %v", w.Code, w.Body, run.calls)
	}
	if f := strings.Fields(run.calls[0]); len(f) != 3 || f[0] != "systemctl" || f[1] != "start" || len(f[2]) != len("aiagent-capture@.service")+32 || strings.ContainsAny(f[2], "/ ;$") {
		t.Errorf("argv: %q", run.calls[0])
	}
	if !strings.Contains(got, "hello anna") || other != "" {
		t.Errorf("the box saw %q; reached %q", got, other)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("job folders left: %v", left)
	}
	var env struct {
		Details struct {
			ID     string         `json:"id"`
			Images []captureImage `json:"images"`
		} `json:"details"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if len(env.Details.Images) != 2 {
		t.Fatalf("images: %s", w.Body)
	}
	// filed in the room it is of, once
	v.seen = nil
	if w := post("review", `{"id":"`+env.Details.ID+`","note":"ok"}`); w.Code != 200 || len(v.seen) != 1 || v.seen[0] != "anna POST "+apiPrefix+"/preview/review" {
		t.Errorf("review = %d %v", w.Code, v.seen)
	}
	if w := post("review", `{"id":"`+env.Details.ID+`"}`); w.Code != 409 {
		t.Errorf("a capture was filed twice: %d", w.Code)
	}
	// not from the frame, not without a session
	for _, h := range [][]string{{"Origin", "null"}, {"X-Aiagent-Preview", ""}, {"Cookie", ""}} {
		if w := post("capture", "{}", h...); w.Code != 403 && w.Code != 401 {
			t.Errorf("capture with %v = %d", h, w.Code)
		}
	}
	// what the box leaves is not trusted
	for what, f := range map[string]func(job string){
		"nothing":     func(string) {},
		"a page":      func(j string) { write(j, 390, []byte("<html>")); write(j, 1440, pic(t, 1440, 900)) },
		"wrong width": func(j string) { write(j, 390, pic(t, 800, 800)); write(j, 1440, pic(t, 1440, 900)) },
		"too large":   func(j string) { write(j, 390, make([]byte, captureMaxBytes+1)); write(j, 1440, pic(t, 1440, 900)) },
		"a link": func(j string) {
			real := filepath.Join(t.TempDir(), "x")
			_ = os.WriteFile(real, pic(t, 390, 844), 0o600)
			_ = os.Symlink(real, filepath.Join(j, "390.jpg"))
			write(j, 1440, pic(t, 1440, 900))
		},
	} {
		run.do = func(_ context.Context, job string) ([]byte, error) { f(job); return nil, nil }
		if w := post("capture", "{}"); w.Code != 502 {
			t.Errorf("%s = %d", what, w.Code)
		}
	}
	// a hard limit, and the unit is stopped with it
	v.p.capture.timeout = 50 * time.Millisecond
	run.calls = nil
	run.do = func(ctx context.Context, _ string) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	if w := post("capture", "{}"); w.Code != 502 || len(run.calls) != 2 || !strings.HasPrefix(run.calls[1], "systemctl stop aiagent-capture@") {
		t.Errorf("a hung browser = %d %v", w.Code, run.calls)
	}
	// one at a time
	v.p.capture.busy <- struct{}{}
	if w := post("capture", "{}"); w.Code != 429 {
		t.Errorf("a second capture = %d", w.Code)
	}
}

func TestChromiumArgs(t *testing.T) {
	a := strings.Join(chromiumArgs("/tmp/p", true), " ")
	if strings.Contains(a, "--no-sandbox") || !strings.Contains(a, "--remote-debugging-pipe") || strings.Contains(a, "remote-debugging-port") {
		t.Errorf("argv: %s", a)
	}
	if !strings.Contains(strings.Join(chromiumArgs("/tmp/p", false), " "), "--no-sandbox") {
		t.Error("no fallback where the browser's own sandbox cannot run")
	}
	for _, p := range []string{"/etc/passwd", "file:///x", "/dev/a/~zz/", "", "http://x/"} {
		if err := RunCaptureBox(context.Background(), "/bin/false", "/x", p, "/tmp"); err == nil {
			t.Errorf("path %q accepted", p)
		}
	}
}

func TestPreviewConfig(t *testing.T) {
	load := func(s string) (*PreviewConfig, error) {
		p := filepath.Join(t.TempDir(), "c")
		_ = os.WriteFile(p, []byte(s), 0o644)
		return LoadPreviewConfig(p)
	}
	if c, err := LoadPreviewConfig("/nonexistent/x"); c != nil || err != nil {
		t.Errorf("missing file: %v %v", c, err)
	}
	if c, err := load("HOST=\n"); c != nil || err != nil {
		t.Errorf("empty: %v %v", c, err)
	}
	c, err := load("# x\nHOST=Preview.Test\nCHROMIUM=/opt/c\nRESERVED=foo, bar\n")
	if err != nil || c.Host != "preview.test" || c.Listen != "127.0.0.1:8891" || len(c.Reserved) != 2 {
		t.Errorf("%+v %v", c, err)
	}
	for _, bad := range []string{"HOST=a b\n", "HOST=x\nLISTEN=0.0.0.0:80\n", "HOST=x\nLISTEN=:80\n", "HOST=x\nLISTEN=example.test:80\n"} {
		if _, err := load(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// the example is off as shipped
	if c, err := LoadPreviewConfig("../../packaging/preview.conf.example"); c != nil || err != nil {
		t.Errorf("example: %v %v", c, err)
	}
}

func TestPreviewInjection(t *testing.T) {
	base := "/dev/anna/~abc/"
	out := string(injectPreview([]byte(`<!doctype html><HTML><head lang="x"><base href="/"><link href='/a.css'><script src="//cdn/x.js"></script><a href="`+base+`y">`), base))
	for _, want := range []string{`<head lang="x"><script>`, `<base href="` + base + `">`, `href='` + base + `a.css'`, `src="//cdn/x.js"`, `href="` + base + `y"`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if out := string(injectPreview([]byte("plain"), base)); !strings.HasSuffix(out, "plain") {
		t.Errorf("plain: %s", out)
	}
}

// The route script renders one rule per room, for the preview host only.
func TestPreviewRoutes(t *testing.T) {
	const script = "../../packaging/preview-routes.py"
	src, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	for n := range reservedRooms {
		if !strings.Contains(string(src), `"`+n+`"`) {
			t.Errorf("%s is not reserved in preview-routes.py", n)
		}
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	conf := filepath.Join(t.TempDir(), "c")
	render := func(text, rooms string) string {
		_ = os.WriteFile(conf, []byte(text), 0o644)
		c := exec.Command(py, script, "--print")
		c.Env = append(os.Environ(), "AIAGENT_PREVIEW_CONF="+conf, "AIAGENT_ROOMS="+rooms, "KUBECTL=/bin/false")
		out, err := c.Output()
		if err != nil {
			t.Fatalf("%v", err)
		}
		return string(out)
	}
	full := "HOST=preview.test\nGATEWAY=gw\nGATEWAY_NAMESPACE=gns\nNAMESPACE=ns\nSERVICE=svc\nPORT=80\nRESERVED=taken\n"
	if out := render(strings.Replace(full, "GATEWAY=gw", "GATEWAY=", 1), "anna"); out != "" {
		t.Errorf("off, but: %s", out)
	}
	rooms := "anna bob taken ci x../y"
	for i := 0; i < 20; i++ {
		rooms += fmt.Sprintf(" r%02d", i)
	}
	var l struct {
		Items []struct {
			Spec struct {
				Hostnames []string
				Rules     []struct {
					Matches []struct {
						Path    struct{ Type, Value string }
						Headers []struct{ Name, Type, Value string }
					}
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(render(full, rooms)), &l); err != nil || len(l.Items) != 2 {
		t.Fatalf("%v, %d routes", err, len(l.Items))
	}
	seen := map[string]bool{}
	for _, it := range l.Items {
		if len(it.Spec.Hostnames) != 0 || len(it.Spec.Rules) > 16 {
			t.Errorf("route: %+v", it)
		}
		for _, r := range it.Spec.Rules {
			m := r.Matches[0]
			if len(r.Matches) != 1 || m.Path.Type != "PathPrefix" || len(m.Headers) != 1 || m.Headers[0].Name != "Host" || m.Headers[0].Value != "preview.test" {
				t.Errorf("rule: %+v", r)
			}
			seen[m.Path.Value] = true
		}
	}
	if len(seen) != 22 || !seen["/dev/anna"] || seen["/dev/ci"] || seen["/dev/taken"] {
		t.Errorf("paths: %v", seen)
	}
}
