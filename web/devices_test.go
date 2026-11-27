package web

import (
	"strings"
	"testing"
)

// The phone dialog shows a button with the number of paired phones; the list
// itself opens in its own popup, so the pairing dialog keeps to one screen.
// The popup revokes a phone after a confirmation, through the device routes
// only, and never shows or sends a device's token: the list carries none.
func TestThePhoneDialogListsAndRevokesPairedPhones(t *testing.T) {
	app := readWebFile(t, "app.js")
	list := jsFunc(t, app, "drawDevices")
	for _, want := range []string{
		"api('/connect/devices')",
		"'/connect/devices/' + encodeURIComponent(d.id) + '/revoke'",
		"confirmBox(",
		"d.current",
		"$('devicesDlg').showModal()",
		"$('devicesBody')",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("drawDevices is missing %q", want)
		}
	}
	for _, no := range []string{"st.token", "localStorage", ".hash"} {
		if strings.Contains(list, no) {
			t.Errorf("drawDevices must not touch %s", no)
		}
	}
	if !strings.Contains(jsFunc(t, app, "drawPair"), "drawDevices(") {
		t.Error("the pairing card must show the paired-phones button")
	}
	page := readWebFile(t, "index.html")
	if !strings.Contains(page, `<dialog id="devicesDlg"`) || !strings.Contains(page, `<div class="spBody" id="devicesBody"></div>`) {
		t.Error("index.html needs the paired-phones popup")
	}
	// the note under the QR says what the phone gets now
	if !strings.Contains(app, "tr('รหัสนี้ใช้ได้ครั้งเดียว ภายใน 10 นาที แอปจะได้ token ของตัวเอง สิทธิ์เท่ากับหน้านี้ เพิกถอนได้จากรายการด้านล่าง')") &&
		!strings.Contains(app, "tr('รหัสนี้ใช้ได้ครั้งเดียว ภายใน 10 นาที แอปจะได้ token ของตัวเอง สิทธิ์เท่ากับหน้านี้ เพิกถอนได้จากปุ่มอุปกรณ์ที่จับคู่')") {
		t.Error("the note under the QR still says the app gets this page's token")
	}
}

// With the reach card shown (a page the phone cannot open), a wide screen
// lays the dialog out in two columns so it fits without scrolling.
func TestThePhoneDialogUsesTwoColumnsOnAWideScreen(t *testing.T) {
	css := readWebFile(t, "styles.css")
	for _, want := range []string{
		"dialog.screenPop.connectDlg.wide",
		".connectDlg.wide .spBody { display: grid; grid-template-columns:",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("styles.css is missing %q", want)
		}
	}
	app := readWebFile(t, "app.js")
	if !strings.Contains(app, "$('connectDlg').classList.toggle('wide', phoneCannotReach())") {
		t.Error("the dialog must switch to two columns when the reach card is shown")
	}
}
