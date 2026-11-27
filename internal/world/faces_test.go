package world

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func faceImage(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	return img
}

func facePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, faceImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// faceJPEGWithExif is a JPEG carrying an APP1 (Exif) segment with a marker a
// leak would show.
func faceJPEGWithExif(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, faceImage(w, h), nil); err != nil {
		t.Fatal(err)
	}
	raw := b.Bytes()
	payload := append([]byte("Exif\x00\x00"), []byte("GPS-SECRET-13.7563N-100.5018E")...)
	seg := []byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}
	seg = append(seg, payload...)
	out := append([]byte{}, raw[:2]...) // SOI
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

type faceResp struct {
	code   int
	header http.Header
	body   []byte
}

func (e *env) raw(t *testing.T, method, path, token, ctype string, body []byte) faceResp {
	t.Helper()
	e.svc.lastWrite = time.Time{} // one write a second is not what these test
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set(TokenHeader, token)
	}
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return faceResp{resp.StatusCode, resp.Header, b}
}

func (e *env) manifest(t *testing.T, token string) faceManifest {
	t.Helper()
	r := e.raw(t, "GET", apiPrefix+"/faces", token, "", nil)
	if r.code != 200 {
		t.Fatalf("manifest = %d %s", r.code, r.body)
	}
	var env struct {
		Code    string       `json:"code"`
		Details faceManifest `json:"details"`
	}
	if err := json.Unmarshal(r.body, &env); err != nil || env.Code != "SUCCESS" {
		t.Fatalf("manifest = %s (%v)", r.body, err)
	}
	if env.Details.CharacterOnly == nil || env.Details.Sessions == nil {
		t.Fatalf("manifest has a null list: %s", r.body)
	}
	return env.Details
}

func putV(t *testing.T, r faceResp) string {
	t.Helper()
	if r.code != 200 {
		t.Fatalf("put = %d %s", r.code, r.body)
	}
	var env struct {
		Details struct {
			V string `json:"v"`
		} `json:"details"`
	}
	if err := json.Unmarshal(r.body, &env); err != nil || len(env.Details.V) != 16 {
		t.Fatalf("put answer = %s", r.body)
	}
	return env.Details.V
}

func facesEnv(t *testing.T) (*env, string, string, string) {
	t.Helper()
	e := newEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, anna, err := e.store.AddUser("anna", "")
	if err != nil {
		t.Fatal(err)
	}
	_, bob, err := e.store.AddUser("bob", "")
	if err != nil {
		t.Fatal(err)
	}
	return e, admin, anna, bob
}

func TestFacesBelongOnlyToTheirOwner(t *testing.T) {
	e, admin, anna, bob := facesEnv(t)
	for _, tok := range []string{admin, anna, bob} {
		if m := e.manifest(t, tok); m.Account != "" || len(m.Sessions) != 0 || m.AccountForAll || len(m.CharacterOnly) != 0 {
			t.Fatalf("a fresh owner has faces: %+v", m)
		}
	}
	va := putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", admin, "image/png", facePNG(t, 40, 40)))
	vn := putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/s-anna-1", anna, "image/png", facePNG(t, 50, 30)))
	if m := e.manifest(t, admin); m.Account != va || len(m.Sessions) != 0 {
		t.Fatalf("admin manifest = %+v", m)
	}
	if m := e.manifest(t, anna); m.Account != "" || m.Sessions["s-anna-1"] != vn || len(m.Sessions) != 1 {
		t.Fatalf("anna manifest = %+v", m)
	}
	if m := e.manifest(t, bob); m.Account != "" || len(m.Sessions) != 0 {
		t.Fatalf("bob sees someone else's faces: %+v", m)
	}
	for _, c := range []struct{ tok, path string }{
		{anna, "/faces/account"}, {bob, "/faces/account"},
		{admin, "/faces/sessions/s-anna-1"}, {bob, "/faces/sessions/s-anna-1"},
	} {
		if r := e.raw(t, "GET", apiPrefix+c.path, c.tok, "", nil); r.code != 404 {
			t.Errorf("GET %s by another owner = %d", c.path, r.code)
		}
	}
	// bob deleting the same id touches only his own (absent) photo
	if r := e.raw(t, "DELETE", apiPrefix+"/faces/sessions/s-anna-1", bob, "", nil); r.code != 200 {
		t.Fatalf("bob delete = %d %s", r.code, r.body)
	}
	if m := e.manifest(t, anna); m.Sessions["s-anna-1"] != vn {
		t.Fatalf("bob's delete removed anna's face: %+v", m)
	}
	if r := e.raw(t, "GET", apiPrefix+"/faces", "", "", nil); r.code != 401 {
		t.Fatalf("no token = %d", r.code)
	}
}

