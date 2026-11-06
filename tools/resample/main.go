// Command resample rewrites an image at an exact size.
//
//	resample -in room.png -size 1600x1000 -out web/assets/bg-code.jpg
//
// Generated art rarely lands on the pixel sizes app.js positions against —
// 1586x992 where the spec says 1600x1000 — and those sizes are not advisory:
// the layout maths uses them. This resamples by area average, which is what
// keeps a downscale from turning into aliased noise, and writes JPEG or PNG
// from the output extension.
//
// It refuses a resize that would distort by more than a hair, because a
// silently squashed room is worse than a failed command: nothing downstream
// reports it and the furniture simply stops lining up.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
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
	log.SetPrefix("resample: ")
	log.SetFlags(0)
	in := flag.String("in", "", "source image")
	out := flag.String("out", "", "destination (.png or .jpg)")
	size := flag.String("size", "", "exact output size, WxH")
	quality := flag.Int("quality", 92, "JPEG quality")
	maxSkew := flag.Float64("max-skew", 0.01, "largest aspect change accepted, as a fraction")
	flag.Parse()
	if *in == "" || *out == "" || *size == "" {
		flag.Usage()
		os.Exit(2)
	}

	w, h, err := parseSize(*size)
	if err != nil {
		log.Fatal(err)
	}
	src, err := load(*in)
	if err != nil {
		log.Fatal(err)
	}
	b := src.Bounds()

	srcAspect := float64(b.Dx()) / float64(b.Dy())
	dstAspect := float64(w) / float64(h)
	skew := math.Abs(srcAspect-dstAspect) / dstAspect
	if skew > *maxSkew {
		log.Fatalf("%s is %dx%d (aspect %.4f); %dx%d is aspect %.4f — a %.1f%% distortion, above the %.1f%% limit. "+
			"Crop it deliberately or raise -max-skew if the squash is wanted.",
			filepath.Base(*in), b.Dx(), b.Dy(), srcAspect, w, h, dstAspect, skew*100, *maxSkew*100)
	}

	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	resize(dst, src)

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	switch strings.ToLower(filepath.Ext(*out)) {
	case ".jpg", ".jpeg":
		// JPEG has no alpha. Flatten onto black rather than letting the
		// encoder decide, so a transparent corner cannot come out grey.
		flat := image.NewRGBA(dst.Bounds())
		draw.Draw(flat, flat.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
		draw.Draw(flat, flat.Bounds(), dst, image.Point{}, draw.Over)
		err = jpeg.Encode(f, flat, &jpeg.Options{Quality: *quality})
	default:
		err = png.Encode(f, dst)
	}
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s  %dx%d → %dx%d  (aspect change %.2f%%)\n",
		filepath.Base(*out), b.Dx(), b.Dy(), w, h, skew*100)
}

func parseSize(s string) (int, int, error) {
	parts := strings.Split(strings.ToLower(s), "x")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("-size %q: want WxH", s)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("-size %q: want two positive numbers", s)
	}
	return w, h, nil
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

// resize fills dst from src by averaging the source pixels under each
// destination pixel, weighting colour by alpha so transparent regions do not
// bleed into the edges.
func resize(dst, src *image.NRGBA) {
	sb, db := src.Bounds(), dst.Bounds()
	sw, sh := float64(sb.Dx()), float64(sb.Dy())
	for y := 0; y < db.Dy(); y++ {
		y0 := int(float64(y) * sh / float64(db.Dy()))
		y1 := int(float64(y+1) * sh / float64(db.Dy()))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < db.Dx(); x++ {
			x0 := int(float64(x) * sw / float64(db.Dx()))
			x1 := int(float64(x+1) * sw / float64(db.Dx()))
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var rs, gs, bs, as, n float64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := src.NRGBAAt(sb.Min.X+sx, sb.Min.Y+sy)
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
