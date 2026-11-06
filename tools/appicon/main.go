// Command appicon builds the home-screen icons from a source image.
//
//	go run ./tools/appicon -in web/assets/house-kcmd.png -out web/assets
//
// iOS composites a transparent icon onto black, so these are rendered onto the
// app's own background colour with the subject inset — an icon that bleeds to
// the edge gets cropped by the rounded-corner mask on both iOS and Android.
//
// Sizes: 180 is what iOS uses for apple-touch-icon on a modern iPad, 192 and
// 512 are what the web manifest asks for (512 doubles as the splash source).
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	log.SetPrefix("appicon: ")
	log.SetFlags(0)

	in := flag.String("in", "", "source image")
	outDir := flag.String("out", "web/assets", "directory to write into")
	bgHex := flag.String("bg", "0E1324", "background colour (--sky)")
	inset := flag.Float64("inset", 0.16, "blank fraction kept on each side, so the rounded mask does not crop the subject")
	sizes := flag.String("sizes", "180,192,512", "icon sizes")
	flag.Parse()

	if *in == "" {
		flag.Usage()
		os.Exit(2)
	}
	bg, err := parseHex(*bgHex)
	if err != nil {
		log.Fatal(err)
	}
	src, err := load(*in)
	if err != nil {
		log.Fatal(err)
	}
	box := contentBounds(src)
	if box.Empty() {
		box = src.Bounds()
	}

	for _, s := range strings.Split(*sizes, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			log.Fatalf("bad size %q", s)
		}
		dst := image.NewNRGBA(image.Rect(0, 0, n, n))
		draw.Draw(dst, dst.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)

		avail := float64(n) * (1 - 2**inset)
		scale := math.Min(avail/float64(box.Dx()), avail/float64(box.Dy()))
		dw := int(math.Round(float64(box.Dx()) * scale))
		dh := int(math.Round(float64(box.Dy()) * scale))
		resizeOver(dst, src, box, image.Rect((n-dw)/2, (n-dh)/2, (n-dw)/2+dw, (n-dh)/2+dh))

		name := filepath.Join(*outDir, fmt.Sprintf("icon-%d.png", n))
		if err := save(dst, name); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s  %dx%d\n", filepath.Base(name), n, n)
	}
}

func parseHex(h string) (color.NRGBA, error) {
	h = strings.TrimPrefix(h, "#")
	if len(h) != 6 {
		return color.NRGBA{}, fmt.Errorf("colour %q: want six hex digits", h)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.NRGBA{}, err
	}
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

func load(p string) (*image.NRGBA, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}

func save(img image.Image, p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// contentBounds trims transparent padding so the subject fills the icon.
func contentBounds(img *image.NRGBA) image.Rectangle {
	b := img.Bounds()
	minX, minY, maxX, maxY := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	found := false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
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

// resizeOver draws src[sr] over dst[dr], averaging source pixels per
// destination pixel and blending onto whatever is already there.
func resizeOver(dst *image.NRGBA, src *image.NRGBA, sr, dr image.Rectangle) {
	sw, sh := float64(sr.Dx()), float64(sr.Dy())
	dw, dh := dr.Dx(), dr.Dy()
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
			var rs, gs, bs, as, n float64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					if !image.Pt(x, y).In(src.Bounds()) {
						continue
					}
					c := src.NRGBAAt(x, y)
					a := float64(c.A)
					rs += float64(c.R) * a
					gs += float64(c.G) * a
					bs += float64(c.B) * a
					as += a
					n++
				}
			}
			if n == 0 || as == 0 {
				continue
			}
			alpha := as / n / 255
			px, py := dr.Min.X+dx, dr.Min.Y+dy
			under := dst.NRGBAAt(px, py)
			dst.SetNRGBA(px, py, color.NRGBA{
				R: blend(rs/as, float64(under.R), alpha),
				G: blend(gs/as, float64(under.G), alpha),
				B: blend(bs/as, float64(under.B), alpha),
				A: 255,
			})
		}
	}
}

func blend(top, bottom, alpha float64) uint8 {
	return uint8(math.Round(top*alpha + bottom*(1-alpha)))
}
