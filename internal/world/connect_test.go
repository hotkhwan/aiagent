package world

import (
	"bytes"
	"image/png"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestConnectCodeCarriesTheOriginAndToken(t *testing.T) {
	code, err := connectCode("https://aiagent.joinanny.com", "kaw_x")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(code)
	if u.Scheme != "aiagent" || u.Host != "connect" || u.Query().Get("u") != "https://aiagent.joinanny.com" || u.Query().Get("t") != "kaw_x" {
		t.Fatalf("code = %s", code)
	}
	for _, bad := range []string{"http://aiagent.joinanny.com", "https://u:p@a.example", "javascript:alert(1)", "", "https://"} {
		if _, err := connectCode(bad, "kaw_x"); err == nil {
			t.Errorf("origin %q accepted", bad)
		}
	}
	// the path and query of the page are not part of the server address
	code, _ = connectCode("https://aiagent.joinanny.com/aiagent/?x=1", "kaw_x")
	if u, _ := url.Parse(code); u.Query().Get("u") != "https://aiagent.joinanny.com" {
		t.Fatalf("origin not reduced: %s", code)
	}
}

func qrRequest(e *env, token, body string) (int, string, []byte) {
	req := httptest.NewRequest("POST", "/aiagent/api/connect/qr", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		return 0, "", nil
	}
	var b bytes.Buffer
	_, _ = b.ReadFrom(resp.Body)
	return resp.StatusCode, resp.Header.Get("Cache-Control") + "|" + resp.Header.Get("Content-Type"), b.Bytes()
}

func TestConnectQR(t *testing.T) {
	e := newEnv(t)
	admin, _ := e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	_, other, _ := e.store.AddUser("other", "")

	// anyone gets their own token's code
	code, head, b := qrRequest(e, user, `{"origin":"https://aiagent.joinanny.com"}`)
	if code != 200 || !strings.Contains(head, "no-store") || !strings.Contains(head, "image/png") {
		t.Fatalf("own code: %d %s", code, head)
	}
	if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Fatalf("not a png: %v", err)
	}
	// an admin gets the code of a token they just made
	if code, _, _ := qrRequest(e, admin, `{"origin":"https://aiagent.joinanny.com","token":"`+user+`"}`); code != 200 {
		t.Fatalf("admin for a user: %d", code)
	}
	// a user never gets another token's code
	if code, _, _ := qrRequest(e, user, `{"origin":"https://aiagent.joinanny.com","token":"`+other+`"}`); code != 403 {
		t.Fatalf("user for another: %d", code)
	}
	// a token this server does not know is not drawn
	if code, _, _ := qrRequest(e, admin, `{"origin":"https://aiagent.joinanny.com","token":"kaw_`+strings.Repeat("Z", 43)+`"}`); code != 400 {
		t.Fatalf("unknown token: %d", code)
	}
	if code, _, _ := qrRequest(e, user, `{"origin":"http://aiagent.joinanny.com"}`); code != 400 {
		t.Fatalf("http origin: %d", code)
	}
	if code, _, _ := qrRequest(e, "", `{"origin":"https://aiagent.joinanny.com"}`); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	// the token never reaches the audit log
	if b, _ := os.ReadFile(e.auditFP); bytes.Contains(b, []byte(user)) {
		t.Fatal("a token was written to the audit log")
	}
}
