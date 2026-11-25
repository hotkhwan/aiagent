package web

import (
	"strings"
	"testing"
)

// .top clips what does not fit, so on a phone the account, language and help
// buttons were simply gone. There the tabs take a row of their own.
func TestPhoneHeaderKeepsItsButtons(t *testing.T) {
	css := readWebFile(t, "styles.css")
	i := strings.Index(css, "a narrow header ----------")
	if i < 0 {
		t.Fatal("the narrow header block is gone")
	}
	block := css[i:]
	phone := block[strings.Index(block, "@media (max-width: 700px)"):]
	if !strings.Contains(phone, ".top .views { order: 10; flex: 1 0 100%") {
		t.Error("the tabs must take their own row on a phone")
	}
	if !strings.Contains(block, "@media (max-width: 900px) {\n  .top { flex-wrap: wrap }") {
		t.Error("a narrow header must be allowed to wrap, or it clips")
	}
}
