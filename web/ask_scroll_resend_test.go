package web

import (
	"strings"
	"testing"
)

// The side panel does not scroll while it shows the chat, and a question with
// long options ran past its bottom edge: Send could not be reached (owner,
// 2026-11-26). The box keeps to part of the panel, scrolls on its own, and
// its Send row (with the note saying why Send is off) sticks to its bottom.
func TestAskBoxScrollsAndKeepsSendInSight(t *testing.T) {
	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, ".insp:has(.chatLog) { overflow: hidden }") {
		t.Skip("the panel scrolls again: revisit this test")
	}
	rule := func(sel string) string {
		i := strings.Index(css, "\n"+sel+" {")
		if i < 0 {
			t.Fatalf("styles.css has no %s rule", sel)
		}
		return css[i : i+strings.Index(css[i:], "}")]
	}
	box := rule(".insp > .promptBox")
	for _, want := range []string{"min-height: ", "max-height: ", "overflow-y: auto", "flex: 0 1 auto"} {
		if !strings.Contains(box, want) {
			t.Errorf(".insp > .promptBox lacks %q", want)
		}
	}
	btns := rule(".insp > .askBox .askBtns")
	for _, want := range []string{"position: sticky", "bottom: ", "background: "} {
		if !strings.Contains(btns, want) {
			t.Errorf(".insp > .askBox .askBtns lacks %q", want)
		}
	}
	box2 := jsFunc(t, readWebFile(t, "app.js"), "askBox")
	if !strings.Contains(box2, "tr('คุยเรื่องนี้')) : null,\n      note))") {
		t.Error("the note must sit in the sticky Send row")
	}
}

// A review reply that was typed but never confirmed may be sent again, only
// on purpose: the dialog turns the service's refusal into "send again", and a
// closed review in that state offers it too. A held message the agent later
// refused is shown as failed, not as waiting.
func TestReviewSendAgainAndFailedHeldMessages(t *testing.T) {
	app := readWebFile(t, "app.js")
	dlg := jsFunc(t, app, "openReview")
	for _, want := range []string{
		"refused.code !== 'AIAGENT_REVIEW_UNCONFIRMED'",
		"call('/decision', { send: true, resend: true })",
		"confirmBox(tr('ผลตรวจนี้ส่งไปแล้ว'), why, r.reply || '', tr('ส่งซ้ำ'))",
		"r.delivery === 'unknown' && r.sentVia !== 'queued'",
		"onceFor('review:' + r.id",
	} {
		if !strings.Contains(dlg, want) {
			t.Errorf("openReview lacks %q", want)
		}
	}
	chat := jsFunc(t, app, "chatPanel")
	if !strings.Contains(chat, "p.state === 'failed'") || !strings.Contains(chat, "serverText(p.lastError") {
		t.Error("chatPanel must show a held message the agent refused, with the reason")
	}
}
