package web

import (
	"strings"
	"testing"
)

// The review dialog is modal, so the page banner is hidden under it: a send's
// result must be written inside the dialog, or the button looks dead.
// A stopped Claude Code session can still be sent to (the service resumes it).
func TestReviewSendShowsItsResultInTheDialog(t *testing.T) {
	app := readWebFile(t, "app.js")
	dlg := jsFunc(t, app, "openReview")
	for _, want := range []string{"sendState", "showSend(sendingText", "showSend(failText(e), 'bad')", "sentText(res)", "resumable"} {
		if !strings.Contains(dlg, want) {
			t.Errorf("openReview is missing %q", want)
		}
	}
	if strings.Contains(dlg, "sess && sess.live && sess.messageVia && open") {
		t.Errorf("sending must not require the session to be running: a stopped one is resumed")
	}
}

// A review page an agent files (aiagent review) waits in the test room; the
// Review tab and its badge must show it too, not only board tasks.
func TestReviewTabListsFiledReviewPages(t *testing.T) {
	app := readWebFile(t, "app.js")
	tab := jsFunc(t, app, "renderTaskReview")
	if !strings.Contains(tab, "openPages()") || !strings.Contains(tab, "reviewList(") {
		t.Errorf("the Review tab must list the open review pages agents filed")
	}
	if strings.Index(tab, "openPages()") > strings.Index(tab, "if (!st.tasks)") {
		t.Errorf("the review pages must show even when the task board is off")
	}
	if !strings.Contains(jsFunc(t, app, "renderNavBadges"), "openPages()") {
		t.Errorf("the Review badge must count the open review pages")
	}
}
