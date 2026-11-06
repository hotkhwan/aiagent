// Command roomcompare composes candidate props into the real rooms.
//
//	go run ./tools/roomcompare -new ~/Downloads/props_batch_02 -rooms code,test,library -out cmp.png
//
// Props never fail on their own. Batch 01 passed every automated check — ten
// exact widths, real alpha, tight crop, correct names — and had to be rejected
// the moment it was seen standing in the rooms: flat vector against painted
// illustration, dark slabs in a white lab, and desks that had lost their
// chairs. Looking at a contact sheet would not have caught any of that.
//
// So this draws each named room twice at the real positions over the real
// background: the left half is what ships today, the right half is the same
// room with every file found in -new swapped in. One room per output row, in
// the order given by -rooms, which is also printed.
//
// Positions come from roomDefs in web/app.js, parsed rather than copied, so
// this cannot drift from what the app actually draws.
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
	"regexp"
	"strconv"
	"strings"
)

// The room composition space, from web/app.js: RW x RH.
const (
	roomW = 800
	roomH = 500
	gap   = 12
)

type item struct {
	name    string
	x, y, w int
}

type room struct {
	key   string
	bg    string
	items []item
}

func main() {
	log.SetPrefix("roomcompare: ")
	log.SetFlags(0)

	newDir := flag.String("new", "", "folder of candidate props to swap in (by file name)")
	assets := flag.String("assets", "web/assets", "the shipping assets")
	appJS := flag.String("app", "web/app.js", "source of roomDefs")
	want := flag.String("rooms", "code,test,library", "rooms to draw, comma separated")
	out := flag.String("out", "cmp.png", "output image")
	flag.Parse()

	if *newDir == "" {
		flag.Usage()
		os.Exit(2)
	}

	rooms, err := parseRooms(*appJS)
	if err != nil {
		log.Fatal(err)
	}

	keys := strings.Split(*want, ",")
	canvas := image.NewNRGBA(image.Rect(0, 0, roomW*2+gap, len(keys)*(roomH+gap)-gap))
	draw.Draw(canvas, canvas.Bounds(), &image.Uniform{color.NRGBA{0x0E, 0x13, 0x24, 0xFF}}, image.Point{}, draw.Src)

	swapped := map[string]bool{}
	for i, k := range keys {
		k = strings.TrimSpace(k)
		r, ok := rooms[k]
		if !ok {
			log.Fatalf("no room %q in roomDefs; known: %s", k, strings.Join(keysOf(rooms), " "))
		}
		y := i * (roomH + gap)
		cur := compose(r, *assets, "", nil)
		nw := compose(r, *assets, *newDir, swapped)
		draw.Draw(canvas, image.Rect(0, y, roomW, y+roomH), cur, image.Point{}, draw.Src)
		draw.Draw(canvas, image.Rect(roomW+gap, y, roomW*2+gap, y+roomH), nw, image.Point{}, draw.Src)
		fmt.Printf("row %d: %-10s left=current  right=new\n", i+1, k)
	}

	if err := save(canvas, *out); err != nil {
		log.Fatal(err)
	}
	if len(swapped) == 0 {
		fmt.Printf("\nWARNING: nothing in %s matched a prop in these rooms — the halves are identical.\n", *newDir)
	} else {
		fmt.Printf("\nswapped in: %s\n", strings.Join(keysOf2(swapped), " "))
	}
	fmt.Printf("%s  %dx%d\n", *out, canvas.Bounds().Dx(), canvas.Bounds().Dy())
}

// parseRooms reads roomDefs out of app.js. It wants the bg name and the
// ['name', x, y, w] item tuples; anything else in the object is ignored.
func parseRooms(path string) (map[string]room, error) {
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
	body := src[start : start+end]

	head := regexp.MustCompile(`(?m)^  (\w+): \{`)
	itemRe := regexp.MustCompile(`\['([\w-]+)', *(-?\d+), *(-?\d+), *(\d+)\]`)
	bgRe := regexp.MustCompile(`bg: '([\w-]+)'`)

	out := map[string]room{}
	locs := head.FindAllStringSubmatchIndex(body, -1)
	for i, loc := range locs {
		key := body[loc[2]:loc[3]]
		chunkEnd := len(body)
		if i+1 < len(locs) {
			chunkEnd = locs[i+1][0]
		}
		chunk := body[loc[1]:chunkEnd]
		r := room{key: key}
		if m := bgRe.FindStringSubmatch(chunk); m != nil {
			r.bg = m[1]
		}
		for _, m := range itemRe.FindAllStringSubmatch(chunk, -1) {
			x, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			w, _ := strconv.Atoi(m[4])
			r.items = append(r.items, item{m[1], x, y, w})
		}
		out[key] = r
	}
	return out, nil
}

// compose draws one room. When newDir is set, a prop is taken from there if a
// file of that name exists, and the name is recorded in swapped.
func compose(r room, assets, newDir string, swapped map[string]bool) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, roomW, roomH))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{color.NRGBA{0x1A, 0x1F, 0x3D, 0xFF}}, image.Point{}, draw.Src)
	if r.bg != "" {
		if bg, err := load(filepath.Join(assets, r.bg+".jpg")); err == nil {
			resizeOver(dst, bg, bg.Bounds(), dst.Bounds())
		}
	}
	for _, it := range r.items {
		path := filepath.Join(assets, it.name+".png")
		if newDir != "" {
			if cand := filepath.Join(newDir, it.name+".png"); exists(cand) {
				path = cand
				if swapped != nil {
					swapped[it.name] = true
				}
			}
		}
		src, err := load(path)
		if err != nil {
			continue
		}
		b := src.Bounds()
		// width is authoritative; height follows the file's aspect, which is
		// what an <img> with only a width does.
		h := int(math.Round(float64(it.w) * float64(b.Dy()) / float64(b.Dx())))
		resizeOver(dst, src, b, image.Rect(it.x, it.y, it.x+it.w, it.y+h))
	}
	return dst
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func keysOf(m map[string]room) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOf2(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if strings.EqualFold(filepath.Ext(p), ".jpg") {
		return jpeg.Encode(f, img, &jpeg.Options{Quality: 86})
	}
	return png.Encode(f, img)
}

// resizeOver draws src[sr] into dst[dr], averaging the source pixels under
// each destination pixel and blending onto what is already there. Clipped to
// dst, so a prop positioned partly outside the room does not panic.
func resizeOver(dst, src *image.NRGBA, sr, dr image.Rectangle) {
	sw, sh := float64(sr.Dx()), float64(sr.Dy())
	dw, dh := dr.Dx(), dr.Dy()
	if dw <= 0 || dh <= 0 {
		return
	}
	for dy := 0; dy < dh; dy++ {
		py := dr.Min.Y + dy
		if py < dst.Bounds().Min.Y || py >= dst.Bounds().Max.Y {
			continue
		}
		y0 := sr.Min.Y + int(float64(dy)*sh/float64(dh))
		y1 := sr.Min.Y + int(float64(dy+1)*sh/float64(dh))
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for dx := 0; dx < dw; dx++ {
			px := dr.Min.X + dx
			if px < dst.Bounds().Min.X || px >= dst.Bounds().Max.X {
				continue
			}
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
