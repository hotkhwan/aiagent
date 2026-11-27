package web

import (
	"strings"
	"testing"
)

// The phone dialog lists the paired phones and can revoke one, after a
// confirmation, through the device routes only. It never shows or sends a
// device's token: the list carries none.
func TestThePhoneDialogListsAndRevokesPairedPhones(t *testing.T) {
	app := readWebFile(t, "app.js")
	list := jsFunc(t, app, "drawDevices")
	for _, want := range []string{
		"api('/connect/devices')",
		"'/connect/devices/' + encodeURIComponent(d.id) + '/revoke'",
		"confirmBox(",
		"d.current",
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
		t.Error("the pairing card must show the device list")
	}
	// the note under the QR says what the phone gets now
	if !strings.Contains(app, "tr('รหัสนี้ใช้ได้ครั้งเดียว ภายใน 10 นาที แอปจะได้ token ของตัวเอง สิทธิ์เท่ากับหน้านี้ เพิกถอนได้จากรายการด้านล่าง')") {
		t.Error("the note under the QR still says the app gets this page's token")
	}
}