func TestFaceIsServedWithItsVersion(t *testing.T) {
	e, admin, anna, _ := facesEnv(t)
	if r := e.raw(t, "GET", apiPrefix+"/faces/account", anna, "", nil); r.code != 404 {
		t.Fatalf("no account face = %d", r.code)
	}
	v1 := putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", anna, "image/png", facePNG(t, 40, 40)))
	r := e.raw(t, "GET", apiPrefix+"/faces/account", anna, "", nil)
	if r.code != 200 || r.header.Get("Content-Type") != "image/png" || r.header.Get("ETag") != `"`+v1+`"` ||
		r.header.Get("Cache-Control") != "private, no-cache" || !bytes.HasPrefix(r.body, []byte("\x89PNG")) {
		t.Fatalf("GET account = %d %v", r.code, r.header)
	}
	if v1 != versionOf(r.body) {
		t.Fatalf("v %s is not the stored PNG's sha256 prefix %s", v1, versionOf(r.body))
	}
	v2 := putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", anna, "image/png", facePNG(t, 60, 40)))
	if v2 == v1 || e.manifest(t, anna).Account != v2 {
		t.Fatalf("a new photo kept the version: %s %s", v1, v2)
	}
	for i := 0; i < 2; i++ { // idempotent
		if r := e.raw(t, "DELETE", apiPrefix+"/faces/account", anna, "", nil); r.code != 200 {
			t.Fatalf("delete = %d %s", r.code, r.body)
		}
	}
	if m := e.manifest(t, anna); m.Account != "" {
		t.Fatalf("deleted account face is listed: %+v", m)
	}
	if r := e.raw(t, "GET", apiPrefix+"/faces/account", anna, "", nil); r.code != 404 {
		t.Fatalf("deleted face = %d", r.code)
	}
	if r := e.raw(t, "GET", apiPrefix+"/faces/sessions/nobody", admin, "", nil); r.code != 404 {
		t.Fatalf("missing session face = %d", r.code)
	}
}

func TestFaceJPEGComesBackAsPNGWithoutMetadata(t *testing.T) {
	e, _, anna, _ := facesEnv(t)
	in := faceJPEGWithExif(t, 64, 48)
	if !bytes.Contains(in, []byte("GPS-SECRET")) {
		t.Fatal("the fixture has no metadata")
	}
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/abc-123", anna, "image/jpeg", in))
	r := e.raw(t, "GET", apiPrefix+"/faces/sessions/abc-123", anna, "", nil)
	if r.code != 200 || !bytes.HasPrefix(r.body, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("GET = %d, not a PNG", r.code)
	}
	if bytes.Contains(r.body, []byte("GPS-SECRET")) || bytes.Contains(r.body, []byte("Exif")) {
		t.Fatal("metadata survived the re-encode")
	}
	img, err := png.Decode(bytes.NewReader(r.body))
	if err != nil || img.Bounds().Dx() != 64 || img.Bounds().Dy() != 48 {
		t.Fatalf("stored image = %v %v", err, img)
	}
	// and nothing of the original was kept anywhere under the state dir
	_ = filepath.Walk(e.store.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte("GPS-SECRET")) {
				t.Errorf("%s holds the metadata", p)
			}
		}
		return nil
	})
}

