// Command assetaudit measures a directory of candidate art and reports what
// each file actually is, so an inventory is built from the pixels rather than
// from a filename or a promise.
//
//	go run ./tools/assetaudit -in ~/Downloads -out audit.json [-sheet contact.png]
//
// For every image it records identity (SHA-256, size, dimensions, mtime) and,
// where the picture is a chroma-key sheet, what the frames are really like:
// how many there are, whether each holds anything, whether the frames differ
// from one another, and whether the content fits a 128px cell without
// clipping. It changes nothing and writes nothing into web/assets.
//
// Two checks earn their place:
//
//   - frame difference. A generated "walk" can come back as the same pose six
//     times. It passes every size check and looks like an animation in a
//     thumbnail, and the character then moonwalks.
//   - per-frame bleed. Content crossing a frame boundary means the grid is
//     wrong, and the slice will show a sliver of the neighbouring pose.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Report is one file's measurements.
type Report struct {
	Index            int      `json:"index"`
	OriginalFilename string   `json:"originalFilename"`
	FileSize         int64    `json:"fileSize"`
	Width            int      `json:"width"`
	Height           int      `json:"height"`
	ModifiedAt       string   `json:"modifiedAt"`
	SHA256           string   `json:"sha256"`
	Background       string   `json:"background"`
	IsChromaGreen    bool     `json:"isChromaGreen"`
	HasAlpha         bool     `json:"hasAlpha"`
	Aspect           float64  `json:"aspect"`
	FrameGuess       int      `json:"frameGuess"`
	Frames           []Frame  `json:"frames,omitempty"`
	FramesDiffer     bool     `json:"framesDiffer"`
	MinShapeDist     float64  `json:"minShapeDistance"`
	MinColourDist    float64  `json:"minColourDistance"`
	ShapeSpread      float64  `json:"shapeSpread"`
	DuplicatePairs   []string `json:"duplicateFramePairs,omitempty"`
	BleedsAcross     bool     `json:"bleedsAcrossFrames"`
	FitsCell         bool     `json:"fitsCell"`
	Notes            []string `json:"notes,omitempty"`
}

// Frame is one cell of a candidate sheet.
type Frame struct {
	N        int     `json:"n"`
	Empty    bool    `json:"empty"`
	BoxX     int     `json:"boxX"`
	BoxY     int     `json:"boxY"`
	BoxW     int     `json:"boxW"`
	BoxH     int     `json:"boxH"`
	FillFrac float64 `json:"fillFraction"`
}

func main() {
	log.SetPrefix("assetaudit: ")
	log.SetFlags(0)
	in := flag.String("in", "", "directory of candidate images")
	out := flag.String("out", "audit.json", "where to write the inventory")
	sheet := flag.String("sheet", "", "optional contact sheet PNG")
	cols := flag.Int("cols", 8, "contact sheet columns")
	cell := flag.Int("cell", 220, "contact sheet cell size")
	flag.Parse()
	if *in == "" {
		flag.Usage()
		os.Exit(2)
	}

	paths := []string{}
	err := filepath.WalkDir(os.ExpandEnv(*in), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".png", ".jpg", ".jpeg":
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}
	// Oldest first: generators emit in order, so this is the closest thing to
	// the batch order the operator described.
	sort.Slice(paths, func(i, j int) bool { return modTime(paths[i]).Before(modTime(paths[j])) })

	var reports []Report
	var thumbs []*image.NRGBA
	for i, p := range paths {
		r, thumb := inspect(p, i+1, *cell)
		reports = append(reports, r)
		thumbs = append(thumbs, thumb)
	}

	b, _ := json.MarshalIndent(reports, "", "  ")
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d files → %s\n", len(reports), *out)

	if *sheet != "" {
		if err := contactSheet(*sheet, thumbs, *cols, *cell); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("contact sheet → %s\n", *sheet)
	}
}

