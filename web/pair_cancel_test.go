package web

import (
	"strings"
	"testing"
)

// Closing the phone dialog ends the pairing code it showed, so a code left in
// a photo or on another screen stops working at once instead of in ten
// minutes. The call carries no body and nothing secret beyond the usual token
// header.
func TestClosingThePhoneDialogEndsItsCode(t *testing.T) {
	app := readWebFile(t, "app.js")
	i := strings.Index(app, "$('connectDlg').addEventListener('close', () => {")
	if i < 0 {
		t.Fatal("the phone dialog's close handler moved; update this test")
	}
	end := strings.Index(app[i:], "\n})")
	if end < 0 {
		t.Fatal("cannot find the end of the close handler")
	}
	closeHandler := app[i : i+end]
	if !strings.Contains(closeHandler, "api('/connect/pair/cancel', { method: 'POST' })") {
		t.Errorf("closing the dialog must end its pairing code:\n%s", closeHandler)
	}
}
