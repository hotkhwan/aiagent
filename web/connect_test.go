package web

import (
	"strings"
	"testing"
)

// An admin gets the connect code of a token they just made (the header's own
// phone dialog uses pairing codes instead, below). The QR is drawn by this
// service: the code holds a token, so no outside QR service may see it.
func TestThePhoneAppCanBeConnectedFromThePage(t *testing.T) {
	page := readWebFile(t, "index.html")
	for _, want := range []string{`id="connectBtn"`, `id="connectDlg"`, `id="connectBody"`} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html is missing %s", want)
		}
	}
	app := readWebFile(t, "app.js")
	qr := jsFunc(t, app, "connectQR")
	if !strings.Contains(qr, "'/aiagent/api/connect/qr'") || !strings.Contains(qr, "location.origin") || !strings.Contains(qr, "cache: 'no-store'") {
		t.Error("connectQR must ask this service for the QR, with the page's origin")
	}
	if strings.Contains(app, "api.qrserver.com") || strings.Contains(app, "chart.googleapis.com") {
		t.Error("a token must never go to an outside QR service")
	}
	if !strings.Contains(jsFunc(t, app, "drawTokens"), "connectQR(fresh.token)") {
		t.Error("a freshly made token shows its connect code")
	}
	if !strings.Contains(readWebFile(t, "styles.css"), "dialog.screenPop.connectDlg, dialog.screenPop.connectDlg.full { width: min(460px") {
		t.Error("the phone-app dialog is a small card in the middle, not a full-width panel")
	}
	if !strings.Contains(app, "$('connectDlg').addEventListener('close'") {
		t.Error("closing the dialog must clear the code from the page")
	}
}

// The header's phone dialog shows a one-time pairing code, never the
// long-lived token: a photo of the QR is useless once used or after ten
// minutes. It can be saved as an image for a phone that cannot scan the
// screen, and it says when it runs out.
func TestThePhoneDialogShowsAOneTimePairingCode(t *testing.T) {
	app := readWebFile(t, "app.js")
	open := jsFunc(t, app, "openConnect")
	if strings.Contains(open, "connectQR(") || strings.Contains(open, "connectCard(") {
		t.Error("the phone dialog must not draw the long-lived connect code")
	}
	if !strings.Contains(open, "drawPair(") {
		t.Error("the phone dialog draws a pairing code")
	}
	pair := jsFunc(t, app, "drawPair")
	for _, want := range []string{"api('/connect/pair'", "origin: location.origin", "download: 'aiagent-pair.png'", "expiresAt", "tr('รหัสใหม่')", "tr('บันทึกภาพ')"} {
		if !strings.Contains(pair, want) {
			t.Errorf("drawPair is missing %s", want)
		}
	}
	for _, no := range []string{"connectCodeOf", "copyText", "st.token", "connectQR(", "connectCard("} {
		if strings.Contains(pair, no) || strings.Contains(open, no) {
			t.Errorf("the phone dialog must not use %s: the token stays out of it", no)
		}
	}
	if !strings.Contains(app, "clearInterval(st.pairTimer)") {
		t.Error("the countdown stops when the dialog closes")
	}
	if !strings.Contains(readWebFile(t, "styles.css"), ".connectQR.expired") {
		t.Error("an expired code is greyed out")
	}
}
