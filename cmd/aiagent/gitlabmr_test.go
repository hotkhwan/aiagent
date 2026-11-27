package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMRAllowed(t *testing.T) {
	for _, c := range []struct {
		purpose, project, iid string
		ok                    bool
	}{
		{"", "atanywhere/psys", "4", true},
		{"https://gitlab.com/atanywhere", "atanywhere/psys", "4", true},
		{"deploys https://gitlab.com/atanywhere/", "atanywhere/sub/pdeploy", "7", true},
		{"https://gitlab.com/atanywhere", "other/psys", "4", false},
		{"https://gitlab.com/atanywhere", "atanywhere-evil/psys", "4", false},
		{"", "psys", "4", false},
		{"", "a/../b", "4", false},
		{"", "a/b?x=1", "4", false},
		{"", "a/b", "0", false},
		{"", "a/b", "4/../../users", false},
	} {
		if err := mrAllowed(c.purpose, c.project, c.iid); (err == nil) != c.ok {
			t.Errorf("mrAllowed(%q, %q, %q) = %v", c.purpose, c.project, c.iid, err)
		}
	}
}

// TestMRStatus: the token goes in the header only, the output names the state
// and never the token, and a refused token is said plainly.
func TestMRStatus(t *testing.T) {
	const token = "glpat-SECRET-123"
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		if r.Header.Get("PRIVATE-TOKEN") != token || strings.Contains(r.URL.String(), token) {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/approvals") {
			_, _ = w.Write([]byte(`{"approved":false,"approvals_left":1}`))
			return
		}
		_, _ = w.Write([]byte(`{"iid":7,"title":"deploy\nnetra","state":"opened","detailed_merge_status":"not_approved","merge_status":"can_be_merged","has_conflicts":false,"blocking_discussions_resolved":true,"source_branch":"feat/x","target_branch":"develop","web_url":"https://gitlab.com/a/b/-/merge_requests/7","head_pipeline":{"status":"success","web_url":"p"},"user":{"can_merge":true}}`))
	}))
	defer srv.Close()
	out, err := mrStatus(srv.Client(), srv.URL, token, "atanywhere/pdeploy", "7")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"!7 deploy netra", "state: opened", "merge status: not_approved", "approvals: approved false · left 1", "feat/x -> develop", "pipeline: success"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, token) {
		t.Fatal("the token was printed")
	}
	if paths[0] != "/api/v4/projects/atanywhere%2Fpdeploy/merge_requests/7" {
		t.Fatalf("path = %s", paths[0])
	}
	if _, err := mrStatus(srv.Client(), srv.URL, "wrong", "a/b", "7"); err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "wrong") {
		t.Fatalf("refused token: %v", err)
	}
}

func TestMRClientDoesNotFollowRedirects(t *testing.T) {
	hit := false
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = r.Header.Get("PRIVATE-TOKEN") != "" }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusFound)
	}))
	defer srv.Close()
	c := mrClient()
	c.Transport = srv.Client().Transport
	if _, err := mrStatus(c, srv.URL, "tok", "a/b", "1"); err == nil {
		t.Fatal("a redirect was taken as an answer")
	}
	if hit {
		t.Fatal("the token followed a redirect")
	}
}
