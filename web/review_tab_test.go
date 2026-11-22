package web

import (
	"strings"
	"testing"
)

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
