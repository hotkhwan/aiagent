// internal/world/faces.go
//
// Photo faces. The phone app can give each agent a photo face, drawn as a big
// head on its character, and keep one account photo that may stand in for
// every agent's face. They are kept here, per token owner, so the same token
// sees the same faces on every phone. Each owner sees and changes only their
// own: the admin token's photos are kept under "admin", a user token's under
// that user's id, and nothing in a request names whose photos it means.
//
// A photo is never kept as sent. It is decoded (PNG or JPEG only), checked for
// size, scaled down to at most 512 px a side and encoded again as a plain PNG,
// which drops EXIF, GPS and any other metadata the camera wrote. The audit log
// records who changed which photo, never the picture.
//
// Agent characters (looks.go) are a different thing: shared by everyone.
package world

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg" // the decoder for image.Decode
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
)

const (
	facesDirName     = "faces"
	faceAccountFile  = "account.png"
	faceSettingsFile = "settings.json"
	faceSessionPre   = "s_"
	faceSessionExt   = ".png"
	maxFaceBody      = 2 << 20
	maxFaceInSide    = 2048
	minFaceSide      = 16
	maxFaceOutSide   = 512
	maxSessionFaces  = 300
	maxCharacterOnly = 500
)

var (
	// faceSessionRE is the session id rule of the rest of the API
	// (sessionPathRE, sessionIDRE), never empty: no dot, no slash, no percent.
	faceSessionRE = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	// faceOwnerRE is what an owner key may be: "admin" or a user token's id.
	faceOwnerRE = regexp.MustCompile(`^(admin|[0-9a-f]{1,64})$`)

	facesMu sync.Mutex
	// faceVers remembers each stored photo's version so a manifest does not
	// hash up to 300 photos every time.
	faceVers = map[string]faceVer{}
)

type faceVer struct {
	size int64
	mod  time.Time
	v    string
}

type faceSettings struct {
	AccountForAll bool     `json:"accountForAll"`
	CharacterOnly []string `json:"characterOnly"`
}

type faceManifest struct {
	AccountForAll bool              `json:"accountForAll"`
	CharacterOnly []string          `json:"characterOnly"`
	Account       string            `json:"account"`
	Sessions      map[string]string `json:"sessions"`
}

// faceOwner is the key an identity's photos are kept under.
func faceOwner(id Identity) (string, bool) {
	k := ""
	switch id.Role {
	case RoleAdmin:
		k = RoleAdmin
	case RoleUser:
		k = id.UserID
	}
	return k, faceOwnerRE.MatchString(k)
}

func (s *Service) facesDir(key string) string {
	return filepath.Join(s.Store.dir, facesDirName, key)
}

// versionOf is a stored photo's version: its sha256, shortened.
func versionOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// faceVersion reads a stored photo's version; "" when there is none.
// Callers hold facesMu.
func faceVersion(path string) string {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		delete(faceVers, path)
		return ""
	}
	if fv, ok := faceVers[path]; ok && fv.size == st.Size() && fv.mod.Equal(st.ModTime()) {
		return fv.v
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(faceVers) > 10000 {
		faceVers = map[string]faceVer{}
	}
	v := versionOf(b)
	faceVers[path] = faceVer{st.Size(), st.ModTime(), v}
	return v
}

func faceSessionFile(id string) string { return faceSessionPre + id + faceSessionExt }

// sessionFaceIDs lists the sessions an owner has a photo for.
func sessionFaceIDs(dir string) []string {
	ents, _ := os.ReadDir(dir)
	out := []string{}
	for _, e := range ents {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasPrefix(n, faceSessionPre) || !strings.HasSuffix(n, faceSessionExt) {
			continue
		}
		if id := strings.TrimSuffix(strings.TrimPrefix(n, faceSessionPre), faceSessionExt); faceSessionRE.MatchString(id) {
			out = append(out, id)
		}
	}
	return out
}

