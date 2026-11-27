package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hotkhwan/aiagent/internal/world"
)

// gitlabMR reads one merge request's state with a GitLab token from the vault
// and prints the state only:
//
//	aiagent gitlab-mr --name GitlabAtanyware atanywhere/psys 4
//
// The token goes into one request header to gitlab.com and nowhere else: it is
// never printed, logged or put in a URL, so an agent can see why a merge
// request is stuck without holding the token (git-credential hands it over).
// It only reads. When the vault entry's purpose names a group URL
// (https://gitlab.com/<group>), only projects in that group are asked about.
func gitlabMR(args []string) {
	fs := flag.NewFlagSet("aiagent gitlab-mr", flag.ExitOnError)
	stateDir := stateDirFlag(fs)
	name := fs.String("name", "", "vault entry to use (a GitLab token)")
	_ = fs.Parse(args)
	if *name == "" || fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: aiagent gitlab-mr --name VAULT_NAME GROUP/PROJECT IID")
		os.Exit(2)
	}
	dir := *stateDir
	if dir == "" {
		dir = world.DefaultStateDir()
	}
	svc := &world.Service{Store: world.NewStore(dir)}
	list, err := svc.Vault()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiagent: cannot read the vault")
		os.Exit(1)
	}
	var entry *world.VaultEntry
	for i := range list {
		if list[i].Name == *name {
			entry = &list[i]
		}
	}
	if entry == nil || entry.Provider != "gitlab" {
		fmt.Fprintln(os.Stderr, "aiagent: no GitLab token with that name in the vault")
		os.Exit(1)
	}
	project, iid := fs.Arg(0), fs.Arg(1)
	if err := mrAllowed(entry.Purpose, project, iid); err != nil {
		fmt.Fprintln(os.Stderr, "aiagent:", err)
		os.Exit(2)
	}
	token, err := svc.VaultValue(*name)
	if err != nil || token == "" {
		fmt.Fprintln(os.Stderr, "aiagent: the vault has no value for that name")
		os.Exit(1)
	}
	out, err := mrStatus(mrClient(), "https://gitlab.com", token, project, iid)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aiagent:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

var (
	mrProjectRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(/[A-Za-z0-9][A-Za-z0-9._-]*){1,5}$`)
	mrIIDRE     = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)
	mrGroupRE   = regexp.MustCompile(`^https://gitlab\.com/([A-Za-z0-9][A-Za-z0-9._/-]*?)/?$`)
)

// mrAllowed checks the arguments, and the project against the group the
// token's purpose names, if it names one.
func mrAllowed(purpose, project, iid string) error {
	if !mrProjectRE.MatchString(project) || strings.Contains(project, "..") {
		return errors.New("the project must look like group/project")
	}
	if !mrIIDRE.MatchString(iid) {
		return errors.New("the merge request number must be a number")
	}
	for _, f := range strings.Fields(purpose) {
		if m := mrGroupRE.FindStringSubmatch(f); m != nil {
			if g := strings.ToLower(m[1]); !strings.HasPrefix(strings.ToLower(project)+"/", g+"/") {
				return fmt.Errorf("this token is for %s/…, not %s", m[1], project)
			}
		}
	}
	return nil
}

func mrClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		// a redirect would carry the header to wherever it points
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// mrStatus asks base's API about one merge request (and its approvals) and
// returns the lines to print. Nothing in it holds the token.
func mrStatus(c *http.Client, base, token, project, iid string) (string, error) {
	path := base + "/api/v4/projects/" + url.PathEscape(project) + "/merge_requests/" + iid
	var mr struct {
		IID                 int    `json:"iid"`
		Title               string `json:"title"`
		State               string `json:"state"`
		Draft               bool   `json:"draft"`
		DetailedMergeStatus string `json:"detailed_merge_status"`
		MergeStatus         string `json:"merge_status"`
		HasConflicts        bool   `json:"has_conflicts"`
		BlockingResolved    bool   `json:"blocking_discussions_resolved"`
		SourceBranch        string `json:"source_branch"`
		TargetBranch        string `json:"target_branch"`
		MergeError          string `json:"merge_error"`
		MergedAt            string `json:"merged_at"`
		WebURL              string `json:"web_url"`
		HeadPipeline        *struct {
			Status string `json:"status"`
			WebURL string `json:"web_url"`
		} `json:"head_pipeline"`
		User struct {
			CanMerge bool `json:"can_merge"`
		} `json:"user"`
	}
	if err := mrGet(c, path, token, &mr); err != nil {
		return "", err
	}
	var ap struct {
		Approved      bool `json:"approved"`
		ApprovalsLeft int  `json:"approvals_left"`
	}
	apErr := mrGet(c, path+"/approvals", token, &ap)
	clean := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	var b strings.Builder
	fmt.Fprintf(&b, "!%d %s\n", mr.IID, clean(mr.Title))
	fmt.Fprintf(&b, "state: %s%s\n", mr.State, map[bool]string{true: " (draft)"}[mr.Draft])
	fmt.Fprintf(&b, "branches: %s -> %s\n", mr.SourceBranch, mr.TargetBranch)
	fmt.Fprintf(&b, "merge status: %s (%s)\n", mr.DetailedMergeStatus, mr.MergeStatus)
	fmt.Fprintf(&b, "conflicts: %v · blocking discussions resolved: %v · this token can merge: %v\n", mr.HasConflicts, mr.BlockingResolved, mr.User.CanMerge)
	if mr.HeadPipeline != nil {
		fmt.Fprintf(&b, "pipeline: %s %s\n", mr.HeadPipeline.Status, mr.HeadPipeline.WebURL)
	} else {
		b.WriteString("pipeline: none\n")
	}
	if apErr == nil {
		fmt.Fprintf(&b, "approvals: approved %v · left %d\n", ap.Approved, ap.ApprovalsLeft)
	}
	if mr.MergeError != "" {
		fmt.Fprintf(&b, "merge error: %s\n", clean(mr.MergeError))
	}
	if mr.MergedAt != "" {
		fmt.Fprintf(&b, "merged at: %s\n", mr.MergedAt)
	}
	fmt.Fprintf(&b, "url: %s\n", mr.WebURL)
	return b.String(), nil
}

func mrGet(c *http.Client, u, token string, v any) error {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return errors.New("bad request")
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Accept", "application/json")
	res, err := c.Do(req)
	if err != nil {
		return errors.New("gitlab.com did not answer")
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == 401:
		return errors.New("GitLab refused the token (401): it may have expired or been revoked")
	case res.StatusCode == 403:
		return errors.New("the token may not read this project (403)")
	case res.StatusCode == 404:
		return errors.New("no such project or merge request, or the token cannot see it (404)")
	case res.StatusCode != 200:
		return fmt.Errorf("GitLab answered %d", res.StatusCode)
	}
	if json.Unmarshal(body, v) != nil {
		return errors.New("GitLab sent something that is not the expected JSON")
	}
	return nil
}
