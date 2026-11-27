package release

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const maxAPI = 1 << 20

// Client reads releases from GitHub. Every request, and every redirect it
// follows, must be https to one of Hosts; downloads must be the release's own
// download URLs. None of that is what makes a release safe (the signature
// is); it keeps the service from being pointed at anything else.
type Client struct {
	API          string // the latest-release endpoint
	DownloadBase string // <DownloadBase>/<tag>/<asset>
	Hosts        []string
	Base         *http.Client // transport; its CheckRedirect is replaced
	UserAgent    string
}

// Default is the client for github.com/hotkhwan/aiagent.
func Default(version string) *Client {
	return &Client{
		API:          "https://api.github.com/repos/hotkhwan/aiagent/releases/latest",
		DownloadBase: "https://github.com/hotkhwan/aiagent/releases/download",
		// github.com answers a download with a redirect to its asset storage
		Hosts:     []string{"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"},
		UserAgent: "aiagent/" + version,
	}
}

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// Release is what GitHub says about the latest release.
type Release struct {
	Tag        string  `json:"tag_name"`
	Notes      string  `json:"body"`
	URL        string  `json:"html_url"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}

func (c *Client) allowed(u *url.URL) bool {
	return u != nil && u.Scheme == "https" && u.User == nil && slices.Contains(c.Hosts, u.Host)
}

func (c *Client) http(timeout time.Duration) *http.Client {
	hc := &http.Client{Timeout: timeout}
	if c.Base != nil {
		hc.Transport = c.Base.Transport
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !c.allowed(req.URL) {
			return fmt.Errorf("redirect to %s is not allowed", req.URL.Host)
		}
		return nil
	}
	return hc
}

func (c *Client) get(ctx context.Context, raw string, max int64, timeout time.Duration, accept string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || !c.allowed(u) {
		return nil, errors.New("not an allowed release URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.http(timeout).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %d", u.Host, resp.StatusCode)
	}
	if resp.ContentLength > max {
		return nil, errors.New("download is larger than allowed")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("download is larger than allowed")
	}
	return b, nil
}

func (c *Client) download(ctx context.Context, raw string, max int64) ([]byte, error) {
	return c.get(ctx, raw, max, 5*time.Minute, "")
}

// Latest asks GitHub for the latest published release.
func (c *Client) Latest(ctx context.Context) (Release, error) {
	b, err := c.get(ctx, c.API, maxAPI, 20*time.Second, "application/vnd.github+json")
	if err != nil {
		return Release{}, err
	}
	var rel Release
	if err := json.Unmarshal(b, &rel); err != nil {
		return Release{}, errors.New("GitHub answered something that is not a release")
	}
	if _, ok := ParseVersion(rel.Tag); !ok || rel.Tag[0] != 'v' {
		return Release{}, fmt.Errorf("latest release has an unexpected tag %q", truncate(rel.Tag, 40))
	}
	if rel.Draft || rel.Prerelease {
		return Release{}, errors.New("latest release is a draft or a pre-release")
	}
	return rel, nil
}

func (c *Client) assetURL(rel Release, name string) (string, error) {
	want := c.DownloadBase + "/" + rel.Tag + "/" + name
	for _, a := range rel.Assets {
		if a.Name == name {
			if a.URL != want {
				return "", fmt.Errorf("asset %s is not at the release's download URL", name)
			}
			return want, nil
		}
	}
	return "", fmt.Errorf("release %s has no %s", rel.Tag, name)
}

// Fetch downloads rel's SHA256SUMS, its signature and this platform's archive
// into dir (emptied first), checking the signature before the archive is
// fetched, and returns what Verify says of the result.
func (c *Client) Fetch(ctx context.Context, rel Release, dir, goos, goarch string, pub ed25519.PublicKey) (Verified, error) {
	if _, ok := ParseVersion(rel.Tag); !ok {
		return Verified{}, errors.New("not a release tag")
	}
	get := func(name string, max int64) ([]byte, error) {
		u, err := c.assetURL(rel, name)
		if err != nil {
			return nil, err
		}
		b, err := c.download(ctx, u, max)
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", name, err)
		}
		return b, nil
	}
	sums, err := get(SumsFile, MaxSums)
	if err != nil {
		return Verified{}, err
	}
	sig, err := get(SigFile, MaxSig)
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
	if "v"+ver != rel.Tag {
		return Verified{}, fmt.Errorf("release %s carries signed files of %s", rel.Tag, ver)
	}
	name := ArchiveName(ver, goos, goarch)
	want, ok := listed[name]
	if !ok {
		return Verified{}, fmt.Errorf("release %s has no archive for %s/%s", ver, goos, goarch)
	}
	archive, err := get(name, MaxArchive)
	if err != nil {
		return Verified{}, err
	}
	if err := CheckHash(archive, want); err != nil {
		return Verified{}, fmt.Errorf("%s: %w", name, err)
	}
	if err := os.RemoveAll(dir); err != nil {
		return Verified{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Verified{}, err
	}
	for n, b := range map[string][]byte{SumsFile: sums, SigFile: sig, name: archive} {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o600); err != nil {
			return Verified{}, err
		}
	}
	return Verify(dir, goos, goarch, pub)
}
