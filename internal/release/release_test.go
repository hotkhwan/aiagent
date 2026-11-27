package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type entry struct {
	name string
	body []byte
	typ  byte
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: typ}
		if typ != tar.TypeReg {
			h.Size = 0
			h.Linkname = "/etc/passwd"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodArchive(t *testing.T) []byte {
	return tarGz(t, entry{name: "aiagent", body: []byte("#!aiagent")}, entry{name: "agcmd", body: []byte("#!agcmd")})
}

func sumLine(name string, b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:]) + "  " + name + "\n"
}

func keypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func sign(priv ed25519.PrivateKey, sums []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums)) + "\n")
}

// releaseDir writes what a release ships for one platform, signed with priv.
func releaseDir(t *testing.T, priv ed25519.PrivateKey, ver string, archive []byte) string {
	t.Helper()
	dir := t.TempDir()
	name := ArchiveName(ver, "linux", "amd64")
	sums := sumLine(name, archive) + sumLine(ArchiveName(ver, "darwin", "arm64"), []byte("other"))
	write(t, filepath.Join(dir, name), archive)
	write(t, filepath.Join(dir, SumsFile), []byte(sums))
	write(t, filepath.Join(dir, SigFile), sign(priv, []byte(sums)))
	return dir
}

func write(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAGoodReleaseVerifies(t *testing.T) {
	pub, priv := keypair(t)
	dir := releaseDir(t, priv, "0.36.0", goodArchive(t))
	got, err := Verify(dir, "linux", "amd64", pub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.36.0" || string(got.Files["aiagent"]) != "#!aiagent" || string(got.Files["agcmd"]) != "#!agcmd" {
		t.Fatalf("verified = %+v", got)
	}
}

func TestVerifyRefusesWhatWasNotSigned(t *testing.T) {
	pub, priv := keypair(t)
	otherPub, _ := keypair(t)
	cases := map[string]func(dir string) ed25519.PublicKey{
		"tampered sums": func(dir string) ed25519.PublicKey {
			p := filepath.Join(dir, SumsFile)
			b, _ := os.ReadFile(p)
			write(t, p, bytes.Replace(b, []byte("0"), []byte("1"), 1))
			return pub
		},
		"tampered archive": func(dir string) ed25519.PublicKey {
			p := filepath.Join(dir, ArchiveName("0.36.0", "linux", "amd64"))
			write(t, p, tarGz(t, entry{name: "aiagent", body: []byte("evil")}, entry{name: "agcmd", body: []byte("evil")}))
			return pub
		},
		"wrong key": func(string) ed25519.PublicKey { return otherPub },
		"no signature": func(dir string) ed25519.PublicKey {
			os.Remove(filepath.Join(dir, SigFile))
			return pub
		},
		"garbage signature": func(dir string) ed25519.PublicKey {
			write(t, filepath.Join(dir, SigFile), []byte("not base64!"))
			return pub
		},
		"no key": func(string) ed25519.PublicKey { return nil },
		"no archive for this machine": func(dir string) ed25519.PublicKey {
			os.Remove(filepath.Join(dir, ArchiveName("0.36.0", "linux", "amd64")))
			return pub
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			dir := releaseDir(t, priv, "0.36.0", goodArchive(t))
			key := mutate(dir)
			if got, err := Verify(dir, "linux", "amd64", key); err == nil {
				t.Fatalf("must refuse, got %+v", got)
			}
		})
	}
}

func TestVerifyRefusesSumsThatMixVersions(t *testing.T) {
	pub, priv := keypair(t)
	dir := t.TempDir()
	a := goodArchive(t)
	sums := sumLine(ArchiveName("0.36.0", "linux", "amd64"), a) + sumLine(ArchiveName("0.37.0", "linux", "arm64"), a)
	write(t, filepath.Join(dir, ArchiveName("0.36.0", "linux", "amd64")), a)
	write(t, filepath.Join(dir, SumsFile), []byte(sums))
	write(t, filepath.Join(dir, SigFile), sign(priv, []byte(sums)))
	if _, err := Verify(dir, "linux", "amd64", pub); err == nil {
		t.Fatal("one release has one version")
	}
}

