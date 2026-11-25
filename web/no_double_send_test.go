package web

import (
	"regexp"
	"strings"
	"testing"
)

// The Send button kept working while a send waited for the agent, and a long
// message reached an agent twice in the same minute (owner, 2026-11-25). Every
// composer locks from the press until the service answers.
func TestEveryComposerSendsOnceAtATime(t *testing.T) {
	app := readWebFile(t, "app.js")

	once := jsFunc(t, app, "sendOnce")
	guard := strings.Index(once, "if (sendingIn.has(ta)) return false")
	mark := strings.Index(once, "sendingIn.add(ta)")
	call := strings.Index(once, "await fn()")
	if guard < 0 || mark < 0 || call < 0 || guard > mark || mark > call {
		t.Errorf("sendOnce must refuse a second send and mark the composer busy before it awaits the send")
	}
	if !strings.Contains(once, "btn.disabled = true") || !strings.Contains(once, "tr('กำลังส่ง…')") {
		t.Errorf("sendOnce must disable the button and show that it is sending")
	}
	if !strings.Contains(once, "finally") || !strings.Contains(once[strings.Index(once, "finally"):], "sendingIn.delete(ta)") {
		t.Errorf("sendOnce must unlock the composer however the send ends")
	}

	// each composer's button and Enter go through sendOnce
	sub := app[strings.Index(app, "$('ask').addEventListener('submit'"):]
	if !strings.HasPrefix(sub[strings.Index(sub, "\n")+1:], "  e.preventDefault()\n  sendOnce($('askText'), $('askGo'), sendFromComposer)") {
		t.Errorf("the page composer's submit must send through sendOnce")
	}
	for _, fn := range []string{"drawPopComposer", "drawSheetComposer"} {
		body := jsFunc(t, app, fn)
		if !strings.Contains(body, "sendOnce(ta, go, () => sendChat(") {
			t.Errorf("%s must send through sendOnce with its own button", fn)
		}
		if strings.Contains(body, "onclick: send") && !strings.Contains(body, "go.addEventListener('click', send)") {
			t.Errorf("%s: the Send button must be the one sendOnce disables", fn)
		}
	}
	// nothing else calls sendChat directly
	if n := strings.Count(app, "sendChat("); n != 4 { // 2 in sendFromComposer, 2 composers
		t.Errorf("sendChat( appears %d times; a new caller must go through sendOnce", n)
	}

	// a press on Send that blurs the box collapses the bar under the pointer
	// and is lost; the button keeps the focus where it is
	if !strings.Contains(app, "$('askGo').addEventListener('mousedown', e => e.preventDefault())") {
		t.Errorf("Send must not take the focus from the box on mousedown, or the first press is lost")
	}

	// a refresh must not re-enable the page's Send button mid-send
	hint := jsFunc(t, app, "updateComposerHint")
	if strings.Count(hint, "$('askGo').disabled = sendingIn.has($('askText'))") != 2 {
		t.Errorf("updateComposerHint runs on every poll; it must keep Send disabled while a send is going")
	}

	// a held-down Enter repeats keydown: only the first one sends
	for _, re := range []string{
		`if \(e\.key === 'Enter' && !e\.shiftKey && !e\.isComposing\) \{ e\.preventDefault\(\); if \(!e\.repeat\)`,
		`if \(e\.key === 'Enter' && !e\.shiftKey && !isPhone\(\)\) \{ e\.preventDefault\(\); if \(!e\.repeat\) send\(\) \}`,
	} {
		if !regexp.MustCompile(re).MatchString(app) {
			t.Errorf("an Enter handler sends on key repeat: %s", re)
		}
	}

	// the answer buttons are one at a time per session or run
	if !strings.Contains(jsFunc(t, app, "promptBox"), "onceFor('answer:'") || !strings.Contains(jsFunc(t, app, "doAnswer"), "onceFor('run:'") {
		t.Errorf("answering an agent's question must be guarded by onceFor")
	}
	of := jsFunc(t, app, "onceFor")
	if !strings.Contains(of, "if (sendingKeys.has(key)) return false") || !strings.Contains(of, "sendingKeys.delete(key)") {
		t.Errorf("onceFor must refuse a second press and release the key afterwards")
	}
}

// Every send carries a sendId, kept for the same draft and dropped once it has
// gone, so the service can answer a repeat with the first result.
func TestEverySendCarriesASendID(t *testing.T) {
	app := readWebFile(t, "app.js")
	sends := regexp.MustCompile(`act\('(/sessions/' \+ encodeURIComponent\([a-zA-Z.]+\) \+ '/(message|answer)|/messages|/missions|/runs/' \+ encodeURIComponent\(run\.id\) \+ '/answer|/pm/messages)'[^\n]*`).FindAllString(app, -1)
	if len(sends) < 6 {
		t.Fatalf("found %d sends; the pattern no longer matches app.js", len(sends))
	}
	for _, s := range sends {
		if !strings.Contains(s, "sendId") {
			t.Errorf("a send without a sendId: %s", s)
		}
	}
	if !strings.Contains(app, "act('/missions', prov ? { provider: prov, task: text, sendId } : { workspace: ws, task: text, sendId }") {
		t.Errorf("both mission forms must carry the sendId")
	}
	chat := jsFunc(t, app, "sendChat")
	ok := strings.Index(chat, "if (ok) {")
	if ok < 0 || !strings.Contains(chat, "sendId: draftSendId(ta, ") || strings.Index(chat, "forgetSendId(ta)") < ok {
		t.Errorf("sendChat must reuse the draft's sendId and forget it only after a success")
	}
	if strings.Index(chat, "clearDraft()") < ok {
		t.Errorf("the draft is cleared only once the send succeeded")
	}
	d := jsFunc(t, app, "draftSendId")
	if !strings.Contains(d, "d.key === key) return d.id") || !strings.Contains(d, "newSendId()") {
		t.Errorf("draftSendId must keep one id per draft")
	}
}

// A refused duplicate is explained and can still be sent on purpose.
func TestARefusedDuplicateCanBeSentAnyway(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "api"), "err.code = body && body.code") {
		t.Errorf("api must keep the service's error code")
	}
	a := jsFunc(t, app, "act")
	i := strings.Index(a, "e.code !== 'AIAGENT_DUPLICATE_SEND'")
	if i < 0 || !strings.Contains(a[i:], "confirmBox(") || !strings.Contains(a[i:], "{ resend: true }") {
		t.Errorf("act must ask before resending a refused duplicate, then send it with resend: true")
	}
	if !strings.Contains(a, "body.resend) throw e") {
		t.Errorf("act must not loop on a refused resend")
	}
	if !strings.Contains(a, "res.replayed") {
		t.Errorf("act must say when the service answered a repeat without typing it")
	}
	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, "button.pri.sending") {
		t.Errorf("styles.css must style a sending button")
	}
}
