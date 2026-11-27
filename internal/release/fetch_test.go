package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testClient talks to srv as if it were GitHub: the API, the download host and
// the host downloads redirect to are all srv.
func testClient(srv *httptest.Server) *Client {
	u, _ := url.Parse(srv.URL)
	c := &Client{
		API:          srv.URL + "/repos/hotkhwan/aiagent/releases/latest",
		DownloadBase: srv.URL + "/hotkhwan/aiagent/releases/download",
		Hosts:        []string{u.Host},
		Base:         srv.Client(),
		UserAgent:    "aiagent-test",
	}
	return c
}

func TestHostAllowList(t *testing.T) {
	c := Default("0.35.0")
	for raw, ok := range map[string]bool{
		"https://github.com/hotkhwan/aiagent/releases/download/v1.0.0/SHA256SUMS": true,
		"https://objects.githubusercontent.com/x":                                 true,
		"https://release-assets.githubusercontent.com/x":                          true,
		"https://api.github.com/repos/hotkhwan/aiagent/releases/latest":           true,
		"http://github.com/x":               false,
		"https://evil.example/x":            false,
		"https://github.com.evil.example/x": false,
		"https://user@github.com/x":         false,
		"https://github.com:8443/x":         false,
		"file:///etc/passwd":                false,
	} {
		u, _ := url.Parse(raw)
		if got := c.allowed(u); got != ok {
			t.Errorf("allowed(%s) = %v, want %v", raw, got, ok)
		}
	}
}

func TestLatestReadsTheRelease(t *testing.T) {
	var ua string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Write([]byte(`{"tag_name":"v0.36.0","body":"<b>notes</b>","html_url":"https://github.com/hotkhwan/aiagent/releases/tag/v0.36.0","draft":false,"prerelease":false,
			"assets":[{"name":"SHA256SUMS","size":10,"browser_download_url":"x"}]}`))
	}))
	defer srv.Close()
	rel, err := testClient(srv).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v0.36.0" || rel.Notes != "<b>notes</b>" || len(rel.Assets) != 1 || ua != "aiagent-test" {
		t.Fatalf("rel = %+v ua=%q", rel, ua)
	}
}

func TestLatestRefusesWhatIsNotARelease(t *testing.T) {
	for name, body := range map[string]string{
		"a bad tag":   `{"tag_name":"latest; rm -rf /","assets":[]}`,
		"a draft":     `{"tag_name":"v0.36.0","draft":true,"assets":[]}`,
		"a pre":       `{"tag_name":"v0.36.0","prerelease":true,"assets":[]}`,
		"not json":    `<html>`,
		"too large":   `{"tag_name":"v0.36.0","body":"` + strings.Repeat("a", maxAPI) + `"}`,
		"the release": "",
	} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if body == "" {
				http.Error(w, "rate limited", 403)
				return
			}
			w.Write([]byte(body))
		}))
		if _, err := testClient(srv).Latest(context.Background()); err == nil {
			t.Errorf("%s: must refuse", name)
		}
		srv.Close()
	}
}

func TestDownloadsFollowRedirectsOnlyToAllowedHosts(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			http.Redirect(w, r, "/blob", http.StatusFound)
		case "/blob":
			w.Write([]byte("data"))
		case "/away":
			http.Redirect(w, r, "https://evil.example/blob", http.StatusFound)
		case "/plain":
			http.Redirect(w, r, "http://"+r.Host+"/blob", http.StatusFound)
		case "/big":
			w.Write([]byte(strings.Repeat("a", 100)))
		}
	}))
	defer srv.Close()
	c := testClient(srv)
	if b, err := c.download(context.Background(), srv.URL+"/ok", 50); err != nil || string(b) != "data" {
		t.Fatalf("a redirect on an allowed host: %q %v", b, err)
	}
	for _, p := range []string{"/away", "/plain"} {
		if _, err := c.download(context.Background(), srv.URL+p, 50); err == nil {
			t.Errorf("%s: a redirect off the allowed hosts must be refused", p)
		}
	}
	if _, err := c.download(context.Background(), srv.URL+"/big", 50); err == nil {
		t.Error("a download over its cap must be refused")
	}
	if _, err := c.download(context.Background(), "https://evil.example/blob", 50); err == nil {
		t.Error("a URL off the allowed hosts must be refused before it is fetched")
	}
}

// Fetch downloads one release into a directory and verifies it.
func TestFetchDownloadsAndVerifies(t *testing.T) {
	pub, priv := keypair(t)
	src := releaseDir(t, priv, "0.36.0", goodArchive(t))
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := os.ReadFile(filepath.Join(src, filepath.Base(r.URL.Path)))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer srv.Close()
	c := testClient(srv)
	rel := Release{Tag: "v0.36.0"}
	for _, n := range []string{SumsFile, SigFile, ArchiveName("0.36.0", "linux", "amd64")} {
		rel.Assets = append(rel.Assets, Asset{Name: n, URL: c.DownloadBase + "/v0.36.0/" + n})
	}
	dir := filepath.Join(t.TempDir(), "dl")
	got, err := c.Fetch(context.Background(), rel, dir, "linux", "amd64", pub)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "0.36.0" || len(got.Files) != 2 {
		t.Fatalf("got %+v", got)
	}

	// the release says v0.37.0 but its signed sums are 0.36.0: refused
	if _, err := c.Fetch(context.Background(), Release{Tag: "v0.37.0", Assets: rel.Assets}, filepath.Join(t.TempDir(), "x"), "linux", "amd64", pub); err == nil {
		t.Error("the signed version must be the release's version")
	}
	// an asset URL that is not the release's own download URL: refused
	bad := Release{Tag: "v0.36.0", Assets: append([]Asset(nil), rel.Assets...)}
	bad.Assets[0].URL = srv.URL + "/elsewhere/" + SumsFile
	if _, err := c.Fetch(context.Background(), bad, filepath.Join(t.TempDir(), "y"), "linux", "amd64", pub); err == nil {
		t.Error("asset URLs must be the release's download URLs")
	}
	// signed by someone else: refused
	other, _ := keypair(t)
	if _, err := c.Fetch(context.Background(), rel, filepath.Join(t.TempDir(), "z"), "linux", "amd64", other); err == nil {
		t.Error("a release signed with another key must be refused")
	}
}
