package web

import (
	"strings"
	"testing"
)

// Typing mode (the chat takes the window while the composer is in use) used
// to end on the composer's blur. Pressing on a message to drag-select it for
// copying blurs the composer, so the window shrank back under the pointer and
// nothing could be copied. Blur must leave typing mode alone; a close button
// in the panel's top-right corner, or picking a view, ends it instead.
func TestTypingModeSurvivesSelectingChatText(t *testing.T) {
	app := readWebFile(t, "app.js")
	start := strings.Index(app, "$('askText').addEventListener('blur'")
	if start < 0 {
		t.Fatalf("app.js has no blur handler on the composer — update this test with the change")
	}
	blur := app[start:]
	if end := strings.Index(blur, "\n$("); end >= 0 {
		blur = blur[:end]
	}
	if strings.Contains(blur, "setTyping(false)") {
		t.Errorf("the composer's blur must not end typing mode, or a drag to select chat text shrinks the window under it:\n%s", blur)
	}

	page := readWebFile(t, "index.html")
	if !strings.Contains(page, `id="typingClose"`) {
		t.Errorf("index.html needs the typing-mode close button (#typingClose)")
	}
	at := strings.Index(app, "$('typingClose').addEventListener('click'")
	if at < 0 {
		t.Fatalf("the close button has no click handler")
	}
	click := app[at:]
	if end := strings.Index(click, "\n})"); end >= 0 {
		click = click[:end]
	}
	if !strings.Contains(click, "setTyping(false)") {
		t.Errorf("the close button must end typing mode:\n%s", click)
	}

	css := readWebFile(t, "styles.css")
	if !strings.Contains(css, ".typingClose { display: none }") || !strings.Contains(css, "body.typing .typingClose {") {
		t.Errorf("the close button must show only in typing mode")
	}

	if !strings.Contains(jsFunc(t, app, "goView"), "setTyping(false)") {
		t.Errorf("goView must end typing mode, or the view picked from the header opens behind the hidden stage")
	}
}
