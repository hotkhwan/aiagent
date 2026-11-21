package world

import (
	"strings"
	"testing"
	"time"
)

// TestReviewItems: per-section verdicts compose the reply, a preview never
// answers the review, and a revise without a note is refused.
func TestReviewItems(t *testing.T) {
	e := newEnv(t)
	e.now = time.Now()
	tok, _ := e.store.Enable("test")
	r, err := FileReview(e.home, "หน้า PM", "", "", "codexCli", "/w", []byte("<h2>ข้อ 1</h2><h2>ข้อ 2</h2>"), e.now)
	if err != nil {
		t.Fatal(err)
	}
	items := []ReviewItem{{Title: "ข้อ 1", Verdict: "approve"}, {Title: "ข้อ 2", Verdict: "revise", Note: "ปุ่มเล็กไป"}}

	decision, reply, err := e.col.ComposeReview(r.ID, items, "")
	if err != nil || decision != "revise" {
		t.Fatalf("compose: decision=%q err=%v", decision, err)
	}
	for _, want := range []string{"1. ข้อ 1 — ผ่าน", "2. ข้อ 2 — ต้องแก้ไข: ปุ่มเล็กไป", r.ID} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply missing %q: %s", want, reply)
		}
	}
	if got := e.col.Reviews(); len(got) != 1 || got[0].Status != "open" {
		t.Fatalf("preview answered the review: %+v", got)
	}

	if _, _, err := e.col.ComposeReview(r.ID, []ReviewItem{{Title: "ข้อ 1", Verdict: "revise"}}, ""); err == nil {
		t.Fatal("revise without a note accepted")
	}
	if _, _, err := e.col.ComposeReview(r.ID, []ReviewItem{{Title: "ข้อ 1", Verdict: "maybe"}}, ""); err == nil {
		t.Fatal("unknown verdict accepted")
	}
	if _, _, err := e.col.ComposeReview(r.ID, nil, ""); err == nil {
		t.Fatal("empty items accepted")
	}

	code, m, _ := e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/compose", tok, `{"items":[{"title":"ข้อ 1","verdict":"approve"},{"title":"ข้อ 2","verdict":"approve"}],"note":""}`)
	if code != 200 || !strings.Contains(m["details"].(map[string]any)["reply"].(string), "ตกลง ปิดงานได้") {
		t.Fatalf("compose route: %d %v", code, m)
	}

	e.svc.lastWrite = time.Time{}
	code, m, _ = e.do(t, "POST", "/aiagent/api/reviews/"+r.ID+"/decision", tok, `{"items":[{"title":"ข้อ 1","verdict":"approve"},{"title":"ข้อ 2","verdict":"revise","note":"ปุ่มเล็กไป"}],"note":"","send":false}`)
	if code != 200 || !strings.Contains(m["details"].(map[string]any)["reply"].(string), "ปุ่มเล็กไป") {
		t.Fatalf("decision with items: %d %v", code, m)
	}
	if got := e.col.Reviews(); len(got) != 1 || got[0].Status != "revise" {
		t.Fatalf("status = %+v", got)
	}
}

// TestReviewFrameScript: the page gets the verdict script before </body>, and
// a page without a body still gets it.
func TestReviewFrameScript(t *testing.T) {
	out := string(withReviewFrame([]byte("<h2>a</h2></body></html>")))
	if i, j := strings.Index(out, "<script>"), strings.Index(out, "</body>"); i < 0 || j < i {
		t.Fatalf("script not before </body>: %s", out)
	}
	if !strings.HasSuffix(string(withReviewFrame([]byte("<h2>a</h2>"))), "</script>") {
		t.Fatal("bodyless page did not get the script")
	}
}
