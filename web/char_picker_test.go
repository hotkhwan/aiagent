package web

import (
	"strings"
	"testing"
)

// The picker stays open after a pick so the owner sees the frame move to the
// character chosen; only ✕ closes it. The choice must apply even when the agent
// has no actor in the view that is open (the village is not drawn yet).
func TestPickingACharacterMovesTheFrameAndKeepsThePickerOpen(t *testing.T) {
	app := readWebFile(t, "app.js")
	pick := jsFunc(t, app, "markCharOption")
	if !strings.Contains(pick, "classList.toggle('on'") || !strings.Contains(pick, "aria-pressed") {
		t.Errorf("markCharOption must move the selected frame and aria-pressed to the chosen character")
	}
	if !strings.Contains(jsFunc(t, app, "pickChar"), "chooseChar(pickingFor, c)") {
		t.Errorf("pickChar must apply the chosen character")
	}
	if strings.Contains(jsFunc(t, app, "openCharPicker"), "close()") {
		t.Errorf("a pick must not close the picker; only ✕ does")
	}
	if strings.Contains(app, "charReset').addEventListener('click', () => { $('charPicker').close()") {
		t.Errorf("the reset button must not close the picker either")
	}
	if !strings.Contains(jsFunc(t, app, "chooseChar"), "st.actors && st.actors[id]") {
		t.Errorf("chooseChar must not read st.actors when no village is drawn yet")
	}
}

// The panel header is rebuilt only when its key changes, so the key must carry
// the character or a new pick waits until the agent is opened again.
func TestInspectorRebuildsHeaderWhenCharacterChanges(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "renderInspector"), "charFor(sess.sessionId)") {
		t.Errorf("renderInspector's key must include the character")
	}
}

// A character picked before looks were saved on the server lives only in that
// browser's localStorage, so the phone app showed another one. The web saves
// such a pick on the server once, and only for an agent it may restyle.
func TestABrowserOnlyPickIsSavedOnTheServerOnce(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "charFor"), "liftLook(id, pick)") {
		t.Fatalf("charFor must hand a browser-only pick to liftLook")
	}
	lift := jsFunc(t, app, "liftLook")
	for _, want := range []string{"lifted.has(id)", "own.look", "own.readOnly", "own.protected && isUser()", "/look'"} {
		if !strings.Contains(lift, want) {
			t.Errorf("liftLook must check %q", want)
		}
	}
}

// A still character is a whole figure; the avatar must crop to the head the way
// the walking sprites do, not fit the whole figure into the circle.
func TestStillCharacterAvatarShowsTheHead(t *testing.T) {
	app := readWebFile(t, "app.js")
	face := jsFunc(t, app, "avatarFace")
	if strings.Contains(face, "'contain'") {
		t.Errorf("avatarFace must zoom a still character to its head, not fit the whole figure")
	}
}