func TestParseSumsIsStrict(t *testing.T) {
	h := strings.Repeat("a", 64)
	for name, sums := range map[string]string{
		"a path":          h + "  ../aiagent_0.36.0_linux_amd64.tar.gz\n",
		"another file":    h + "  install.sh\n",
		"short hash":      "abc  " + ArchiveName("0.36.0", "linux", "amd64") + "\n",
		"listed twice":    h + "  " + ArchiveName("0.36.0", "linux", "amd64") + "\n" + h + "  " + ArchiveName("0.36.0", "linux", "amd64") + "\n",
		"empty":           "",
		"odd platform":    h + "  aiagent_0.36.0_windows_amd64.tar.gz\n",
		"version not x.y": h + "  aiagent_0.36_linux_amd64.tar.gz\n",
	} {
		if _, err := ParseSums([]byte(sums)); err == nil {
			t.Errorf("%s: must refuse %q", name, sums)
		}
	}
	if m, err := ParseSums([]byte(h + "  " + ArchiveName("0.36.0", "linux", "amd64") + "\n")); err != nil || m[ArchiveName("0.36.0", "linux", "amd64")] != h {
		t.Fatalf("good sums: %v %v", m, err)
	}
}

func TestExtractTakesOnlyTheTwoBinaries(t *testing.T) {
	ok := []byte("x")
	cases := map[string][]byte{
		"path traversal":  tarGz(t, entry{name: "../aiagent", body: ok}, entry{name: "agcmd", body: ok}),
		"absolute path":   tarGz(t, entry{name: "/usr/local/bin/aiagent", body: ok}, entry{name: "agcmd", body: ok}),
		"in a folder":     tarGz(t, entry{name: "bin/aiagent", body: ok}, entry{name: "agcmd", body: ok}),
		"an extra file":   tarGz(t, entry{name: "aiagent", body: ok}, entry{name: "agcmd", body: ok}, entry{name: "evil.sh", body: ok}),
		"a symlink":       tarGz(t, entry{name: "aiagent", typ: tar.TypeSymlink}, entry{name: "agcmd", body: ok}),
		"twice":           tarGz(t, entry{name: "aiagent", body: ok}, entry{name: "aiagent", body: ok}, entry{name: "agcmd", body: ok}),
		"one missing":     tarGz(t, entry{name: "aiagent", body: ok}),
		"empty binary":    tarGz(t, entry{name: "aiagent", body: nil}, entry{name: "agcmd", body: ok}),
		"not a tar.gz":    []byte("plain text"),
		"oversize binary": tarGz(t, entry{name: "aiagent", body: bytes.Repeat([]byte("a"), 2048)}, entry{name: "agcmd", body: ok}),
	}
	for name, archive := range cases {
		if files, err := extract(archive, 1024); err == nil {
			t.Errorf("%s: must refuse, got %d files", name, len(files))
		}
	}
	files, err := extract(goodArchive(t), 1024)
	if err != nil || len(files) != 2 {
		t.Fatalf("good archive: %v %v", files, err)
	}
}

func TestVersionCompare(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"0.36.0", "0.35.0", true},
		{"v0.36.0", "0.35.9", true},
		{"0.35.10", "0.35.9", true},
		{"1.0.0", "0.99.99", true},
		{"0.35.0", "0.35.0", false},
		{"0.34.9", "0.35.0", false},
		{"0.36.0-rc1", "0.35.0", false}, // not a release version: never newer
		{"0.36", "0.35.0", false},
		{"", "0.35.0", false},
		{"0.36.0", "garbage", false},
	} {
		if got := Newer(c.a, c.b); got != c.newer {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.newer)
		}
	}
}

func TestArchiveName(t *testing.T) {
	if got := ArchiveName("0.36.0", "darwin", "arm64"); got != "aiagent_0.36.0_darwin_arm64.tar.gz" {
		t.Fatal(got)
	}
}
