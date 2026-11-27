package world

import (
	"strings"
	"testing"
)

// TestReviewPagesAreAlwaysLight: an agent's dark-mode rules never apply, in
// the web page or the app, so half-done dark styles cannot draw white on white.
func TestReviewPagesAreAlwaysLight(t *testing.T) {
	page := []byte(`<html><head><style>code{color:#222} @media (prefers-color-scheme: dark){code{color:#fff}} @media (PREFERS-COLOR-SCHEME:DARK){p{color:#fff}}</style></head><body><p>x</p></body></html>`)
	out := string(withReviewFrame(page))
	if strings.Contains(strings.ToLower(out), "prefers-color-scheme: dark") || strings.Contains(strings.ToLower(out), "prefers-color-scheme:dark") {
		t.Fatalf("a dark query survived: %s", out)
	}
	if !strings.Contains(out, `<head><meta name="color-scheme" content="light">`) {
		t.Fatalf("the page does not say it is light: %s", out)
	}
	if !strings.Contains(out, "aiagentReview") {
		t.Fatal("the verdict script is gone")
	}
	// a page with no head still gets it
	if out := string(withReviewFrame([]byte(`<p>x</p>`))); !strings.HasPrefix(out, `<meta name="color-scheme" content="light">`) {
		t.Fatalf("no head: %s", out)
	}
}