func loadFaceSettings(dir string) faceSettings {
	var st faceSettings
	if b, err := os.ReadFile(filepath.Join(dir, faceSettingsFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	ids := []string{}
	for _, id := range st.CharacterOnly {
		if faceSessionRE.MatchString(id) {
			ids = append(ids, id)
		}
	}
	st.CharacterOnly = ids
	return st
}

// manifest is everything an owner has, by version. Callers hold facesMu.
func (s *Service) manifest(key string) faceManifest {
	dir := s.facesDir(key)
	st := loadFaceSettings(dir)
	m := faceManifest{AccountForAll: st.AccountForAll, CharacterOnly: st.CharacterOnly, Sessions: map[string]string{}}
	m.Account = faceVersion(filepath.Join(dir, faceAccountFile))
	for _, id := range sessionFaceIDs(dir) {
		if v := faceVersion(filepath.Join(dir, faceSessionFile(id))); v != "" {
			m.Sessions[id] = v
		}
	}
	return m
}

// writeFaceFile writes one file of an owner's atomically, private to this
// service's OS user.
func (s *Service) writeFaceFile(key, name string, b []byte) error {
	root := filepath.Join(s.Store.dir, facesDirName)
	dir := s.facesDir(key)
	for _, d := range []string{root, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// removeFaces deletes everything a revoked user had.
func (s *Service) removeFaces(userID string) {
	if userID == RoleAdmin || !faceOwnerRE.MatchString(userID) {
		return
	}
	facesMu.Lock()
	defer facesMu.Unlock()
	_ = os.RemoveAll(s.facesDir(userID))
}

// checkFaceUpload is the cheap part of the checks, before a write is spent.
func checkFaceUpload(ctype string, body []byte) error {
	mt, _, _ := strings.Cut(ctype, ";")
	if mt = strings.ToLower(strings.TrimSpace(mt)); mt != "image/png" && mt != "image/jpeg" {
		return &APIError{fiber.StatusUnsupportedMediaType, "AIAGENT_UNSUPPORTED_TYPE", "รับเฉพาะรูป PNG หรือ JPEG"}
	}
	if len(body) > maxFaceBody {
		return &APIError{fiber.StatusRequestEntityTooLarge, "AIAGENT_TOO_LARGE", "รูปใหญ่เกิน 2 MB"}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") {
		return ErrBadRequest("ไม่ใช่รูป PNG หรือ JPEG")
	}
	if cfg.Width > maxFaceInSide || cfg.Height > maxFaceInSide || cfg.Width < minFaceSide || cfg.Height < minFaceSide {
		return ErrBadRequest("รูปต้องกว้างและสูง 16-2048 px")
	}
	return nil
}

// cleanFace decodes a checked upload and encodes it again as a PNG of at most
// 512 px a side, with nothing of the original file but its pixels.
func cleanFace(body []byte) ([]byte, error) {
	src, format, err := image.Decode(bytes.NewReader(body))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, ErrBadRequest("ไม่ใช่รูป PNG หรือ JPEG")
	}
	b := src.Bounds()
	if b.Dx() > maxFaceInSide || b.Dy() > maxFaceInSide || b.Dx() < minFaceSide || b.Dy() < minFaceSide {
		return nil, ErrBadRequest("รูปต้องกว้างและสูง 16-2048 px")
	}
	rgba := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	out := image.Image(rgba)
	if side := max(b.Dx(), b.Dy()); side > maxFaceOutSide {
		w := max(1, (b.Dx()*maxFaceOutSide+side/2)/side)
		h := max(1, (b.Dy()*maxFaceOutSide+side/2)/side)
		out = areaAverage(rgba, w, h)
	}
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// areaAverage shrinks src to w x h, each new pixel the mean of the source
// pixels it covers (premultiplied, so a transparent edge does not darken).
func areaAverage(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := 0; x < w; x++ {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var r, g, bl, a, n uint64
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					r, g, bl, a = r+uint64(p[0]), g+uint64(p[1]), bl+uint64(p[2]), a+uint64(p[3])
					n++
				}
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o], dst.Pix[o+1], dst.Pix[o+2], dst.Pix[o+3] = uint8((r+n/2)/n), uint8((g+n/2)/n), uint8((bl+n/2)/n), uint8((a+n/2)/n)
		}
	}
	return dst
}

// faceTarget is the file one request means: the account photo, or one
// session's.
func faceTarget(c fiber.Ctx, session bool) (name, id string, err error) {
	if !session {
		return faceAccountFile, "", nil
	}
	id = c.Params("id")
	if !faceSessionRE.MatchString(id) {
		return "", "", ErrBadRequest("รหัส session ไม่ถูกต้อง")
	}
	id = strings.Clone(id)
	return faceSessionFile(id), id, nil
}

func (s *Service) faceKey(c fiber.Ctx) (string, error) {
	if k, ok := faceOwner(identity(c)); ok {
		return k, nil
	}
	return "", &APIError{fiber.StatusForbidden, "AIAGENT_FORBIDDEN", "token นี้ไม่มีที่เก็บรูป"}
}

func (s *Service) handleFaces(c fiber.Ctx) error {
	key, err := s.faceKey(c)
	if err != nil {
		return s.fail(c, err)
	}
	facesMu.Lock()
	m := s.manifest(key)
	facesMu.Unlock()
	return c.JSON(okEnv(m))
}

func (s *Service) handleFaceGet(session bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		key, err := s.faceKey(c)
		if err != nil {
			return s.fail(c, err)
		}
		name, _, err := faceTarget(c, session)
		if err != nil {
			return s.fail(c, err)
		}
		facesMu.Lock()
		b, rerr := os.ReadFile(filepath.Join(s.facesDir(key), name))
		facesMu.Unlock()
		if rerr != nil {
			return s.fail(c, ErrNotFound("ไม่มีรูปนี้"))
		}
		etag := `"` + versionOf(b) + `"`
		c.Set("ETag", etag)
		c.Set("Cache-Control", "private, no-cache")
		c.Set("X-Content-Type-Options", "nosniff")
		if c.Get("If-None-Match") == etag {
			return c.SendStatus(fiber.StatusNotModified)
		}
		c.Set("Content-Type", "image/png")
		return c.Status(fiber.StatusOK).Send(b)
	}
}

