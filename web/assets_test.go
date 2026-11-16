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
		// Home-screen icons are deliberately opaque: iOS composites a
		// transparent icon onto black, so they are rendered onto the app's
		// background colour instead (tools/appicon).
		case strings.HasPrefix(n, "icon-"):
			if transparent {
				t.Errorf("%s has transparency; a home-screen icon must be opaque or iOS will matte it onto black", n)
			}
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

// TestEveryThaiStringIsTranslated catches the one failure mode i18n has.
//
// t() returns its argument unchanged when the dictionary has no entry, which
// is the right behaviour at runtime — a missing line shows Thai rather than a
// raw key — and is invisible in review. The result is an English page with
// Thai scattered through it, and nothing anywhere says so. So every string
// app.js passes to t() must have an entry in i18n.js.
func TestEveryThaiStringIsTranslated(t *testing.T) {
	app, err := fs.ReadFile(FS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	dict, err := fs.ReadFile(FS, "i18n.js")
	if err != nil {
		t.Fatalf("read i18n.js: %v", err)
	}

	// Keys are written one per line as 'thai': — the value may wrap to the
	// next line, which does not affect the key.
	keys := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*'((?:[^'\\]|\\.)*)':`).FindAllStringSubmatch(string(dict), -1) {
		keys[m[1]] = true
	}
	if len(keys) == 0 {
		t.Fatal("i18n.js has no dictionary entries — the EN table was renamed or restructured; update this test with it")
	}

	thai := regexp.MustCompile(`[\x{0E00}-\x{0E7F}]`)
	calls := regexp.MustCompile(`\btr\('((?:[^'\\\n]|\\.)*)'\)`)
	seen, missing := map[string]bool{}, 0
	for _, m := range calls.FindAllStringSubmatch(string(app), -1) {
		s := m[1]
		if !thai.MatchString(s) || seen[s] {
			continue
		}
		seen[s] = true
		if !keys[s] {
			missing++
			t.Errorf("app.js passes %q to t() but i18n.js has no entry; it will stay Thai on the English page", s)
		}
	}
	if len(seen) == 0 {
		t.Error("found no t('…') calls carrying Thai in app.js — either the wrapping was lost or this test stopped matching it")
	}
}

