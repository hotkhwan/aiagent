package web

import (
	"strings"
	"testing"
)

// Everyone signed in can connect the phone app with their own token, and an
// admin gets the code of a token they just made. The QR is drawn by this
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
	if !strings.Contains(jsFunc(t, app, "openConnect"), "connectQR()") {
		t.Error("the dialog shows the holder's own code")
	}
	if !strings.Contains(jsFunc(t, app, "drawTokens"), "connectQR(fresh.token)") {
		t.Error("a freshly made token shows its connect code")
	}
	if !strings.Contains(app, "$('connectDlg').addEventListener('close'") {
		t.Error("closing the dialog must clear the code from the page")
	}
}
