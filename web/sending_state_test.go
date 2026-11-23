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