// TestEveryRoomHasAGlyph keeps the workflow stepper honest.
//
// iconSvg() falls back to a plain dash for an unknown key, so a room with no
// entry in iconPaths does not throw, does not log, and does not look broken
// in a diff — it just draws "—" next to its name in the stepper and in the
// room label, which reads as a deliberate design until someone asks why one
// room has no icon. Three of the eight went that way for a month.
func TestEveryRoomHasAGlyph(t *testing.T) {
	b, err := fs.ReadFile(FS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(b)
	start := strings.Index(src, "const iconPaths = {")
	if start < 0 {
		t.Fatal("app.js has no `const iconPaths = {` — the glyph table was renamed; update this test with it")
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatal("iconPaths is not closed by a `}` at column 0")
	}
	table := src[start : start+end]
	for _, r := range rooms {
		if !regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(r) + `: '`).MatchString(table) {
			t.Errorf("room %q has no glyph in iconPaths; iconSvg() will silently draw the dash fallback for it", r)
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

// TestEveryViewHasAButtonAndAPane ties the nav in index.html to the table in
// app.js that switches views.
//
// A view is three things in two files: a button in the nav, a row in `views`,
// and the pane the row names. Add the button and forget the row and the
// button does nothing; name a pane that is not in the page and setView throws
// on load, before the first poll, which leaves the whole page blank.
func TestEveryViewHasAButtonAndAPane(t *testing.T) {
	page, err := fs.ReadFile(FS, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	app, err := fs.ReadFile(FS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	html, src := string(page), string(app)
	start := strings.Index(src, "const views = [")
	if start < 0 {
		t.Fatal("app.js has no `const views = [` — the view table was renamed; update this test with it")
	}
	end := strings.Index(src[start:], "\n]")
	if end < 0 {
		t.Fatal("views is not closed by a `]` at column 0")
	}
	rows := regexp.MustCompile(`\['(view[A-Za-z]+)', '([a-z]+)', '([A-Za-z]+)'\]`).FindAllStringSubmatch(src[start:start+end], -1)
	if len(rows) == 0 {
		t.Fatal("views has no rows of the form ['viewX', 'name', 'paneId']")
	}
	inTable := map[string]bool{}
	for _, r := range rows {
		button, pane := r[1], r[3]
		inTable[button] = true
		if !strings.Contains(html, `id="`+button+`"`) {
			t.Errorf("views names the button %q, which index.html does not have", button)
		}
		if !strings.Contains(html, `id="`+pane+`"`) {
			t.Errorf("view %q shows the pane %q, which index.html does not have", r[2], pane)
		}
	}
	for _, m := range regexp.MustCompile(`<button type="button" id="(view[A-Za-z]+)"`).FindAllStringSubmatch(html, -1) {
		if !inTable[m[1]] {
			t.Errorf("index.html has the nav button %q with no row in views; it would do nothing", m[1])
		}
	}
	for _, want := range []string{"viewMap", "viewList", "viewPM", "viewTasks", "viewReview"} {
		if !inTable[want] {
			t.Errorf("views has no row for %q", want)
		}
	}
}

// TestAThrottledWriteIsNotASignOut guards the difference between the two 429s
// the service sends.
//
// AIAGENT_LOCKED means wrong tokens and the page must ask for one again.
// AIAGENT_SLOW_DOWN means two writes landed inside a second — confirming one
// PM proposal and then the next is enough. api() once treated every 429 as
// the first kind, so a quick second tap threw a signed-in user to the login
// dialog.
func TestAThrottledWriteIsNotASignOut(t *testing.T) {
	b, err := fs.ReadFile(FS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(b)
	start := strings.Index(src, "async function api (")
	if start < 0 {
		t.Fatal("app.js has no `async function api (` — update this test with the rename")
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatal("api() is not closed by a `}` at column 0")
	}
	body := src[start : start+end]
	if regexp.MustCompile(`res\.status === 401 \|\| res\.status === 429\)`).MatchString(body) {
		t.Error("api() sends every 429 to the login dialog; AIAGENT_SLOW_DOWN is write spacing, not a bad token")
	}
	if !strings.Contains(body, "AIAGENT_SLOW_DOWN") {
		t.Error("api() does not tell AIAGENT_SLOW_DOWN apart from AIAGENT_LOCKED")
	}
}

// jsFunc is the source of one top-level function of app.js.
func jsFunc(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "function "+name+" (")
	if start < 0 {
		t.Fatalf("app.js has no `function %s (` — update this test with the rename", name)
	}
	end := strings.Index(src[start:], "\n}")
	if end < 0 {
		t.Fatalf("%s() is not closed by a `}` at column 0", name)
	}
	return src[start : start+end]
}

// TestApprovingATaskIsWithinReach guards where a task's buttons are.
//
// A ready task waits for one thing: the user pressing "approve — start". The
// button once came last in the task's panel, under the acceptance criteria and
// the timeline, and the card on the board said nothing. In a laptop window the
// panel's head was all that showed: "ready", a title, and no way to start.
func TestApprovingATaskIsWithinReach(t *testing.T) {
	b, err := fs.ReadFile(FS, "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	src := string(b)

	detail := jsFunc(t, src, "taskDetail")
	actions := strings.Index(detail, "class: 'tactions'")
	long := strings.Index(detail, "class: 'tcols2'")
	if actions < 0 || long < 0 {
		t.Fatal("taskDetail() no longer has the tactions row and the tcols2 block — update this test")
	}
	if actions > long {
		t.Error("the task's buttons come after the criteria and the timeline; they belong under the title, where they show without scrolling")
	}

	card := jsFunc(t, src, "taskCard")
	if !regexp.MustCompile(`t\.status === 'ready' \? h\('div', \{ class: 'tcall' \}`).MatchString(card) {
		t.Error("a ready card does not say that it waits for the user's approval")
	}
	if !strings.Contains(card, "scrollIntoView") {
		t.Error("opening a card does not bring the task's panel into view; it opens below the board")
	}
}
