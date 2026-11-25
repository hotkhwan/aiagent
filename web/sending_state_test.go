package web

import (
	"strings"
	"testing"
)

// A send waits for the server, and the server waits up to ~8 s for the
// message to show in the transcript. The chat showed nothing meanwhile, so a
// send looked stuck (owner, 2026-11-23: "ส่งแล้วช้ากว่าจะขึ้น chat มันทำให้ดู
// เหมือนค้างๆ"). The message must appear at once, marked as sending, and stay
// through the chat's own refreshes until the send has answered.
func TestChatShowsAMessageWhileItIsSending(t *testing.T) {
	app := readWebFile(t, "app.js")

	send := jsFunc(t, app, "sendChat")
	mark := strings.Index(send, "st.sending = {")
	draw := strings.Index(send, "drawSending()")
	call := strings.Index(send, "await act(")
	if mark < 0 || draw < 0 || call < 0 || mark > call || draw > call {
		t.Errorf("sendChat must record st.sending and drawSending() before it awaits the send")
	}
	if !strings.Contains(send[call:], "st.sending = null") {
		t.Errorf("sendChat must clear st.sending once the send has answered")
	}

	if !strings.Contains(jsFunc(t, app, "updateChat"), "drawSending()") {
		t.Errorf("updateChat rebuilds the log; it must draw the sending message again or a refresh removes it")
	}

	draw2 := jsFunc(t, app, "drawSending")
	if !strings.Contains(draw2, "sending") || !strings.Contains(draw2, "st.sel.id") {
		t.Errorf("drawSending must draw only in the chat of the session being sent to")
	}

	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, ".msg.sending") {
		t.Errorf("styles.css must style .msg.sending so it reads as not yet sent")
	}
}

// A message nobody saw arrive must not look like a delivered one.
func TestAnUnconfirmedMessageStaysMarked(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "act"), "=== 'unknown'") {
		t.Errorf("act must show an unconfirmed send as a warning, whatever page sent it")
	}
	if !strings.Contains(jsFunc(t, app, "drawSending"), "s.delivery === 'unknown'") {
		t.Errorf("drawSending must mark an unconfirmed message")
	}
	if !strings.Contains(jsFunc(t, app, "sendChat"), "mine.delivery !== 'unknown'") {
		t.Errorf("sendChat must not clear an unconfirmed message from the chat")
	}
	if !strings.Contains(jsFunc(t, app, "updateChat"), "settleSending(") {
		t.Errorf("updateChat must clear the mark once the transcript shows the message")
	}
}

// A long paste is a token in the box; a draft kept without its text was sent
// after a reload as "[Pasted text #1 +40 lines]" and nothing else.
func TestDraftKeepsWhatAPasteTokenStandsFor(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "saveDraft"), "pastes") || !strings.Contains(jsFunc(t, app, "restoreDraft"), "st.pastes = d.pastes") {
		t.Errorf("the draft must keep the pasted text behind each token")
	}
	i := strings.Index(app, "ta.setRangeText(tok")
	if i < 0 || !strings.Contains(app[i:i+120], "saveDraft()") {
		t.Errorf("setRangeText fires no input event: the paste handler must save the draft")
	}
}