func TestFaceIsScaledToAtMost512(t *testing.T) {
	e, admin, _, _ := facesEnv(t)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", admin, "image/png", facePNG(t, 1600, 900)))
	r := e.raw(t, "GET", apiPrefix+"/faces/account", admin, "", nil)
	cfg, err := png.DecodeConfig(bytes.NewReader(r.body))
	if err != nil || cfg.Width != 512 || cfg.Height != 288 {
		t.Fatalf("scaled = %v %+v", err, cfg)
	}
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", admin, "image/png", facePNG(t, 300, 2048)))
	r = e.raw(t, "GET", apiPrefix+"/faces/account", admin, "", nil)
	if cfg, err = png.DecodeConfig(bytes.NewReader(r.body)); err != nil || cfg.Height != 512 || cfg.Width != 75 {
		t.Fatalf("tall scaled = %v %+v", err, cfg)
	}
}

func TestFaceUploadsThatAreRefused(t *testing.T) {
	e, admin, _, _ := facesEnv(t)
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	big := append(facePNG(t, 16, 16), make([]byte, 2<<20)...)
	for _, c := range []struct {
		name, ctype string
		body        []byte
		want        int
	}{
		{"text type", "text/plain", facePNG(t, 40, 40), 415},
		{"no type", "", facePNG(t, 40, 40), 415},
		{"gif named png", "image/png", gif, 400},
		{"garbage", "image/jpeg", []byte("not an image at all"), 400},
		{"empty", "image/png", []byte{}, 400},
		{"too wide", "image/png", facePNG(t, 2049, 20), 400},
		{"too small", "image/png", facePNG(t, 15, 100), 400},
		{"over 2 MB", "image/png", big, 413},
	} {
		if r := e.raw(t, "PUT", apiPrefix+"/faces/account", admin, c.ctype, c.body); r.code != c.want {
			t.Errorf("%s = %d %s, want %d", c.name, r.code, r.body, c.want)
		}
	}
	if m := e.manifest(t, admin); m.Account != "" {
		t.Fatalf("a refused upload was stored: %+v", m)
	}
	if r := e.raw(t, "PUT", apiPrefix+"/faces/account", admin, "image/png; charset=binary", facePNG(t, 16, 16)); r.code != 200 {
		t.Fatalf("16 px with a type parameter = %d %s", r.code, r.body)
	}
}

func TestFaceSessionIDsAreChecked(t *testing.T) {
	e, admin, _, _ := facesEnv(t)
	for _, id := range []string{"..", "a.b", "..%2F..%2Fstate", "a%2Fb", "a_b", strings.Repeat("a", 65), "%2E%2E"} {
		for _, m := range []string{"PUT", "GET", "DELETE"} {
			var body []byte
			if m == "PUT" {
				body = facePNG(t, 20, 20)
			}
			// a GET of ".." is cleaned to the manifest, which is no file
			if r := e.raw(t, m, apiPrefix+"/faces/sessions/"+id, admin, "image/png", body); r.code == 200 && (m != "GET" || bytes.HasPrefix(r.body, []byte("\x89PNG"))) {
				t.Errorf("%s %q was accepted", m, id)
			}
		}
	}
	if m := e.manifest(t, admin); len(m.Sessions) != 0 {
		t.Fatalf("a bad id was stored: %+v", m)
	}
	var stray []string
	_ = filepath.Walk(e.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".png") {
			stray = append(stray, p)
		}
		return nil
	})
	if len(stray) != 0 {
		t.Fatalf("files written: %v", stray)
	}
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/"+strings.Repeat("a", 64), admin, "image/png", facePNG(t, 20, 20)))
}

