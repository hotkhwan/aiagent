package world

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hotkhwan/aiagent/internal/release"
)

// fakeGitHub serves one release: the API answer and its assets, signed with
// priv, for this machine's platform.
type fakeGitHub struct {
	srv   *httptest.Server
	calls atomic.Int32
	tag   string
}

func newFakeGitHub(t *testing.T, priv ed25519.PrivateKey, ver string) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{tag: "v" + ver}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range release.Binaries {
		body := []byte("binary " + n + " " + ver)
		_ = tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(body)
	}
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	name := release.ArchiveName(ver, runtime.GOOS, runtime.GOARCH)
	h := sha256.Sum256(archive)
	sums := []byte(hex.EncodeToString(h[:]) + "  " + name + "\n")
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n")
	files := map[string][]byte{release.SumsFile: sums, release.SigFile: sig, name: archive}
	g.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			g.calls.Add(1)
			var assets []string
			for n := range files {
				assets = append(assets, `{"name":"`+n+`","browser_download_url":"`+g.srv.URL+`/dl/`+g.tag+`/`+n+`"}`)
			}
			w.Write([]byte(`{"tag_name":"` + g.tag + `","body":"<script>alert(1)</script> notes","html_url":"https://github.com/hotkhwan/aiagent/releases/tag/` + g.tag + `","assets":[` + strings.Join(assets, ",") + `]}`))
			return
		}
		if b, ok := files[filepath.Base(r.URL.Path)]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) client() *release.Client {
	u, _ := url.Parse(g.srv.URL)
	return &release.Client{API: g.srv.URL + "/repos/hotkhwan/aiagent/releases/latest", DownloadBase: g.srv.URL + "/dl", Hosts: []string{u.Host}, Base: g.srv.Client(), UserAgent: "t"}
}

func testKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// The bell asks GitHub at most every few hours; an admin may ask now.
func TestReleaseCheckIsCached(t *testing.T) {
	_, priv := testKey(t)
	g := newFakeGitHub(t, priv, "99.0.0")
	e := newEnv(t)
	e.svc.release.client = g.client()
	admin, _ := e.store.Enable("test")
	for range 3 {
		code, m, _ := e.do(t, "GET", "/aiagent/api/update/release", admin, "")
		if code != 200 {
			t.Fatalf("check: %d %v", code, m)
		}
		r := m["details"].(map[string]any)["release"].(map[string]any)
		if r["latest"] != "v99.0.0" || r["newer"] != true || !strings.Contains(r["notes"].(string), "<script>") {
			t.Fatalf("release = %v", r)
		}
	}
	if n := g.calls.Load(); n != 1 {
		t.Fatalf("GitHub asked %d times, want once", n)
	}
	e.svc.release.at = time.Now().Add(-releaseCheckTTL - time.Minute)
	e.do(t, "GET", "/aiagent/api/update/release", admin, "")
	if n := g.calls.Load(); n != 2 {
		t.Fatalf("an old answer must be asked again: %d", n)
	}
	e.svc.release.at = time.Now().Add(-releaseFreshGap - time.Second)
	e.do(t, "GET", "/aiagent/api/update/release?fresh=1", admin, "")
	if n := g.calls.Load(); n != 3 {
		t.Fatalf("an admin's fresh check asks again: %d", n)
	}
	// and the full update answer carries it too
	_, m, _ := e.do(t, "GET", "/aiagent/api/update", admin, "")
	if r, _ := m["details"].(map[string]any)["release"].(map[string]any); r["latest"] != "v99.0.0" {
		t.Fatalf("GET /update has no release: %v", m)
	}
}

func TestReleaseRoutesAreAdminOnly(t *testing.T) {
	e := newEnv(t)
	e.store.Enable("test")
	_, user, _ := e.store.AddUser("nok", "")
	if code, _, _ := e.do(t, "GET", "/aiagent/api/update/release", user, ""); code != 403 {
		t.Fatalf("a user reads the release check: %d", code)
	}
	if code, _, _ := e.do(t, "POST", "/aiagent/api/update", user, `{"release":"v99.0.0"}`); code != 403 {
		t.Fatalf("a user starts a release update: %d", code)
	}
}

func TestReleaseApplyRefusesWhatIsNotAnUpgrade(t *testing.T) {
	_, priv := testKey(t)
	g := newFakeGitHub(t, priv, Version) // the latest release is this one
	e := newEnv(t)
	e.svc.release.client = g.client()
	started := false
	e.svc.release.launch = func(string, ...string) error { started = true; return nil }
	admin, _ := e.store.Enable("test")
	for body, want := range map[string]int{
		`{"release":"v` + Version + `"}`: 409, // equal
		`{"release":"v0.0.1"}`:           409, // older
		`{"release":"v99.0.0"}`:          409, // not the latest release
		`{"release":"latest"}`:           400,
		`{"release":"v1.0.0; rm -rf /"}`: 400,
	} {
		time.Sleep(writeSpacing)
		if code, m, _ := e.do(t, "POST", "/aiagent/api/update", admin, body); code != want {
			t.Errorf("%s: %d %v, want %d", body, code, m, want)
		}
	}
	if started {
		t.Fatal("no update may start")
	}
}

