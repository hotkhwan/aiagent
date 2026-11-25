package web

import (
	"image/png"
	"os"
	"strings"
	"testing"
)

// The sky tint is drawn through a mask. A mask that is a plain window shape
// hangs a coloured sheet over the frames, plants and furniture of the art;
// it must hold the sky's own pixels and have no hard outline.
func TestSkyMaskIsNotAPlainWindowShape(t *testing.T) {
	for _, room := range []string{"code", "test", "library", "plan", "approval", "ops", "deploy", "lounge"} {
		f, err := os.Open("assets/sky-" + room + ".png")
		if err != nil {
			t.Fatal(err)
		}
		im, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		b := im.Bounds()
		minX, minY, maxX, maxY, solid := b.Max.X, b.Max.Y, 0, 0, 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			prev := uint32(0)
			jump := 0
			for x := b.Min.X; x < b.Max.X; x++ {
				_, _, _, a := im.At(x, y).RGBA()
				a >>= 8
				if a > 8 {
					minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
				}
				if a > 200 {
					solid++
				}
				if d := int(a) - int(prev); d > 220 || d < -220 {
					jump++
				}
				prev = a
			}
			if jump > 0 {
				t.Errorf("%s: row %d goes from clear to solid in one pixel", room, y)
				break
			}
		}
		if area := (maxX - minX) * (maxY - minY); area <= 0 || solid*100/area > 70 || solid == 0 {
			t.Errorf("%s: %d of %d pixels of the window are solid", room, solid, area)
		}
	}
	css := readWebFile(t, "styles.css")
	i := strings.Index(css, ".skyLayer {")
	if i < 0 || !strings.Contains(css[i:i+400], "mix-blend-mode: soft-light") {
		t.Error("the sky must blend with the painted window, not cover it")
	}
}
