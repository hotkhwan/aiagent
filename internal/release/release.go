// Package release checks a published aiagent release before anything in it is
// installed or run.
//
// A release on GitHub ships, per platform, aiagent_<ver>_<os>_<arch>.tar.gz
// (holding exactly the two binaries aiagent and agcmd), one SHA256SUMS over all
// the archives, and SHA256SUMS.sig: a base64 ed25519 signature of SHA256SUMS
// made with the owner's release key (packaging/release.sh). The public half of
// that key is compiled into aiagent, so a release verifies only when it was
// signed by the owner, whoever hosts the files and however they were fetched.
//
// Verify is the one gate: signature over SHA256SUMS, then the archive's hash is
// the one SHA256SUMS lists, then the archive holds the two binaries and nothing
// else. Nothing from a release is written to its final place or run before it.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	SumsFile = "SHA256SUMS"
	SigFile  = "SHA256SUMS.sig"

	// MaxArchive caps a downloaded archive; MaxBinary caps each binary in it.
	MaxArchive = 100 << 20
	MaxBinary  = 160 << 20
	MaxSums    = 64 << 10
	MaxSig     = 1 << 10
)

// Binaries are the files an archive holds, and the only ones it may.
var Binaries = []string{"aiagent", "agcmd"}

var (
	versionRE = regexp.MustCompile(`^v?([0-9]{1,6})\.([0-9]{1,6})\.([0-9]{1,6})$`)
	archiveRE = regexp.MustCompile(`^aiagent_([0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6})_(darwin|linux)_(amd64|arm64)\.tar\.gz$`)
	hashRE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ArchiveName is the file a release ships for one platform.
func ArchiveName(ver, goos, goarch string) string {
	return fmt.Sprintf("aiagent_%s_%s_%s.tar.gz", strings.TrimPrefix(ver, "v"), goos, goarch)
}

// ParseVersion reads MAJOR.MINOR.PATCH, with or without a leading v. Anything
// else (a pre-release, a build suffix) is not a release version.
func ParseVersion(s string) ([3]int, bool) {
	var v [3]int
	m := versionRE.FindStringSubmatch(s)
	if m == nil {
		return v, false
	}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Newer says whether a is a later release than b. Either one not being a
// release version answers false: an unknown version is never an upgrade.
func Newer(a, b string) bool {
	va, ok := ParseVersion(a)
	vb, ok2 := ParseVersion(b)
	if !ok || !ok2 {
		return false
	}
	for i := range va {
		if va[i] != vb[i] {
			return va[i] > vb[i]
		}
	}
	return false
}

// VerifySignature checks sig (base64, as release.sh writes it) over sums.
func VerifySignature(sums, sig []byte, pub ed25519.PublicKey) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("no release key compiled in")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("SHA256SUMS.sig is not an ed25519 signature")
	}
	if !ed25519.Verify(pub, sums, raw) {
		return errors.New("SHA256SUMS is not signed by the aiagent release key")
	}
	return nil
}

// ParseSums reads SHA256SUMS: one "<sha256>  <archive>" per line, only
// archive names a release ships, each once, all of one version.
func ParseSums(sums []byte) (map[string]string, error) {
	out := map[string]string{}
	ver := ""
	for _, line := range strings.Split(strings.TrimRight(string(sums), "\n"), "\n") {
		hash, name, ok := strings.Cut(line, "  ")
		m := archiveRE.FindStringSubmatch(name)
		if !ok || !hashRE.MatchString(hash) || m == nil {
			return nil, fmt.Errorf("SHA256SUMS: unexpected line %q", truncate(line, 120))
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("SHA256SUMS lists %s twice", name)
		}
		if ver != "" && m[1] != ver {
			return nil, errors.New("SHA256SUMS mixes versions")
		}
		ver = m[1]
		out[name] = hash
	}
	if len(out) == 0 {
		return nil, errors.New("SHA256SUMS is empty")
	}
	return out, nil
}

// SumsVersion is the one version every archive in a parsed SHA256SUMS has.
func SumsVersion(sums map[string]string) string {
	for name := range sums {
		return archiveRE.FindStringSubmatch(name)[1]
	}
	return ""
}

// Verified is a release that passed Verify: its version and the two binaries.
type Verified struct {
	Version string
	Files   map[string][]byte
}

// Verify checks the release in dir (SHA256SUMS, SHA256SUMS.sig and this
// platform's archive) and returns the binaries. It writes nothing.
func Verify(dir, goos, goarch string, pub ed25519.PublicKey) (Verified, error) {
	sums, err := readCapped(filepath.Join(dir, SumsFile), MaxSums)
	if err != nil {
		return Verified{}, err
	}
	sig, err := readCapped(filepath.Join(dir, SigFile), MaxSig)
	if err != nil {
		return Verified{}, err
	}
	if err := VerifySignature(sums, sig, pub); err != nil {
		return Verified{}, err
	}
	listed, err := ParseSums(sums)
	if err != nil {
		return Verified{}, err
	}
	ver := SumsVersion(listed)
	name := ArchiveName(ver, goos, goarch)
	want, ok := listed[name]
	if !ok {
		return Verified{}, fmt.Errorf("release %s has no archive for %s/%s", ver, goos, goarch)
	}
	archive, err := readCapped(filepath.Join(dir, name), MaxArchive)
	if err != nil {
		return Verified{}, err
	}
	if err := CheckHash(archive, want); err != nil {
		return Verified{}, fmt.Errorf("%s: %w", name, err)
	}
	files, err := extract(archive, MaxBinary)
	if err != nil {
		return Verified{}, fmt.Errorf("%s: %w", name, err)
	}
	return Verified{Version: ver, Files: files}, nil
}

// CheckHash compares b's sha256 with the hex one SHA256SUMS lists.
func CheckHash(b []byte, want string) error {
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != want {
		return errors.New("sha256 does not match SHA256SUMS")
	}
	return nil
}

// extract reads the two binaries out of a tar.gz. Every entry must be one of
// them, a regular file, at the top level, once, non-empty and at most max
// bytes; anything else refuses the whole archive.
func extract(archive []byte, max int64) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, errors.New("not a tar.gz")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("broken tar")
		}
		if !isBinary(h.Name) {
			return nil, fmt.Errorf("unexpected entry %q", truncate(h.Name, 80))
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("%s is not a regular file", h.Name)
		}
		if _, dup := files[h.Name]; dup {
			return nil, fmt.Errorf("%s appears twice", h.Name)
		}
		if h.Size <= 0 || h.Size > max {
			return nil, fmt.Errorf("%s has an unexpected size", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(tr, max+1))
		if err != nil || int64(len(b)) != h.Size {
			return nil, fmt.Errorf("%s: short read", h.Name)
		}
		files[h.Name] = b
	}
	for _, n := range Binaries {
		if _, ok := files[n]; !ok {
			return nil, fmt.Errorf("archive has no %s", n)
		}
	}
	return files, nil
}

func isBinary(name string) bool {
	for _, n := range Binaries {
		if name == n {
			return true
		}
	}
	return false
}

func readCapped(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("missing %s", filepath.Base(path))
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is too large", filepath.Base(path))
	}
	return b, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
