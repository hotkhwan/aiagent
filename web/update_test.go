package web

import (
	"strings"
	"testing"
)

// The admin can see whether a newer aiagent is out and install it from the
// page; the page reloads itself once the service comes back on the new version.
func TestTheServiceCanBeUpdatedFromThePage(t *testing.T) {
	page := readWebFile(t, "index.html")
	if !strings.Contains(page, `id="updateBody"`) {
		t.Error("the accounts dialog needs an update section")
	}
	app := readWebFile(t, "app.js")
	draw := jsFunc(t, app, "drawUpdate")
	for _, want := range []string{"api('/update", "confirmBox(", "method: 'POST'", "canUpdate", "commits", "status.running"} {
		if !strings.Contains(draw, want) {
			t.Errorf("drawUpdate is missing %q", want)
		}
	}
	if !strings.Contains(jsFunc(t, app, "waitForNewVersion"), "location.reload()") {
		t.Error("the page must reload when the new version answers")
	}
	if !strings.Contains(jsFunc(t, app, "openAccounts"), "drawUpdate()") {
		t.Error("opening the accounts dialog checks for an update")
	}
}
