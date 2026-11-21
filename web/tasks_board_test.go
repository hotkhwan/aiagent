package web

import (
	"strings"
	"testing"
)

// A long board must not run on forever: the owner filters to the tasks still
// in progress or to the finished ones, and pages through each list.
func TestTaskBoardFiltersAndPages(t *testing.T) {
	app := readWebFile(t, "app.js")
	render := jsFunc(t, app, "renderTasks")
	if !strings.Contains(render, "filterTasks(") || !strings.Contains(render, "pageTasks(") {
		t.Fatalf("renderTasks must filter the tasks and page through them")
	}
	if !strings.Contains(render, "[10, 20, 50]") {
		t.Errorf("the board needs a per-page choice of 10, 20 or 50")
	}
	filter := jsFunc(t, app, "filterTasks")
	for _, want := range []string{"openStatuses", "'completed'", "'cancelled'"} {
		if !strings.Contains(filter, want) {
			t.Errorf("filterTasks must pick %s", want)
		}
	}
	if !strings.Contains(jsFunc(t, app, "pageTasks"), "slice(") {
		t.Errorf("pageTasks must cut one page out of the list")
	}
}

// A cancelled task is shown in the lane, so the lane is where its delete
// button belongs: the owner could not find it behind the detail panel.
func TestCancelledTaskDeleteIsOnItsLaneCard(t *testing.T) {
	app := readWebFile(t, "app.js")
	if !strings.Contains(jsFunc(t, app, "renderTasks"), "deleteTask(") {
		t.Errorf("renderTasks must put a delete button on a cancelled card in the lane")
	}
}

// Every card is the same size so the board does not look ragged, and the
// delete badge sits on the card's top-right corner.
func TestTaskCardsAreEqualAndDeleteIsABadge(t *testing.T) {
	app := readWebFile(t, "app.js")
	css := readWebFile(t, "styles.css")
	if !strings.Contains(jsFunc(t, app, "renderTasks"), "tdelBadge") {
		t.Errorf("the cancelled card's delete must be a tdelBadge on the card")
	}
	rule := func(sel string) string {
		i := strings.Index(css, sel+" {")
		if i < 0 {
			t.Fatalf("styles.css has no %s rule", sel)
		}
		return css[i : i+strings.Index(css[i:], "}")]
	}
	if !strings.Contains(rule(".tcard"), "height: 128px") {
		t.Errorf(".tcard needs one fixed height so every card is the same size")
	}
	if !strings.Contains(rule(".ttitle"), "line-clamp: 2") {
		t.Errorf(".ttitle must clamp to two lines so a long title cannot stretch its card")
	}
	if !strings.Contains(rule(".tdelBadge"), "position: absolute") {
		t.Errorf(".tdelBadge must sit over the card's top-right corner")
	}
}
