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