func modTime(p string) time.Time {
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func inspect(p string, idx, cell int) (Report, *image.NRGBA) {
	r := Report{Index: idx, OriginalFilename: filepath.Base(p)}
	fi, err := os.Stat(p)
	if err == nil {
		r.FileSize = fi.Size()
		r.ModifiedAt = fi.ModTime().Format(time.RFC3339)
	}
	if h, err := hashFile(p); err == nil {
		r.SHA256 = h
	}

	img, err := loadNRGBA(p)
	if err != nil {
		r.Notes = append(r.Notes, "could not decode: "+err.Error())
		return r, blank(cell)
	}
	b := img.Bounds()
	r.Width, r.Height = b.Dx(), b.Dy()
	r.Aspect = round(float64(r.Width)/float64(r.Height), 3)

	bg := corner(img)
	r.Background = fmt.Sprintf("#%02X%02X%02X", bg.R, bg.G, bg.B)
	// "Green enough": the generator is asked for #00FF00, but JPEG and
	// rescaling move it, so this is a band rather than an equality test.
	r.IsChromaGreen = bg.G > 150 && int(bg.G)-int(bg.R) > 70 && int(bg.G)-int(bg.B) > 70
	r.HasAlpha = anyAlpha(img)

	// A sheet is a wide strip. Guess the frame count from the aspect: six
	// 128-cells is 6.0, four is 4.0.
	switch {
	case near(r.Aspect, 6, 0.25):
		r.FrameGuess = 6
	case near(r.Aspect, 4, 0.25):
		r.FrameGuess = 4
	case near(r.Aspect, 3, 0.2):
		r.FrameGuess = 3
	case near(r.Aspect, 8, 0.3):
		r.FrameGuess = 8
	}

	if r.IsChromaGreen && r.FrameGuess > 0 {
		analyseFrames(&r, img, bg)
	} else if !r.IsChromaGreen {
		r.Notes = append(r.Notes, "not a chroma-key sheet: treated as a finished picture")
	}
	return r, thumbnail(img, cell, bg)
}

// analyseFrames keys the background out and measures each cell.
func analyseFrames(r *Report, img *image.NRGBA, bg color.NRGBA) {
	n := r.FrameGuess
	fw := r.Width / n
	if fw == 0 {
		return
	}
	mask := keyed(img, bg)

	var boxes []image.Rectangle
	for i := 0; i < n; i++ {
		cellR := image.Rect(i*fw, 0, (i+1)*fw, r.Height)
		box := contentBounds(mask, cellR)
		f := Frame{N: i + 1, Empty: box.Empty()}
		if !box.Empty() {
			local := box.Sub(image.Pt(i*fw, 0))
			f.BoxX, f.BoxY, f.BoxW, f.BoxH = local.Min.X, local.Min.Y, local.Dx(), local.Dy()
			f.FillFrac = round(float64(local.Dx()*local.Dy())/float64(fw*r.Height), 3)
			// Content touching a cell edge is the signature of a grid that
			// does not line up with the art.
			if local.Min.X <= 1 || local.Max.X >= fw-1 {
				r.BleedsAcross = true
			}
		}
		boxes = append(boxes, box)
		r.Frames = append(r.Frames, f)
	}

	// Scaled to a 128 cell by the same rule tools/spritesheet uses, does the
	// widest frame still fit?
	union := image.Rectangle{Min: image.Pt(1<<30, 1<<30), Max: image.Pt(-1, -1)}
	for i, bx := range boxes {
		if bx.Empty() {
			continue
		}
		union = union.Union(bx.Sub(image.Pt(i*fw, 0)))
	}
	r.FitsCell = !union.Empty() && union.Dy() > 0 &&
		float64(union.Dx())*(128.0*0.96/float64(union.Dy())) <= 128

	// Do the frames actually differ?
	//
	// Two measures, because they answer different questions. SHAPE (alpha
	// coverage) is what catches a walk that does not move its legs — the
	// silhouette has to change. COLOUR catches a repeated frame in a loop
	// where the silhouette legitimately barely moves: a "work" pose stands
	// still and animates its face and hands, so shape alone would condemn a
	// perfectly good sheet.
	r.MinShapeDist, r.MinColourDist = 1, 1
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if boxes[i].Empty() || boxes[j].Empty() {
				continue
			}
			sd := frameDistance(mask, boxes[i], boxes[j])
			cd := colourDistance(mask, boxes[i], boxes[j])
			r.MinShapeDist = math.Min(r.MinShapeDist, sd)
			r.MinColourDist = math.Min(r.MinColourDist, cd)
			// Identical on both counts is a genuinely repeated frame.
			if sd < 0.012 && cd < 0.012 {
				r.DuplicatePairs = append(r.DuplicatePairs, fmt.Sprintf("%d=%d shape %.3f colour %.3f", i+1, j+1, sd, cd))
			}
		}
	}
	r.MinShapeDist = round(r.MinShapeDist, 4)
	r.MinColourDist = round(r.MinColourDist, 4)
	r.FramesDiffer = len(r.DuplicatePairs) == 0

	// How much the silhouette width varies across the sheet. A walk swings
	// its legs, so this is well above zero; a "walk" that is one pose
	// repeated sits near it even when the faces differ.
	minW, maxW := 1<<30, 0
	for _, f := range r.Frames {
		if f.Empty {
			continue
		}
		if f.BoxW < minW {
			minW = f.BoxW
		}
		if f.BoxW > maxW {
			maxW = f.BoxW
		}
	}
	if maxW > 0 {
		r.ShapeSpread = round(float64(maxW-minW)/float64(maxW), 3)
	}
	if !r.FramesDiffer {
		r.Notes = append(r.Notes, "repeated frames: identical in both shape and colour")
	}
	if r.BleedsAcross {
		r.Notes = append(r.Notes, "content touches a cell edge: the frame grid does not match the art")
	}
}

