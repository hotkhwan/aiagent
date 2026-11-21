package web

import (
	"strings"
	"testing"
)

// A picture from the monitor is often smaller than the screen. CSS only
// shrinks an image, so the lightbox must size it to the viewport itself.
func TestLightboxFitsPictureToViewport(t *testing.T) {
	app := readWebFile(t, "app.js")
	fit := jsFunc(t, app, "fitLightbox")
	for _, want := range []string{"naturalWidth", "naturalHeight", "innerWidth", "innerHeight", "style.width", "style.height"} {
		if !strings.Contains(fit, want) {
			t.Errorf("fitLightbox must use %s so a small picture grows to fill the viewport", want)
		}
	}
	if !strings.Contains(app, "lightboxImg').addEventListener('load', fitLightbox)") {
		t.Errorf("the lightbox must fit the picture once it has loaded")
	}
}
