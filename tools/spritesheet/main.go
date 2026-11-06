// Command spritesheet turns a generated chroma-key sprite sheet into the grid
// the village actually loads.
//
//	spritesheet -in raw.png -frames 6 -out web/assets/ch-dev-walkR.png
//
// Generators produce big frames on a flat green background — 3072x512 for a
// six-frame walk, 2508x627 for a four-frame loop. The UI wants 128px frames in
// one row with real alpha. This does the three steps between:
//
//  1. key the background out, with despill so edges are not green-fringed;
//  2. measure ONE bounding box across every frame and scale them all by the
//     same factor, anchored bottom-centre. Per-frame trimming would resize
//     each pose independently and the character would bob and change height
//     as it walked — the sheet has to be treated as one animation, not as N
//     unrelated pictures;
//  3. downscale by area average, which is what keeps a 5x reduction from
//     turning into aliased noise.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	log.SetPrefix("spritesheet: ")
	log.SetFlags(0)

	in := flag.String("in", "", "source sheet (PNG)")
	out := flag.String("out", "", "destination PNG")
	frames := flag.Int("frames", 0, "number of frames in the source row")
	size := flag.Int("size", 128, "output frame size (square)")
	keyHex := flag.String("key", "", "background colour to remove, e.g. 00ff00 (default: sample the corners)")
	tol := flag.Float64("tol", 0.34, "how far from the key colour still counts as background, 0..1")
	margin := flag.Float64("margin", 0.04, "blank fraction kept above the tallest frame")
	flag.Parse()

	if *in == "" || *out == "" || *frames <= 0 {
		flag.Usage()
		os.Exit(2)
	}

	src, err := readPNG(*in)
	if err != nil {
		log.Fatal(err)
	}
	b := src.Bounds()
	if b.Dx()%*frames != 0 {
		log.Printf("warning: width %d is not a multiple of %d frames; the last frame will be short", b.Dx(), *frames)
	}

	key, err := keyColour(*keyHex, src)
	if err != nil {
		log.Fatal(err)
	}

	cut := chromaKey(src, key, *tol)
	fw := b.Dx() / *frames

	// One bounding box for the whole animation (step 2 above).
	box := image.Rectangle{Min: image.Pt(1<<30, 1<<30), Max: image.Pt(-1, -1)}
	for i := 0; i < *frames; i++ {
		r := image.Rect(i*fw, 0, (i+1)*fw, b.Dy())
		fb := contentBounds(cut, r)
		if fb.Empty() {
			continue
		}
		// Measure relative to the frame so frames can be compared.
		fb = fb.Sub(image.Pt(i*fw, 0))
		box = box.Union(fb)
	}
	if box.Empty() || box.Dx() <= 0 || box.Dy() <= 0 {
		log.Fatalf("%s: every frame keyed out completely — wrong -key or -tol too high", *in)
	}

	// Scale so the tallest content fits the frame, less the top margin, and
	// so nothing is ever enlarged past the frame width.
	avail := float64(*size) * (1 - *margin)
	scale := math.Min(avail/float64(box.Dy()), float64(*size)/float64(box.Dx()))

	dst := image.NewNRGBA(image.Rect(0, 0, *size**frames, *size))
	for i := 0; i < *frames; i++ {
		// The source window for this frame, in the shared box.
		sx0 := i*fw + box.Min.X
		sy0 := box.Min.Y
		dw := int(math.Round(float64(box.Dx()) * scale))
		dh := int(math.Round(float64(box.Dy()) * scale))
		// Bottom-centre anchor: the character stands on the frame's floor, so
		// a taller pose grows upward and the feet never move.
		ox := i**size + (*size-dw)/2
		oy := *size - dh
		resizeInto(dst, cut, image.Rect(sx0, sy0, sx0+box.Dx(), sy0+box.Dy()), image.Rect(ox, oy, ox+dw, oy+dh))
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, dst); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s  %dx%d  %d frames  (source box %dx%d, scale %.3f)\n",
		filepath.Base(*out), dst.Bounds().Dx(), dst.Bounds().Dy(), *frames, box.Dx(), box.Dy(), scale)
}

func readPNG(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return img, nil
}

// keyColour returns the background colour: the flag when given, otherwise the
// most common of the four corners, which is what a flat backdrop makes it.
func keyColour(hex string, img image.Image) (color.NRGBA, error) {
	if hex != "" {
		h := strings.TrimPrefix(hex, "#")
		if len(h) != 6 {
			return color.NRGBA{}, fmt.Errorf("-key %q: want six hex digits", hex)
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return color.NRGBA{}, fmt.Errorf("-key %q: %w", hex, err)
		}
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
	}
	b := img.Bounds()
	counts := map[color.NRGBA]int{}
	for _, p := range []image.Point{
		{X: b.Min.X, Y: b.Min.Y}, {X: b.Max.X - 1, Y: b.Min.Y},
		{X: b.Min.X, Y: b.Max.Y - 1}, {X: b.Max.X - 1, Y: b.Max.Y - 1},
	} {
		counts[at(img, p.X, p.Y)]++
	}
	best, n := color.NRGBA{}, 0
	for c, k := range counts {
		if k > n {
			best, n = c, k
		}
	}
	return best, nil
}

