package web

import (
	"strings"
	"testing"
)

// Picking a character must always close the picker and apply the choice, even
// when the agent has no actor in the view that is open (the village is not
// drawn yet, or only the room focus is). A throw here leaves the picker open
// and the new character unshown.
func TestPickingACharacterClosesThePickerFirst(t *testing.T) {
	app := readWebFile(t, "app.js")
	open := jsFunc(t, app, "openCharPicker")
	close := strings.Index(open, "$('charPicker').close()")
	apply := strings.Index(open, "chooseChar(pickingFor, c)")
	if close < 0 || apply < 0 || close > apply {
		t.Errorf("openCharPicker must close the picker before it applies the character")
	}
	if !strings.Contains(jsFunc(t, app, "chooseChar"), "st.actors && st.actors[id]") {
		t.Errorf("chooseChar must not read st.actors when no village is drawn yet")
	}
}
