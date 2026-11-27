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

// The admin sees a 🔔 with a dot when a newer release is published, and one
// click installs it from the release's signed files.
func TestTheBellOffersANewerRelease(t *testing.T) {
	page := readWebFile(t, "index.html")
	for _, want := range []string{`id="releaseBtn" hidden`, `id="releaseDot" hidden`, `id="releaseDlg"`, `id="releaseBody"`} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html is missing %q", want)
		}
	}
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "showMenu"), "'releaseBtn'") || !strings.Contains(jsFunc(t, app, "showMenu"), "checkRelease()") {
		t.Error("the bell is shown to admins only, and checks when it is")
	}
	check := jsFunc(t, app, "checkRelease")
	if !strings.Contains(check, "api('/update/release')") || !strings.Contains(check, "$('releaseDot').hidden = !r.newer") {
		t.Error("the dot follows release.newer")
	}
	draw := jsFunc(t, app, "drawRelease")
	for _, want := range []string{"body: { release: r.latest }", "method: 'POST'", "waitForNewVersion(", "confirmBox(", "status.running", "'relNotes' }, r.notes)"} {
		if !strings.Contains(draw, want) {
			t.Errorf("drawRelease is missing %q", want)
		}
	}
	// the notes come from GitHub: text, never markup
	for _, bad := range []string{"innerHTML", "insertAdjacentHTML", "outerHTML"} {
		if strings.Contains(draw, bad) || strings.Contains(check, bad) {
			t.Errorf("release notes must not be rendered as HTML (%s)", bad)
		}
	}
	if !strings.Contains(app, "$('releaseBtn').addEventListener('click', openRelease)") {
		t.Error("clicking the bell opens the release panel")
	}
}
