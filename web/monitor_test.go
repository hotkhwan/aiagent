package web

import (
	"io/fs"
	"strings"
	"testing"
)

func readWebFile(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(FS, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// The Testing room's wall screen is the room's live monitor. It must show the
// open reviews as well as the agents, or the owner cannot see what is waiting.
func TestMonitorWallListsOpenReviews(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "updateMonitor"), "liveWallCards(") {
		t.Fatalf("updateMonitor must fill the live wall through liveWallCards")
	}
	wall := jsFunc(t, app, "liveWallCards")
	if !strings.Contains(wall, "'test'") || !strings.Contains(wall, "st.reviews") {
		t.Errorf("liveWallCards must put the open reviews on the Testing room's wall")
	}
}

// The monitor composer must take a picture pasted from the clipboard, the same
// as the page composer does.
func TestMonitorComposersAcceptPastedImages(t *testing.T) {
	app := readWebFile(t, "app.js")
	for _, name := range []string{"drawPopComposer", "drawSheetComposer"} {
		fn := jsFunc(t, app, name)
		if !strings.Contains(fn, "addEventListener('paste'") || !strings.Contains(fn, "addFiles(") {
			t.Errorf("%s: a pasted image must be attached through addFiles", name)
		}
	}
}

// A picture a message carries must reach the monitor dialog, and the dialog must
// size it to be read, not the 84px chat thumbnail.
func TestMonitorShowsMessageImagesLarge(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "monitorCards"), "msgImages(") {
		t.Errorf("monitorCards must render the pictures a message carries")
	}
	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, ".spBody .msgImgs img") {
		t.Errorf("styles.css needs a size rule for message pictures inside the monitor dialog")
	}
}
