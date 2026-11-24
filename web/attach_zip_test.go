package web

import (
	"strings"
	"testing"
)

// The file picker and the composer take a zip as well as a picture. A zip is
// not a picture: it is uploaded as it is (never re-encoded), drawn as a tile
// and named as a zip in the message the agent reads.
func TestComposerAttachesZip(t *testing.T) {
	page := readWebFile(t, "index.html")
	if !strings.Contains(page, `id="askFile" accept="image/png,image/jpeg,image/gif,image/webp,.zip,application/zip,application/x-zip-compressed"`) {
		t.Errorf("the file picker must offer zip archives")
	}
	app := readWebFile(t, "app.js")
	add := jsFunc(t, app, "addFiles")
	for _, want := range []string{"isZipFile(f)", "kind: 'zip'", "maxZipBytes", "uploadFile(a.blob, a.kind === 'zip')"} {
		if !strings.Contains(add, want) {
			t.Errorf("addFiles is missing %q", want)
		}
	}
	if up := jsFunc(t, app, "uploadFile"); !strings.Contains(up, "raw ? blob : await shrink(blob)") {
		t.Errorf("a zip must be uploaded as it is: shrink() re-encodes pictures")
	}
	if tile := jsFunc(t, app, "attTile"); !strings.Contains(tile, "a.kind === 'zip'") || !strings.Contains(tile, "zipTile") {
		t.Errorf("a zip attachment must be drawn as a tile, not as an <img>")
	}
	if !strings.Contains(jsFunc(t, app, "drawThumbs"), "attTile(") {
		t.Errorf("the attachment tray must draw through attTile")
	}
	lines := jsFunc(t, app, "attachLines")
	if !strings.Contains(lines, "f.kind === 'zip'") || !strings.Contains(lines, "f.path") {
		t.Errorf("attachLines must name a zip as a zip, with its path")
	}
	// two composers send: a chat to one session, and a new message or mission
	if n := strings.Count(app, "attachLines(files)"); n != 2 {
		t.Errorf("both send paths must build their attachment lines through attachLines, found %d", n)
	}
	if !strings.Contains(jsFunc(t, app, "restoreDraft"), "isZipPath(p)") {
		t.Errorf("a restored draft must not try to preview a zip as a picture")
	}
}

// The review dialog's note takes pictures too, so the reviewer can mark what
// is wrong on a screenshot. Their paths travel inside the note, which is what
// the service already sends to the agent.
func TestReviewNoteTakesPictures(t *testing.T) {
	app := readWebFile(t, "app.js")
	dlg := jsFunc(t, app, "openReview")
	for _, want := range []string{
		"revPics", "addPics(", "uploadFile(p.blob, false)", "noteText()",
		"note.addEventListener('paste'", "picsBusy()",
	} {
		if !strings.Contains(dlg, want) {
			t.Errorf("openReview is missing %q", want)
		}
	}
	if strings.Contains(dlg, "note: note.value") {
		t.Errorf("the note sent to the service must be noteText(), which carries the pictures")
	}
	if !strings.Contains(readWebFile(t, "styles.css"), ".revPics") {
		t.Errorf("the review pictures need their styles")
	}
}
