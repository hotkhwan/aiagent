package web

import (
	"strings"
	"testing"
)

// A Claude Code dialog with several questions (tabs) used to show the tab row
// as the question and buttons that answered one key at a time, so a
// multiSelect could not be answered from the web (owner, 2026-11-25). The page
// now shows every question with radios or boxes and sends them all at once.
func TestAskBoxSendsEveryAnswerOnce(t *testing.T) {
	app := readWebFile(t, "app.js")
	pb := jsFunc(t, app, "promptBox")
	if !strings.Contains(pb, "if (p.ask) return askBox(sess, p)") {
		t.Fatal("promptBox must hand an AskUserQuestion dialog to askBox")
	}
	box := jsFunc(t, app, "askBox")
	for _, want := range []string{
		"q.multi ? 'checkbox' : 'radio'",                                           // one choice vs several
		"onceFor('answer:' + sess.sessionId",                                       // one answer at a time per session
		"sendOnce(sendBtn, sendBtn, () => act(path, { ask, sendId: state.sendId }", // one press, one send
		"{ ask: { tabs, chat: true }",                                              // Chat about this, on its own
		"tr('ยืนยันส่งคำตอบ')",                                                     // the Submit tab already on screen
		"a.review ? [] : askQuestions(p)",
		"sendBtn.disabled = !!why",
	} {
		if !strings.Contains(box, want) {
			t.Errorf("askBox lacks %q", want)
		}
	}
	if strings.Contains(box, "style:") || strings.Contains(box, ".style.") {
		t.Error("askBox sets inline styles; the CSP forbids them")
	}
	// the picks are dropped when the questions change
	st := jsFunc(t, app, "askStateFor")
	if !strings.Contains(st, "st0.sig !== sig") {
		t.Error("askStateFor must start afresh for another set of questions")
	}
	css := readWebFile(t, "styles.css")
	for _, c := range []string{".askQ ", ".askOpt ", ".askText ", ".askNote:empty"} {
		if !strings.Contains(css, c) {
			t.Errorf("styles.css lacks %s", c)
		}
	}
}