// frameDistance compares two frames by resampling both to a small grid of
// alpha coverage — shape, not colour, which is what tells two poses apart.
func frameDistance(mask *image.NRGBA, a, b image.Rectangle) float64 {
	const g = 16
	sig := func(r image.Rectangle) []float64 {
		out := make([]float64, g*g)
		if r.Empty() {
			return out
		}
		for y := 0; y < g; y++ {
			for x := 0; x < g; x++ {
				x0 := r.Min.X + r.Dx()*x/g
				x1 := r.Min.X + r.Dx()*(x+1)/g
				y0 := r.Min.Y + r.Dy()*y/g
				y1 := r.Min.Y + r.Dy()*(y+1)/g
				var on, tot float64
				for yy := y0; yy < y1; yy++ {
					for xx := x0; xx < x1; xx++ {
						tot++
						if mask.NRGBAAt(xx, yy).A > 16 {
							on++
						}
					}
				}
				if tot > 0 {
					out[y*g+x] = on / tot
				}
			}
		}
		return out
	}
	sa, sb := sig(a), sig(b)
	var sum float64
	for i := range sa {
		sum += math.Abs(sa[i] - sb[i])
	}
	return sum / float64(len(sa))
}

// colourDistance compares the average colour of a coarse grid over each
// frame's content, so a change of expression or a lit screen registers even
// when the outline does not move.
func colourDistance(mask *image.NRGBA, a, b image.Rectangle) float64 {
	const g = 12
	sig := func(r image.Rectangle) []float64 {
		out := make([]float64, g*g*3)
		if r.Empty() {
			return out
		}
		for y := 0; y < g; y++ {
			for x := 0; x < g; x++ {
				x0, x1 := r.Min.X+r.Dx()*x/g, r.Min.X+r.Dx()*(x+1)/g
				y0, y1 := r.Min.Y+r.Dy()*y/g, r.Min.Y+r.Dy()*(y+1)/g
				var rs, gs, bs, n float64
				for yy := y0; yy < y1; yy++ {
					for xx := x0; xx < x1; xx++ {
						c := mask.NRGBAAt(xx, yy)
						if c.A <= 16 {
							continue
						}
						rs += float64(c.R)
						gs += float64(c.G)
						bs += float64(c.B)
						n++
					}
				}
				if n > 0 {
					i := (y*g + x) * 3
					out[i], out[i+1], out[i+2] = rs/n/255, gs/n/255, bs/n/255
				}
			}
		}
		return out
	}
	sa, sb := sig(a), sig(b)
	var sum float64
	for i := range sa {
		sum += math.Abs(sa[i] - sb[i])
	}
	return sum / float64(len(sa))
}

// ---------- image helpers ----------

func loadNRGBA(p string) (*image.NRGBA, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func corner(img *image.NRGBA) color.NRGBA {
	b := img.Bounds()
	counts := map[color.NRGBA]int{}
	for _, p := range []image.Point{
		{X: b.Min.X + 2, Y: b.Min.Y + 2}, {X: b.Max.X - 3, Y: b.Min.Y + 2},
		{X: b.Min.X + 2, Y: b.Max.Y - 3}, {X: b.Max.X - 3, Y: b.Max.Y - 3},
	} {
		counts[img.NRGBAAt(p.X, p.Y)]++
	}
	best, n := color.NRGBA{}, 0
	for c, k := range counts {
		if k > n {
			best, n = c, k
		}
	}
	return best
}

func anyAlpha(img *image.NRGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 3 {
		for x := b.Min.X; x < b.Max.X; x += 3 {
			if img.NRGBAAt(x, y).A < 255 {
				return true
			}
		}
	}
	return false
}

// keyed returns a copy with the background made transparent, using the same
// tolerance tools/spritesheet uses so the measurements match what conversion
// would produce.
func keyed(img *image.NRGBA, key color.NRGBA) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(b)
	limit := 0.34 * 441.67
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			d := math.Sqrt(sq(float64(c.R)-float64(key.R)) + sq(float64(c.G)-float64(key.G)) + sq(float64(c.B)-float64(key.B)))
			if d <= limit*0.45 {
				c.A = 0
			}
			out.SetNRGBA(x, y, c)
		}
	}
	return out
}

