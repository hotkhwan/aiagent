package web

import (
	"strings"
	"testing"
)

// The login page's QR login keeps its nonce in memory and sends it only in
// the poll's header: never in a URL, never in storage. The poll stops once the
// tab is signed in, and the token it receives goes through the same sign-in
// as a typed one (so "remember on this device" applies to it too).
func TestLoginQRKeepsItsNonceInMemory(t *testing.T) {
	app := readWebFile(t, "app.js")
	i := strings.Index(app, "async function startLoginQR () {")
	if i < 0 {
		t.Fatal("startLoginQR moved; update this test")
	}
	end := strings.Index(app[i:], "\n}\n")
	fn := app[i : i+end]
	for _, want := range []string{
		"fetch('/aiagent/login-qr', { method: 'POST'",
		"{ headers: { 'X-Aiagent-Login-Nonce': r.nonce }",
		"signIn(d.token)",
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("startLoginQR lacks %q", want)
		}
	}
	for _, never := range []string{"localStorage", "sessionStorage", "safeSet", "r.nonce +", "'?n='"} {
		if strings.Contains(fn, never) {
			t.Errorf("startLoginQR must not use %q", never)
		}
	}
	html := readWebFile(t, "index.html")
	if !strings.Contains(html, `id="loginQR"`) || !strings.Contains(html, `id="loginQRNew"`) {
		t.Error("the login card lost its QR")
	}
}

// The token form is the login page's default; a QR (a login request on the
// server) is made only when the visitor asks for one (owner, 2026-11-28).
func TestLoginQRIsMadeOnlyWhenAskedFor(t *testing.T) {
	app := readWebFile(t, "app.js")
	i := strings.Index(app, "function showLogin (msg) {")
	if i < 0 {
		t.Fatal("showLogin moved; update this test")
	}
	end := strings.Index(app[i:], "\n}\n")
	if fn := app[i : i+end]; strings.Contains(fn, "startLoginQR()") {
		t.Error("the login page makes a QR before it is asked for")
	}
	if !strings.Contains(app, "$('loginQRAsk').addEventListener('click'") {
		t.Error("nothing asks for the QR")
	}
	if html := readWebFile(t, "index.html"); !strings.Contains(html, `id="loginQRAsk"`) {
		t.Error("the login card lost its QR button")
	}
}
