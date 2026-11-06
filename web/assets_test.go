package web

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// These tests guard the embedded art against the mistakes that are invisible
// in a code review and only show up as a broken-looking village: a sprite
// sheet whose frames are the wrong size, a prop that ships as an opaque
// rectangle instead of a cut-out, a renamed file the server will not serve,
// or a room that is missing one of its three pieces.
//
// They matter most when a new art set arrives. The set is specified in
// docs/ASSET_SPEC.md; this file is that spec made executable.

// assetName mirrors the serving allowlist in internal/world/http.go. A file
// that fails it is embedded in the binary but answers 404 — silently, because
// a missing asset is indistinguishable from a disabled service.
var assetName = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}\.(png|jpg)$`)

var (
	agentRoles = []string{"deploy", "designer", "dev", "infra", "research", "tester"}
	// Frame counts come from animSpec in web/app.js. Each frame is 128x128 in
	// a single row, so the canvas width is frames*128.
	agentAnims = map[string]int{
		"walkL": 6, "walkR": 6,
		"work": 4, "think": 4, "rest": 4, "cheer": 4,
	}
	// The eight rooms, in workflow order. Each needs a background, a sky seen
	// through its window, and a door plaque.
	rooms = []string{"plan", "library", "code", "test", "ops", "deploy", "approval", "lounge"}
)

const frameSize = 128

func assetFiles(t *testing.T) []string {
	t.Helper()
	ents, err := fs.ReadDir(FS, "assets")
	if err != nil {
		t.Fatalf("read embedded assets: %v", err)
	}
	if len(ents) == 0 {
		t.Fatal("no embedded assets: the //go:embed directive is not matching web/assets")
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// config decodes just the header of an embedded image.
func config(t *testing.T, name string) image.Config {
	t.Helper()
	f, err := FS.Open(path.Join("assets", name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("%s: not a decodable image: %v", name, err)
	}
	return cfg
}

func TestAssetNamesAreServable(t *testing.T) {
	for _, n := range assetFiles(t) {
		if !assetName.MatchString(n) {
			t.Errorf("%s does not match the serving allowlist %s — it would be embedded but answer 404", n, assetName)
		}
	}
}

// TestAgentSpriteSheetGrid is the one that earns its place. A sheet that is
// off by a single row or carries padding produces a character that jitters,
// floats or shows a slice of the next frame, and nothing in the build or the
// browser console reports it.
func TestAgentSpriteSheetGrid(t *testing.T) {
	present := map[string]bool{}
	for _, n := range assetFiles(t) {
		if strings.HasPrefix(n, "ch-") {
			present[n] = true
		}
	}
	for _, role := range agentRoles {
		for anim, frames := range agentAnims {
			name := "ch-" + role + "-" + anim + ".png"
			if !present[name] {
				t.Errorf("missing sprite sheet %s", name)
				continue
			}
			delete(present, name)
			cfg := config(t, name)
			wantW := frames * frameSize
			if cfg.Width != wantW || cfg.Height != frameSize {
				t.Errorf("%s is %dx%d, want %dx%d (%d frames of %dpx in one row, no padding)",
					name, cfg.Width, cfg.Height, wantW, frameSize, frames, frameSize)
			}
		}
	}
	for n := range present {
		t.Errorf("%s looks like an agent sheet but is not one of %v x %v — app.js will never load it",
			n, agentRoles, animNames())
	}
}

func animNames() []string {
	out := make([]string, 0, len(agentAnims))
	for a := range agentAnims {
		out = append(out, a)
	}
	return out
}

// TestRoomArtIsComplete checks each room has its background, sky and plaque at
// the sizes app.js positions them at. A room missing one piece renders as a
// hole rather than an error.
func TestRoomArtIsComplete(t *testing.T) {
	for _, room := range rooms {
		for _, want := range []struct {
			name string
			w, h int
		}{
			{"bg-" + room + ".jpg", 1600, 1000},
			{"sky-" + room + ".png", 800, 500},
		} {
			cfg := config(t, want.name)
			if cfg.Width != want.w || cfg.Height != want.h {
				t.Errorf("%s is %dx%d, want %dx%d", want.name, cfg.Width, cfg.Height, want.w, want.h)
			}
		}
		// Plaques are hand-sized to their text, so only existence is checked.
		if _, err := FS.Open(path.Join("assets", "plq-"+room+".png")); err != nil {
			t.Errorf("missing door plaque plq-%s.png", room)
		}
	}
}

// TestPropsAreCutOut catches the single most common failure in a new art set:
// a prop exported with its background baked in. Every PNG here is composited
// over a room, so it must carry at least one non-opaque pixel. The eight
// bg-*.jpg room backgrounds are the deliberate exception — they are the
// bottom layer and are fully opaque.
func TestPropsAreCutOut(t *testing.T) {
	for _, n := range assetFiles(t) {
		f, err := FS.Open(path.Join("assets", n))
		if err != nil {
			t.Errorf("%s: %v", n, err)
			continue
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: not a decodable image: %v", n, err)
			continue
		}
		transparent := false
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y && !transparent; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if _, _, _, a := img.At(x, y).RGBA(); a < 0xffff {
					transparent = true
					break
				}
			}
		}
		switch {
		case strings.HasSuffix(n, ".jpg"):
			if !strings.HasPrefix(n, "bg-") {
				t.Errorf("%s is a JPG but not a room background; props need PNG alpha", n)
			}
			if transparent {
				t.Errorf("%s reports transparency, which a JPG cannot carry — re-check the export", n)
			}
		case !transparent:
			t.Errorf("%s has no transparent pixel: it will render as an opaque rectangle over the room. "+
				"Export it cut out, with a real alpha channel", n)
		}
	}
}

// TestDialogDisplayStaysScoped guards one CSS mistake that is invisible in a
// diff and total in effect.
//
// A <dialog> is hidden by the user agent's `dialog:not([open]) { display:
// none }`. Any author rule that sets `display` on a dialog without [open]
// beats it, and every dialog in the app is then permanently on screen, over
// the page, and impossible to close — closing only removes [open], and
// nothing else was hiding it. That shipped once.
func TestDialogDisplayStaysScoped(t *testing.T) {
	b, err := fs.ReadFile(FS, "styles.css")
	if err != nil {
		t.Fatalf("read styles.css: %v", err)
	}
	// Each rule is "selectors { declarations }"; look at the ones naming a
	// dialog and check any display declaration is gated on [open].
	for _, rule := range strings.Split(string(b), "}") {
		open := strings.Index(rule, "{")
		if open < 0 {
			continue
		}
		selector := strings.TrimSpace(rule[:open])
		body := rule[open+1:]
		// Only the last line of the previous rule can trail in; selectors
		// start after the last newline that is not part of this selector.
		if i := strings.LastIndex(selector, ";"); i >= 0 {
			selector = strings.TrimSpace(selector[i+1:])
		}
		if !strings.Contains(selector, "dialog") {
			continue
		}
		if !strings.Contains(body, "display:") {
			continue
		}
		if strings.Contains(selector, "[open]") || strings.Contains(body, "display: none") {
			continue
		}
		t.Errorf("rule %q sets display on a dialog without [open]; "+
			"it will override `dialog:not([open]) { display: none }` and leave every dialog "+
			"stuck open and unclosable. Move the display declaration into a [open] rule.",
			strings.Join(strings.Fields(selector), " "))
	}
}