func TestFaceSessionPhotosAreCapped(t *testing.T) {
	e, _, anna, _ := facesEnv(t)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/first", anna, "image/png", facePNG(t, 20, 20)))
	dir := e.svc.facesDir(identity2key(t, e, anna))
	src, err := os.ReadFile(filepath.Join(dir, "s_first.png"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxSessionFaces; i++ {
		if err := os.WriteFile(filepath.Join(dir, "s_x"+strconv.Itoa(i)+".png"), src, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if m := e.manifest(t, anna); len(m.Sessions) != maxSessionFaces {
		t.Fatalf("listed %d", len(m.Sessions))
	}
	if r := e.raw(t, "PUT", apiPrefix+"/faces/sessions/one-too-many", anna, "image/png", facePNG(t, 20, 20)); r.code != 409 {
		t.Fatalf("301st = %d %s", r.code, r.body)
	}
	// replacing one already there is not a new one
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/first", anna, "image/png", facePNG(t, 30, 20)))
	// and the account photo does not count
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", anna, "image/png", facePNG(t, 30, 20)))
}

// identity2key is the owner key a token's photos are kept under.
func identity2key(t *testing.T, e *env, token string) string {
	t.Helper()
	id, ok := e.store.Identify(token)
	if !ok {
		t.Fatal("unknown token")
	}
	k, ok := faceOwner(id)
	if !ok {
		t.Fatal("no owner key")
	}
	return k
}

func TestFaceSettings(t *testing.T) {
	e, admin, anna, _ := facesEnv(t)
	r := e.raw(t, "PUT", apiPrefix+"/faces/settings", anna, "application/json", []byte(`{"accountForAll":true,"characterOnly":["s-1","s-2","s-1"]}`))
	if r.code != 200 || !strings.Contains(string(r.body), `"accountForAll":true`) || !strings.Contains(string(r.body), `"sessions":{}`) {
		t.Fatalf("settings = %d %s", r.code, r.body)
	}
	if m := e.manifest(t, anna); !m.AccountForAll || strings.Join(m.CharacterOnly, ",") != "s-1,s-2" {
		t.Fatalf("anna manifest = %+v", m)
	}
	if m := e.manifest(t, admin); m.AccountForAll || len(m.CharacterOnly) != 0 {
		t.Fatalf("admin got anna's settings: %+v", m)
	}
	many := make([]string, 501)
	for i := range many {
		many[i] = `"s` + strconv.Itoa(i) + `"`
	}
	for _, body := range []string{
		`{"accountForAll":true,"characterOnly":["../x"]}`,
		`{"accountForAll":true,"characterOnly":[""]}`,
		`{"characterOnly":[` + strings.Join(many, ",") + `]}`,
		`{"accountForAll":"yes"}`,
		`not json`,
	} {
		if r := e.raw(t, "PUT", apiPrefix+"/faces/settings", anna, "application/json", []byte(body)); r.code != 400 {
			t.Errorf("%.40s = %d %s", body, r.code, r.body)
		}
	}
	if m := e.manifest(t, anna); !m.AccountForAll || len(m.CharacterOnly) != 2 {
		t.Fatalf("a refused change was kept: %+v", m)
	}
	if r := e.raw(t, "PUT", apiPrefix+"/faces/settings", anna, "application/json", []byte(`{"accountForAll":false,"characterOnly":[]}`)); r.code != 200 {
		t.Fatalf("clear = %d", r.code)
	}
	if m := e.manifest(t, anna); m.AccountForAll || len(m.CharacterOnly) != 0 {
		t.Fatalf("not cleared: %+v", m)
	}
}

func TestFaceWritesAreAuditedWithoutTheImage(t *testing.T) {
	e, _, anna, _ := facesEnv(t)
	body := facePNG(t, 40, 40)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", anna, "image/png", body))
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/s-9", anna, "image/jpeg", faceJPEGWithExif(t, 20, 20)))
	e.raw(t, "DELETE", apiPrefix+"/faces/sessions/s-9", anna, "", nil)
	e.raw(t, "PUT", apiPrefix+"/faces/settings", anna, "application/json", []byte(`{"accountForAll":true,"characterOnly":[]}`))
	stored := e.raw(t, "GET", apiPrefix+"/faces/account", anna, "", nil).body
	log, err := os.ReadFile(e.auditFP)
	if err != nil {
		t.Fatal(err)
	}
	for _, rpc := range []string{"aiagent.faceAccountSet", "aiagent.faceSessionSet", "aiagent.faceSessionDelete", "aiagent.faceSettings"} {
		if !strings.Contains(string(log), `"`+rpc+`"`) {
			t.Errorf("no %s in the audit log", rpc)
		}
	}
	for _, leak := range [][]byte{[]byte("PNG"), []byte("GPS-SECRET"), []byte(base64.StdEncoding.EncodeToString(body[:24])), []byte(base64.StdEncoding.EncodeToString(stored[:24]))} {
		if bytes.Contains(log, leak) {
			t.Errorf("audit log holds image bytes %q", leak)
		}
	}
}

func TestFaceWritesAreRateLimited(t *testing.T) {
	e, admin, _, _ := facesEnv(t)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", admin, "image/png", facePNG(t, 20, 20)))
	req := httptest.NewRequest("PUT", apiPrefix+"/faces/account", bytes.NewReader(facePNG(t, 20, 20)))
	req.Header.Set("Content-Type", "image/png")
	req.Header.Set(TokenHeader, admin)
	resp, err := e.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 429 {
		t.Fatalf("second write in the same second = %d", resp.StatusCode)
	}
}

func TestFaceFilesArePrivate(t *testing.T) {
	e, _, anna, _ := facesEnv(t)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/s1", anna, "image/png", facePNG(t, 20, 20)))
	e.raw(t, "PUT", apiPrefix+"/faces/settings", anna, "application/json", []byte(`{"accountForAll":true,"characterOnly":["s1"]}`))
	dir := e.svc.facesDir(identity2key(t, e, anna))
	for _, p := range []string{filepath.Dir(dir), dir} {
		if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s = %v %v", p, st.Mode(), err)
		}
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 2 {
		t.Fatalf("faces dir holds %v", ents)
	}
	for _, en := range ents {
		st, _ := os.Stat(filepath.Join(dir, en.Name()))
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s = %v", en.Name(), st.Mode())
		}
	}
}

func TestRevokedUsersFacesAreDeleted(t *testing.T) {
	e, admin, anna, _ := facesEnv(t)
	putV(t, e.raw(t, "PUT", apiPrefix+"/faces/account", anna, "image/png", facePNG(t, 20, 20)))
	key := identity2key(t, e, anna)
	dir := e.svc.facesDir(key)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
	if r := e.raw(t, "POST", apiPrefix+"/tokens/"+key+"/revoke", admin, "application/json", []byte(`{}`)); r.code != 200 {
		t.Fatalf("revoke = %d %s", r.code, r.body)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("revoked user's faces remain: %v", err)
	}
}

func TestFacesStayOnTheMainServiceInRooms(t *testing.T) {
	e, _, o := osUserEnv(t)
	admin, err := e.store.Enable("t")
	if err != nil {
		t.Fatal(err)
	}
	_, user, err := e.store.AddUser("vibe-a", "vibe-a")
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	instance(t, o, "vibe-a", func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"code":"SUCCESS","details":{}}`))
	})
	v := putV(t, e.raw(t, "PUT", apiPrefix+"/faces/sessions/room-s1", user, "image/png", facePNG(t, 20, 20)))
	if m := e.manifest(t, user); m.Sessions["room-s1"] != v {
		t.Fatalf("user manifest = %+v", m)
	}
	if r := e.raw(t, "PUT", apiPrefix+"/faces/settings", user, "application/json", []byte(`{"accountForAll":true,"characterOnly":[]}`)); r.code != 200 {
		t.Fatalf("settings = %d %s", r.code, r.body)
	}
	if r := e.raw(t, "GET", apiPrefix+"/faces/sessions/room-s1", user, "", nil); r.code != 200 {
		t.Fatalf("get = %d", r.code)
	}
	if r := e.raw(t, "DELETE", apiPrefix+"/faces/account", user, "", nil); r.code != 200 {
		t.Fatalf("delete = %d", r.code)
	}
	// an admin looking at a room keeps the admin's own faces
	req := httptest.NewRequest("GET", apiPrefix+"/faces", nil)
	req.Header.Set(TokenHeader, admin)
	req.Header.Set(AsHeader, "vibe-a")
	resp, err := e.app.Test(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("admin as room = %v %v", resp, err)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "room-s1") {
		t.Fatalf("admin sees the user's faces: %s", b)
	}
	if len(seen) != 0 {
		t.Fatalf("faces were forwarded to the room: %v", seen)
	}
}
