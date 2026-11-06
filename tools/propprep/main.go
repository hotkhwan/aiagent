// Command propprep turns raw generated prop art into shippable assets.
//
//	go run ./tools/propprep -in ~/Downloads/raw -out /tmp/ready \
//	  -map 01=plant-big,02=desk-blue2,03=desk-pink2
//
// Art arrives as whatever the generator produced: a square-ish canvas with the
// object floating in the middle and a wide transparent margin. The app anchors
// a prop by its own edges and positions it by a width taken from roomDefs, so
// that margin is not cosmetic — it shifts the furniture off its mark and
// shrinks the object inside its own slot.
//
// So each file is trimmed to its alpha, scaled to the width the room actually
// draws it at (times two, for a retina iPad), and written under the name the
// code looks up. The widths are read out of web/app.js rather than typed here,
// which is the whole point: there is one place that decides how big a desk is.
//
// It does not judge the art. Run tools/roomcompare after this and look.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Scale from the 800x500 room space to a shipped pixel: the focus view draws
// the room at 1500/800, and an iPad at DPR 2 doubles that again.
const sourceScale = 1500.0 / 800.0 * 2

func main() {
	log.SetPrefix("propprep: ")
	log.SetFlags(0)

	in := flag.String("in", "", "folder of raw art")
	out := flag.String("out", "", "folder to write prepared PNGs into")
	mapping := flag.String("map", "", "comma list of sourceStem=propName, e.g. 01=desk-blue2")
	appJS := flag.String("app", "web/app.js", "source of the roomDefs widths")
	alphaFloor := flag.Int("alpha-floor", 8, "alpha at or below this counts as empty when trimming")
	colours := flag.Int("colours", 256, "palette size; 0 ships full 32-bit RGBA")
	flag.Parse()

	if *in == "" || *out == "" || *mapping == "" {
		flag.Usage()
		os.Exit(2)
	}

	widths, err := propWidths(*appJS)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		log.Fatal(err)
	}

	var pairs [][2]string
	for _, p := range strings.Split(*mapping, ",") {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 {
			log.Fatalf("-map entry %q: want sourceStem=propName", p)
		}
		pairs = append(pairs, [2]string{kv[0], kv[1]})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][1] < pairs[j][1] })

	fmt.Printf("%-14s %-12s %-12s %-12s %-9s %s\n", "prop", "source", "trimmed", "shipped", "bytes", "note")
	for _, p := range pairs {
		stem, name := p[0], p[1]
		drawn, ok := widths[name]
		if !ok {
			log.Fatalf("%q is not a prop in roomDefs; it would be embedded and never drawn", name)
		}
		src, err := loadAny(*in, stem)
		if err != nil {
			log.Fatal(err)
		}
		box := contentBounds(src, uint8(*alphaFloor))
		if box.Empty() {
			log.Fatalf("%s: every pixel is transparent", stem)
		}
		want := int(math.Round(float64(drawn) * sourceScale))
		h := int(math.Round(float64(want) * float64(box.Dy()) / float64(box.Dx())))
		dst := image.NewNRGBA(image.Rect(0, 0, want, h))
		resize(dst, src, box)
		// The generator leaves the subject at a=252 rather than 255. Push the
		// near-opaque interior back to full so the prop does not sit faintly
		// see-through over a bright room.
		solidify(dst, 250)

		var final image.Image = dst
		if *colours > 0 {
			final = quantize(dst, *colours)
		}
		path := filepath.Join(*out, name+".png")
		if err := save(final, path); err != nil {
			log.Fatal(err)
		}
		st, _ := os.Stat(path)
		note := ""
		if box.Dx() < want {
			note = fmt.Sprintf("UPSCALED %.2fx — source is smaller than the room needs", float64(want)/float64(box.Dx()))
		}
		fmt.Printf("%-14s %-12s %-12s %-12s %-9s %s\n", name,
			fmt.Sprintf("%dx%d", src.Bounds().Dx(), src.Bounds().Dy()),
			fmt.Sprintf("%dx%d", box.Dx(), box.Dy()),
			fmt.Sprintf("%dx%d", want, h), kb(st.Size()), note)
	}
}

// propWidths reads the widths props are drawn at out of roomDefs. A prop used
// in more than one room takes the largest, so it is never upscaled in the room
// that draws it biggest.
func propWidths(path string) (map[string]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(b)
	start := strings.Index(src, "const roomDefs = {")
	if start < 0 {
		return nil, fmt.Errorf("%s has no `const roomDefs = {`", path)
	}
	end := strings.Index(src[start:], "\n}\n")
	if end < 0 {
		return nil, fmt.Errorf("roomDefs is not closed by a `}` at column 0")
	}
	out := map[string]int{}
	re := regexp.MustCompile(`\['([\w-]+)', *-?\d+, *-?\d+, *(\d+)\]`)
	for _, m := range re.FindAllStringSubmatch(src[start:start+end], -1) {
		w, _ := strconv.Atoi(m[2])
		if w > out[m[1]] {
			out[m[1]] = w
		}
	}
	return out, nil
}

func loadAny(dir, stem string) (*image.NRGBA, error) {
	for _, ext := range []string{".png", ".PNG"} {
		p := filepath.Join(dir, stem+ext)
		if _, err := os.Stat(p); err == nil {
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			defer f.Close()
			img, err := png.Decode(f)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			b := img.Bounds()
			out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
			return out, nil
		}
	}
	return nil, fmt.Errorf("no %s.png in %s", stem, dir)
}

func save(img image.Image, p string) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func kb(n int64) string { return fmt.Sprintf("%.0fK", float64(n)/1024) }