func contentBounds(img *image.NRGBA, r image.Rectangle) image.Rectangle {
	minX, minY, maxX, maxY := r.Max.X, r.Max.Y, r.Min.X, r.Min.Y
	found := false
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.NRGBAAt(x, y).A > 16 {
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

func sq(v float64) float64        { return v * v }
func near(v, t, tol float64) bool { return math.Abs(v-t) <= tol }
func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}

// ---------- contact sheet ----------

func blank(cell int) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, cell, cell))
	draw.Draw(im, im.Bounds(), &image.Uniform{color.NRGBA{40, 44, 66, 255}}, image.Point{}, draw.Src)
	return im
}

// thumbnail fits the image into one cell on a dark backing, so a green sheet
// and a finished picture are both legible side by side.
func thumbnail(img *image.NRGBA, cell int, bg color.NRGBA) *image.NRGBA {
	out := blank(cell)
	b := img.Bounds()
	scale := math.Min(float64(cell-8)/float64(b.Dx()), float64(cell-26)/float64(b.Dy()))
	dw, dh := int(float64(b.Dx())*scale), int(float64(b.Dy())*scale)
	if dw < 1 || dh < 1 {
		return out
	}
	ox, oy := (cell-dw)/2, 4
	for y := 0; y < dh; y++ {
		for x := 0; x < dw; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			sy := b.Min.Y + int(float64(y)/scale)
			if !image.Pt(sx, sy).In(b) {
				continue
			}
			out.SetNRGBA(ox+x, oy+y, img.NRGBAAt(sx, sy))
		}
	}
	return out
}

// contactSheet lays the thumbnails out in reading order. Each cell carries its
// index drawn in a small built-in digit font; the index maps to the filename
// and mtime in the JSON, which is what makes the sheet checkable without
// pulling in a font dependency.
func contactSheet(path string, thumbs []*image.NRGBA, cols, cell int) error {
	if len(thumbs) == 0 {
		return fmt.Errorf("nothing to draw")
	}
	rows := (len(thumbs) + cols - 1) / cols
	out := image.NewNRGBA(image.Rect(0, 0, cols*cell, rows*cell))
	draw.Draw(out, out.Bounds(), &image.Uniform{color.NRGBA{14, 19, 36, 255}}, image.Point{}, draw.Src)
	for i, t := range thumbs {
		ox, oy := (i%cols)*cell, (i/cols)*cell
		draw.Draw(out, image.Rect(ox, oy, ox+cell, oy+cell), t, image.Point{}, draw.Over)
		drawNumber(out, ox+6, oy+cell-18, i+1)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, out)
}

// digits is a 3x5 bitmap per numeral — enough to label a contact sheet
// without a font package.
var digits = [10][5]string{
	{"###", "# #", "# #", "# #", "###"},
	{" # ", "## ", " # ", " # ", "###"},
	{"###", "  #", "###", "#  ", "###"},
	{"###", "  #", "###", "  #", "###"},
	{"# #", "# #", "###", "  #", "  #"},
	{"###", "#  ", "###", "  #", "###"},
	{"###", "#  ", "###", "# #", "###"},
	{"###", "  #", "  #", "  #", "  #"},
	{"###", "# #", "###", "# #", "###"},
	{"###", "# #", "###", "  #", "###"},
}

func drawNumber(dst *image.NRGBA, x, y, n int) {
	s := fmt.Sprintf("%d", n)
	const px = 2
	ink := color.NRGBA{0, 220, 130, 255}
	for _, ch := range s {
		d := digits[ch-'0']
		for ry, row := range d {
			for rx, c := range row {
				if c != '#' {
					continue
				}
				for dy := 0; dy < px; dy++ {
					for dx := 0; dx < px; dx++ {
						dst.SetNRGBA(x+rx*px+dx, y+ry*px+dy, ink)
					}
				}
			}
		}
		x += 4 * px
	}
}
