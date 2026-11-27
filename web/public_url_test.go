package web

import (
	"os"
	"strings"
	"testing"
)

// Opened on 127.0.0.1, localhost or plain http, the phone dialog says first
// how to get an address the phone can reach. On an https address that is not
// loopback it is the pairing code alone, as before.
func TestThePhoneDialogSaysHowToReachItOnlyFromALoopbackPage(t *testing.T) {
	page := readWebFile(t, "index.html")
	if !strings.Contains(page, `<div id="connectReach"></div><div id="connectBody"></div>`) {
		t.Error("the phone dialog needs a place for the card above the pairing code")
	}
	app := readWebFile(t, "app.js")
	open := jsFunc(t, app, "openConnect")
	if !strings.Contains(open, "phoneCannotReach() ? [reachCard()] : []") {
		t.Error("openConnect must draw the card only when the phone cannot reach this page")
	}
	if !strings.Contains(open, "drawPair()") {
		t.Error("the pairing code stays in the dialog")
	}
	cant := jsFunc(t, app, "phoneCannotReach")
	for _, want := range []string{"location.protocol !== 'https:'", "'localhost'", "'127.0.0.1'", "'::1'", "'[::1]'"} {
		if !strings.Contains(cant, want) {
			t.Errorf("phoneCannotReach must test %s", want)
		}
	}
	card := jsFunc(t, app, "reachCard")
	for _, want := range []string{"tr('ทำให้มือถือเปิดถึงได้')", "tr('คัดลอกคำสั่ง')", "tr('ส่งให้ agent')", "copyText(cur.text"} {
		if !strings.Contains(card, want) {
			t.Errorf("reachCard is missing %s", want)
		}
	}
	if !strings.Contains(card, "QR ด้านล่างสร้างจากที่อยู่นี้") {
		t.Error("the card must say a QR made from this address will not work on the phone")
	}
	if !strings.Contains(app, "$('connectReach').replaceChildren()\n})") {
		t.Error("closing the dialog clears the card")
	}
}

// "Send to an agent" is a message the admin could have typed: the same
// /sessions/<id>/message call as the chat, confirmed first, to an agent the
// web may already message. No new endpoint, no new power.
func TestSendingTheReachInstructionsUsesTheOrdinaryMessagePath(t *testing.T) {
	app := readWebFile(t, "app.js")
	send := jsFunc(t, app, "sendReach")
	if !strings.Contains(send, "act('/sessions/' + encodeURIComponent(target.sessionId) + '/message', { message: text, sendId: newSendId() }") {
		t.Error("sendReach must send through the chat's own /sessions/<id>/message path")
	}
	if !strings.Contains(send, "confirmBox(") || !strings.Contains(send, "onceFor(") {
		t.Error("sendReach must ask before sending, and send once")
	}
	for _, no := range []string{"fetch(", "api('/", "/connect/", "/tunnel"} {
		if strings.Contains(send, no) {
			t.Errorf("sendReach must not use %s: it is an ordinary message", no)
		}
	}
	targets := jsFunc(t, app, "reachTargets")
	for _, want := range []string{"isUser()", "x.live && x.messageVia", "!x.readOnly"} {
		if !strings.Contains(targets, want) {
			t.Errorf("reachTargets must keep the message rule: missing %s", want)
		}
	}
}

// The instructions point at the playbook and carry nothing secret: the page's
// address is fine, the token and the connect code are not.
func TestTheReachInstructionsNameThePlaybookAndNoSecret(t *testing.T) {
	app := readWebFile(t, "app.js")
	ways := jsFunc(t, app, "reachWays")
	// The agent runs what the playbook says with the user's shell, so it must
	// read the copy that ships with the running version, never a moving
	// branch that anyone who can push to it could change.
	doc := jsFunc(t, app, "publicUrlDoc")
	for _, want := range []string{"'docs/PUBLIC_URL.md", "/blob/v' + ", "st.snap.version"} {
		if !strings.Contains(doc, want) {
			t.Errorf("publicUrlDoc must pin the playbook to the running version; missing %q", want)
		}
	}
	if strings.Contains(app, "blob/main/docs/PUBLIC_URL.md") {
		t.Error("the instructions point at main, which can change under the user")
	}
	if n := strings.Count(ways, "tr('ทำตาม ') + publicUrlDoc()"); n != 3 {
		t.Errorf("each of the three ways must point at docs/PUBLIC_URL.md, got %d", n)
	}
	for _, section := range []string{"หัวข้อ A", "หัวข้อ B", "หัวข้อ C"} {
		if !strings.Contains(ways, section) {
			t.Errorf("reachWays is missing %s", section)
		}
	}
	if !strings.Contains(ways, "หยุดรอให้ฉันล็อกอิน Cloudflare ในเบราว์เซอร์เอง") || !strings.Contains(ways, "ห้ามขอรหัสผ่านหรือ API token") {
		t.Error("the own-address instruction must stop for the human's Cloudflare login and forbid asking for credentials")
	}
	for _, fn := range []string{"reachWays", "reachCard", "sendReach", "reachTargets"} {
		body := jsFunc(t, app, fn)
		for _, no := range []string{"st.token", "X-Aiagent-Token", "localStorage", "connectCodeOf", "connectQR(", "/connect/pair"} {
			if strings.Contains(body, no) {
				t.Errorf("%s must not use %s: no secret goes into the instructions", fn, no)
			}
		}
	}
	en := readWebFile(t, "i18n.js")
	if !strings.Contains(en, "stop for me to log in to Cloudflare in the browser") {
		t.Error("the English instruction must keep the stop-for-login rule")
	}
}

// The playbook's two load-bearing rules: the tunnel ends in a 404 (never a
// catch-all proxy), and the agent stops for the human's browser login instead
// of handling Cloudflare credentials.
func TestThePublicURLPlaybookKeepsItsSafetyRules(t *testing.T) {
	b, err := os.ReadFile("../docs/PUBLIC_URL.md")
	if err != nil {
		t.Fatalf("docs/PUBLIC_URL.md must exist: %v", err)
	}
	doc := string(b)
	for _, want := range []string{
		// the human must know the token is then the only gate, and why
		// aiagent's own TLS rule does not cover a tunnel
		"## What protects it once it is public",
		"the aiagent token is the **only** thing",
		"Cloudflare Access",
		// the account-wide Cloudflare certificate must not stay where every
		// agent running as this user can use it
		"If `~/.cloudflared/cert.pem` was **not** there before step 1",
		"  - service: http_status:404",
		"the **last rule is `service: http_status:404`**",
		"**STOP and\n   ask the human to finish the browser login**",
		"Never ask for, accept, or type a Cloudflare password, API token",
		"**Expose only aiagent.**",
		"Never expose ssh",
		"wait for a yes",
		"header_up Cf-Connecting-Ip {remote_host}",
		"Never use\n`tailscale funnel`",
		"**Undo:**",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/PUBLIC_URL.md lost %q", want)
		}
	}
	if strings.Contains(doc, "ssh://localhost") || strings.Contains(doc, "service: ssh://") {
		t.Error("the playbook must never route ssh")
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), "[docs/PUBLIC_URL.md](docs/PUBLIC_URL.md)") {
		t.Error("README's phone app section must point at docs/PUBLIC_URL.md")
	}
}
