package web

import (
	"strings"
	"testing"
)

// A name under a big character was covered by its own feet (the sprite is
// transformed and so painted above it), by its neighbours, and cut off at the
// bottom of the room. Names are one layer over everybody, kept inside.
func TestRoomNamesAreALayerOverTheCharacters(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "renderRoomFocus"), "F.stage.append(act.tag)") {
		t.Error("the name must be a child of the stage: inside its actor a neighbour covers it")
	}
	place := jsFunc(t, app, "place")
	if !strings.Contains(place, "RH * st.focus.FS - tagH") || !strings.Contains(place, "placeTags()") {
		t.Error("place must keep the agent high enough for its name to stay in the room")
	}
	tags := jsFunc(t, app, "placeTags")
	if !strings.Contains(tags, "1492 - w / 2") || !strings.Contains(tags, "o.y - tagH") {
		t.Error("placeTags must keep names inside the sides and off each other")
	}
	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, ".tag.big { position: absolute") || strings.Contains(css, ".actor.big .tag") {
		t.Error("the big name is styled on its own, outside .actor")
	}
}