// The whole apply, against a fake GitHub: download, verify, then hand the
// verified folder to `aiagent self-update --release-dir`.
func TestReleaseApplyVerifiesThenHandsOff(t *testing.T) {
	pub, priv := testKey(t)
	g := newFakeGitHub(t, priv, "99.0.0")
	e := newEnv(t)
	e.svc.release.client = g.client()
	e.svc.release.key = pub
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	e.svc.release.launch = func(to string, args ...string) error {
		mu.Lock()
		got = append([]string{to}, args...)
		mu.Unlock()
		close(done)
		return nil
	}
	admin, _ := e.store.Enable("test")
	code, m, _ := e.do(t, "POST", "/aiagent/api/update", admin, `{"release":"v99.0.0"}`)
	if code != 200 {
		t.Fatalf("apply: %d %v", code, m)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("never handed off; status %+v", readUpdateStatus(e.store.dir))
	}
	mu.Lock()
	defer mu.Unlock()
	dir := filepath.Join(e.store.dir, releaseDownloadDir)
	if got[0] != "99.0.0" || !slices.Contains(got, "--release-dir") || !slices.Contains(got, dir) || got[1] != "self-update" {
		t.Fatalf("handed off %v", got)
	}
	if _, err := release.Verify(dir, runtime.GOOS, runtime.GOARCH, pub); err != nil {
		t.Fatalf("the handed-off folder must verify: %v", err)
	}
	b, _ := os.ReadFile(e.auditFP)
	if !strings.Contains(string(b), "aiagent.update.release") {
		t.Fatal("the apply must be audited")
	}
}

// A release signed with another key stops before anything is handed off.
func TestReleaseApplyStopsOnABadSignature(t *testing.T) {
	_, priv := testKey(t)
	other, _ := testKey(t)
	g := newFakeGitHub(t, priv, "99.0.0")
	e := newEnv(t)
	e.svc.release.client = g.client()
	e.svc.release.key = other
	launched := make(chan struct{}, 1)
	e.svc.release.launch = func(string, ...string) error { launched <- struct{}{}; return nil }
	admin, _ := e.store.Enable("test")
	if code, m, _ := e.do(t, "POST", "/aiagent/api/update", admin, `{"release":"v99.0.0"}`); code != 200 {
		t.Fatalf("apply: %d %v", code, m)
	}
	deadline := time.Now().Add(10 * time.Second)
	for readUpdateStatus(e.store.dir).Running && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	st := readUpdateStatus(e.store.dir)
	if st.Running || st.OK || !strings.Contains(st.Log, "signed") {
		t.Fatalf("status = %+v", st)
	}
	select {
	case <-launched:
		t.Fatal("an unsigned release was handed off")
	default:
	}
}

func TestInstallReleaseKeepsTheOldBinaries(t *testing.T) {
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "aiagent"), "old aiagent")
	writeFile(t, filepath.Join(bin, "agcmd"), "old agcmd")
	var signed []string
	err := installRelease(map[string][]byte{"aiagent": []byte("new aiagent"), "agcmd": []byte("new agcmd")}, bin, func(path, name string) error {
		signed = append(signed, name)
		if filepath.Dir(path) != bin || filepath.Base(path) == name {
			t.Errorf("sign the staged copy, not the live one: %s", path)
		}
		return nil
	}, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"aiagent", "agcmd"} {
		if b, _ := os.ReadFile(filepath.Join(bin, n)); string(b) != "new "+n {
			t.Errorf("%s = %q", n, b)
		}
		if b, _ := os.ReadFile(filepath.Join(bin, n+".prev")); string(b) != "old "+n {
			t.Errorf("%s.prev = %q", n, b)
		}
		fi, _ := os.Stat(filepath.Join(bin, n))
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("%s mode %v", n, fi.Mode())
		}
	}
	if len(signed) != 2 {
		t.Fatalf("signed %v", signed)
	}
	left, _ := os.ReadDir(bin)
	if len(left) != 4 {
		t.Fatalf("staged files left behind: %v", left)
	}
}

func TestInstallReleaseRefusesAHalfRelease(t *testing.T) {
	bin := t.TempDir()
	if err := installRelease(map[string][]byte{"aiagent": []byte("x")}, bin, nil, func(string, ...any) {}); err == nil {
		t.Fatal("both binaries or none")
	}
	if left, _ := os.ReadDir(bin); len(left) != 0 {
		t.Fatalf("wrote %v", left)
	}
}

func TestUnitFromCgroup(t *testing.T) {
	for in, want := range map[string]string{
		"0::/system.slice/system-aiagent.slice/aiagent@root.service\n": "aiagent@root.service",
		"0::/system.slice/aiagent@nok.service\n":                       "aiagent@nok.service",
		"0::/user.slice/user-1000.slice/session-3.scope\n":             "",
		"0::/system.slice/evil;rm.service\n":                           "",
		"":                                                             "",
	} {
		if got := unitFromCgroup(in); got != want {
			t.Errorf("unitFromCgroup(%q) = %q, want %q", in, got, want)
		}
	}
}

// The compiled-in key is a real ed25519 key and the PEM next to the install
// docs is the same key.
func TestTheReleaseKeyIsCompiledIn(t *testing.T) {
	k := ReleasePublicKey()
	if len(k) != ed25519.PublicKeySize {
		t.Fatal("no release key compiled in")
	}
	b, err := os.ReadFile("../../packaging/release-pubkey.pem")
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		t.Fatal("packaging/release-pubkey.pem is not PEM")
	}
	p, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil || !bytes.Equal(p.(ed25519.PublicKey), k) {
		t.Fatalf("packaging/release-pubkey.pem is not the compiled-in key: %v", err)
	}
	script, err := os.ReadFile("../../packaging/install-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(script, bytes.TrimSpace(b)) {
		t.Fatal("packaging/install-release.sh checks downloads with another key")
	}
}
