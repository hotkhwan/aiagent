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

// After "ส่งให้ agent" succeeds the review popup closes and the session that
// filed the review opens on its chat tab, so the owner sees the message that
// went in. A failed send keeps the popup open with the error.
func TestReviewSendClosesAndShowsTheSession(t *testing.T) {
	app := readWebFile(t, "app.js")
	dlg := jsFunc(t, app, "openReview")
	if n := strings.Count(dlg, "if (sendIt) sentToSession(r)"); n != 2 {
		t.Errorf("both send paths (items and legacy) must hand off to sentToSession, found %d", n)
	}
	for _, path := range []string{"const finish = async", "const decideLegacy = async"} {
		i := strings.Index(dlg, path)
		if i < 0 {
			t.Fatalf("openReview has no %q", path)
		}
		body := dlg[i:]
		ok, bad := strings.Index(body, "sentToSession(r)"), strings.Index(body, "catch (e)")
		if ok < 0 || bad < 0 || ok > bad {
			t.Errorf("%s: the hand-off must be on the success path, before the catch", path)
		}
	}
	show := jsFunc(t, app, "sentToSession")
	for _, want := range []string{"$('reviewDlg').close()", "st.tab = 'chat'", "select('session'", "updateChat(true)", "sessOfReview(r)"} {
		if !strings.Contains(show, want) {
			t.Errorf("sentToSession is missing %q", want)
		}
	}
}

// The review dialog shows the change the page describes next to the page, and
// opens the room's running preview: a reviewer checks the real code and the
// real page, not only the agent's summary of them.
func TestReviewDialogHasCodeTabAndPreview(t *testing.T) {
	html := readWebFile(t, "index.html")
	for _, id := range []string{`id="rvTabPage"`, `id="rvTabCode"`, `id="rvPreview"`, `id="reviewCode"`} {
		if !strings.Contains(html, id) {
			t.Errorf("index.html is missing %s", id)
		}
	}
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "openReview"), "reviewTabs(r)") {
		t.Errorf("openReview must set up the page/code tabs")
	}
	load := jsFunc(t, app, "loadReviewCode")
	for _, want := range []string{"'/code'", "reviewFiles(v, box)", "reviewDiff(v)", "d.branchHead"} {
		if !strings.Contains(load, want) {
			t.Errorf("loadReviewCode is missing %q", want)
		}
	}
	if !strings.Contains(jsFunc(t, app, "reviewTabs"), "openPreview(room)") {
		t.Errorf("the review dialog must open the room's preview")
	}
	if !strings.Contains(jsFunc(t, app, "reviewRoom"), "st.me.preview") {
		t.Errorf("the preview button must only show when previews are on")
	}
}

// A room's session panel opens that room's preview: the admin should not
// have to "enter" the room or go to the token list to see the running page.
func TestSessionPanelOpensRoomPreview(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "sessionPanel"), "openPreview(sessPreviewRoom(sess))") {
		t.Errorf("the session panel must offer the room's preview")
	}
	room := jsFunc(t, app, "sessPreviewRoom")
	for _, want := range []string{"st.me.preview", "'root'"} {
		if !strings.Contains(room, want) {
			t.Errorf("sessPreviewRoom is missing %q", want)
		}
	}
}

// A pre-opened preview tab that stays blank (the browser did not navigate it)
// must not leave the user with nothing: the same link is offered to click.
func TestPreviewFallsBackToALink(t *testing.T) {
	app := readWebFile(t, "app.js")
	open := jsFunc(t, app, "openPreview")
	for _, want := range []string{"'about:blank'", "previewLink(room, to)", "!w.closed"} {
		if !strings.Contains(open, want) {
			t.Errorf("openPreview is missing %q", want)
		}
	}
	link := jsFunc(t, app, "previewLink")
	if !strings.Contains(link, "rel: 'noopener noreferrer'") || !strings.Contains(link, "target: '_blank'") {
		t.Errorf("the fallback link must open a new tab without an opener or referrer")
	}
}