// quantize reduces the image to an n-colour palette, which for painted art
// with a soft edge is worth 4x to 5x on the wire. 32-bit RGBA is the wrong
// format to ship this in: ten props came to 3.8 MB against the 101 KB the
// pastel set occupied, and there are 39 of them.
//
// A PNG palette carries its own alpha per entry, so the antialiased edge
// survives — it just costs palette slots, which is why the alpha is bucketed
// before the colours are chosen rather than left as 256 separate levels.
//
// The palette comes from median cut, and image/draw's Floyd-Steinberg does
// the matching, dithering away the banding that a painted gradient would
// otherwise show.
func quantize(src *image.NRGBA, n int) image.Image {
	pal := medianCut(src, n)
	if len(pal) == 0 {
		return src
	}
	dst := image.NewPaletted(src.Bounds(), pal)
	draw.FloydSteinberg.Draw(dst, src.Bounds(), src, src.Bounds().Min)
	return dst
}

// medianCut picks n representative colours by repeatedly splitting the box
// with the widest channel spread at its median.
func medianCut(img *image.NRGBA, n int) color.Palette {
	b := img.Bounds()
	var px []color.NRGBA
	// Sample at most ~120k pixels; the palette does not get better past that
	// and a 1500x1500 source would otherwise sort six million entries.
	step := 1
	if total := b.Dx() * b.Dy(); total > 120000 {
		step = int(math.Sqrt(float64(total) / 120000))
		if step < 1 {
			step = 1
		}
	}
	transparent := false
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			c := img.NRGBAAt(x, y)
			if c.A == 0 {
				transparent = true
				continue
			}
			// Bucket alpha so the soft edge does not eat the palette.
			c.A = c.A / 16 * 16
			if c.A == 0 {
				c.A = 16
			}
			px = append(px, c)
		}
	}
	if len(px) == 0 {
		return nil
	}
	if transparent {
		n-- // reserve one slot for fully transparent
	}

	boxes := [][]color.NRGBA{px}
	for len(boxes) < n {
		// split the box with the widest spread in any channel
		best, bestSpread, bestCh := -1, -1, 0
		for i, bx := range boxes {
			if len(bx) < 2 {
				continue
			}
			for ch := 0; ch < 4; ch++ {
				lo, hi := 255, 0
				for _, c := range bx {
					v := int(chanOf(c, ch))
					if v < lo {
						lo = v
					}
					if v > hi {
						hi = v
					}
				}
				if hi-lo > bestSpread {
					best, bestSpread, bestCh = i, hi-lo, ch
				}
			}
		}
		if best < 0 || bestSpread <= 0 {
			break
		}
		bx := boxes[best]
		ch := bestCh
		sort.Slice(bx, func(i, j int) bool { return chanOf(bx[i], ch) < chanOf(bx[j], ch) })
		mid := len(bx) / 2
		boxes[best] = bx[:mid]
		boxes = append(boxes, bx[mid:])
	}

	pal := make(color.Palette, 0, n+1)
	if transparent {
		pal = append(pal, color.NRGBA{})
	}
	for _, bx := range boxes {
		if len(bx) == 0 {
			continue
		}
		var r, g, bl, a float64
		for _, c := range bx {
			r += float64(c.R)
			g += float64(c.G)
			bl += float64(c.B)
			a += float64(c.A)
		}
		f := float64(len(bx))
		pal = append(pal, color.NRGBA{
			R: uint8(math.Round(r / f)), G: uint8(math.Round(g / f)),
			B: uint8(math.Round(bl / f)), A: uint8(math.Round(a / f)),
		})
	}
	return pal
}

func chanOf(c color.NRGBA, ch int) uint8 {
	switch ch {
	case 0:
		return c.R
	case 1:
		return c.G
	case 2:
		return c.B
	default:
		return c.A
	}
}

// contentBounds is the smallest rectangle holding every pixel above floor.
func contentBounds(img *image.NRGBA, floor uint8) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X-1, b.Min.Y-1
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.NRGBAAt(x, y).A > floor {
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
	if maxX < minX {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX+1, maxY+1)
}

// solidify raises near-opaque alpha to 255 so a prop is not faintly
// see-through, while leaving the soft edge that keeps it from looking cut with
// scissors.
func solidify(img *image.NRGBA, floor uint8) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c := img.NRGBAAt(x, y); c.A >= floor && c.A < 255 {
				c.A = 255
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// resize fills dst from src[sr] by averaging the source pixels under each
// destination pixel, weighting colour by alpha so the transparent surround
// does not bleed a dark fringe into the edges.
func resize(dst, src *image.NRGBA, sr image.Rectangle) {
	sw, sh := float64(sr.Dx()), float64(sr.Dy())
	db := dst.Bounds()
	for y := 0; y < db.Dy(); y++ {
		y0 := sr.Min.Y + int(float64(y)*sh/float64(db.Dy()))
		y1 := sr.Min.Y + int(float64(y+1)*sh/float64(db.Dy()))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < db.Dx(); x++ {
			x0 := sr.Min.X + int(float64(x)*sw/float64(db.Dx()))
			x1 := sr.Min.X + int(float64(x+1)*sw/float64(db.Dx()))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					if !image.Pt(sx, sy).In(src.Bounds()) {
						continue
					}
					c := src.NRGBAAt(sx, sy)
					a := float64(c.A)
					rs += float64(c.R) * a
					gs += float64(c.G) * a
					bs += float64(c.B) * a
					as += a
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := color.NRGBA{A: uint8(math.Round(as / n))}
			if as > 0 {
				o.R = uint8(math.Round(rs / as))
				o.G = uint8(math.Round(gs / as))
				o.B = uint8(math.Round(bs / as))
			}
			dst.SetNRGBA(x, y, o)
		}
	}
}