func (s *Service) handleFacePut(session bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		key, err := s.faceKey(c)
		if err != nil {
			return s.fail(c, err)
		}
		name, id, err := faceTarget(c, session)
		if err != nil {
			return s.fail(c, err)
		}
		body := c.Body()
		if err := checkFaceUpload(c.Get("Content-Type"), body); err != nil {
			return s.fail(c, err)
		}
		rpc, params := "aiagent.faceAccountSet", []string{"account", strconv.Itoa(len(body))}
		if session {
			rpc, params = "aiagent.faceSessionSet", []string{"session", id, strconv.Itoa(len(body))}
		}
		return s.mutate(c, rpc, params, func() (any, error) {
			clean, err := cleanFace(body)
			if err != nil {
				return nil, err
			}
			facesMu.Lock()
			defer facesMu.Unlock()
			dir := s.facesDir(key)
			if session {
				if _, err := os.Stat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) && len(sessionFaceIDs(dir)) >= maxSessionFaces {
					return nil, ErrConflict("เก็บรูปของ agent ได้ไม่เกิน 300 รูป ลบบางรูปก่อน")
				}
			}
			if err := s.writeFaceFile(key, name, clean); err != nil {
				return nil, err
			}
			return fiber.Map{"v": versionOf(clean)}, nil
		})
	}
}

func (s *Service) handleFaceDelete(session bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		key, err := s.faceKey(c)
		if err != nil {
			return s.fail(c, err)
		}
		name, id, err := faceTarget(c, session)
		if err != nil {
			return s.fail(c, err)
		}
		rpc, params := "aiagent.faceAccountDelete", []string{"account"}
		if session {
			rpc, params = "aiagent.faceSessionDelete", []string{"session", id}
		}
		return s.mutate(c, rpc, params, func() (any, error) {
			facesMu.Lock()
			defer facesMu.Unlock()
			p := filepath.Join(s.facesDir(key), name)
			delete(faceVers, p)
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			return fiber.Map{}, nil
		})
	}
}

func (s *Service) handleFaceSettings(c fiber.Ctx) error {
	key, err := s.faceKey(c)
	if err != nil {
		return s.fail(c, err)
	}
	var body faceSettings
	if err := json.Unmarshal(c.Body(), &body); err != nil {
		return s.fail(c, ErrBadRequest(`expected {"accountForAll": bool, "characterOnly": ["<sessionId>", …]}`))
	}
	if len(body.CharacterOnly) > maxCharacterOnly {
		return s.fail(c, ErrBadRequest("characterOnly: ไม่เกิน 500 รายการ"))
	}
	ids, seen := []string{}, map[string]bool{}
	for _, id := range body.CharacterOnly {
		if !faceSessionRE.MatchString(id) {
			return s.fail(c, ErrBadRequest("characterOnly: รหัส session ไม่ถูกต้อง"))
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, strings.Clone(id))
		}
	}
	st := faceSettings{AccountForAll: body.AccountForAll, CharacterOnly: ids}
	return s.mutate(c, "aiagent.faceSettings", append([]string{strconv.FormatBool(st.AccountForAll)}, ids...), func() (any, error) {
		b, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return nil, err
		}
		facesMu.Lock()
		defer facesMu.Unlock()
		if err := s.writeFaceFile(key, faceSettingsFile, append(b, '\n')); err != nil {
			return nil, err
		}
		return s.manifest(key), nil
	})
}

// mountFaces registers the photo routes; every role, each owner its own.
func (s *Service) mountFaces(api fiber.Router) {
	api.Get("/faces", s.handleFaces)
	api.Put("/faces/settings", s.handleFaceSettings)
	for _, session := range []bool{false, true} {
		path := "/faces/account"
		if session {
			path = "/faces/sessions/:id"
		}
		api.Get(path, s.handleFaceGet(session))
		api.Put(path, s.handleFacePut(session))
		api.Delete(path, s.handleFaceDelete(session))
	}
}