func at(img image.Image, x, y int) color.NRGBA {
	r, g, bb, a := img.At(x, y).RGBA()
	if a == 0 {
		return color.NRGBA{}
	}
	// Un-premultiply into 8-bit NRGBA.
	return color.NRGBA{
		R: uint8(r * 0xffff / a >> 8),
		G: uint8(g * 0xffff / a >> 8),
		B: uint8(bb * 0xffff / a >> 8),
		A: uint8(a >> 8),
	}
}

// chromaKey removes the background and despills the remaining edge pixels.
func chromaKey(src image.Image, key color.NRGBA, tol float64) *image.NRGBA {
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	// Distance is measured in RGB; the backdrop is flat, so a simple radius
	// separates it from the art without needing a colour space conversion.
	limit := tol * 441.67 // sqrt(3)*255: the longest possible RGB distance
	soft := limit * 0.45  // inside this, fade rather than cut, to keep edges smooth
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := at(src, x, y)
			d := math.Sqrt(sq(float64(c.R)-float64(key.R)) + sq(float64(c.G)-float64(key.G)) + sq(float64(c.B)-float64(key.B)))
			o := c
			switch {
			case d <= soft:
				o.A = 0
			case d < limit:
				o.A = uint8(float64(c.A) * (d - soft) / (limit - soft))
			}
			if o.A != 0 {
				despill(&o, key)
			}
			dst.SetNRGBA(x-b.Min.X, y-b.Min.Y, o)
		}
	}
	return dst
}

// despill pulls a pixel back off the key hue. Without it every edge keeps a
// green rim that is obvious once the sprite sits on a warm room background.
func despill(c *color.NRGBA, key color.NRGBA) {
	// Only the dominant channel of the key is spilled onto the subject.
	switch {
	case key.G >= key.R && key.G >= key.B:
		if lim := maxU8(c.R, c.B); c.G > lim {
			c.G = lim
		}
	case key.R >= key.G && key.R >= key.B:
		if lim := maxU8(c.G, c.B); c.R > lim {
			c.R = lim
		}
	default:
		if lim := maxU8(c.R, c.G); c.B > lim {
			c.B = lim
		}
	}
}

func maxU8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}

func sq(v float64) float64 { return v * v }

// contentBounds is the tightest rectangle holding any pixel that survived the
// key, within r.
func contentBounds(img *image.NRGBA, r image.Rectangle) image.Rectangle {
	minX, minY := r.Max.X, r.Max.Y
	maxX, maxY := r.Min.X, r.Min.Y
	found := false
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			// 8 and not 0: a few almost-transparent pixels survive any key and
			// would stretch the box across the whole frame.
			if img.NRGBAAt(x, y).A > 8 {
				found = true
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if !found {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// resizeInto draws src[sr] into dst[dr] by averaging the source pixels that
// fall in each destination pixel. Alpha is weighted so transparent pixels do
// not drag colour into the edges.
func resizeInto(dst *image.NRGBA, src *image.NRGBA, sr, dr image.Rectangle) {
	sw, sh := float64(sr.Dx()), float64(sr.Dy())
	dw, dh := dr.Dx(), dr.Dy()
	if dw <= 0 || dh <= 0 {
		return
	}
	for dy := 0; dy < dh; dy++ {
		y0 := sr.Min.Y + int(float64(dy)*sh/float64(dh))
		y1 := sr.Min.Y + int(float64(dy+1)*sh/float64(dh))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			x0 := sr.Min.X + int(float64(dx)*sw/float64(dw))
			x1 := sr.Min.X + int(float64(dx+1)*sw/float64(dw))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, wsum float64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					if !(image.Pt(x, y).In(src.Bounds())) {
						continue
					}
					c := src.NRGBAAt(x, y)
					a := float64(c.A)
					rs += float64(c.R) * a
					gs += float64(c.G) * a
					bs += float64(c.B) * a
					as += a
					wsum++
				}
			}
			if wsum == 0 {
				continue
			}
			o := color.NRGBA{A: uint8(math.Round(as / wsum))}
			if as > 0 {
				o.R = uint8(math.Round(rs / as))
				o.G = uint8(math.Round(gs / as))
				o.B = uint8(math.Round(bs / as))
			}
			dst.SetNRGBA(dr.Min.X+dx, dr.Min.Y+dy, o)
		}
	}
}
