// internal/world/trust.go
//
// Trusted roots. Claude Code asks "Do you trust the files in this folder?"
// the first time it runs in a folder, and a relay sitting on that question
// can neither be messaged nor resumed. The owner names the folders whose
// subfolders they trust anyway (their own project directories); before this
// service starts or resumes a claude process in a folder under one of them,
// it records the answer the way Claude Code itself does, in the service
// user's ~/.claude.json:
//
//	"projects": { "<folder>": { "hasTrustDialogAccepted": true, … } }
//
// Nothing is trusted unless a root is configured: an empty list keeps the old
// behaviour, an error naming the folder. A trusted parent does not cover its
// children in Claude Code, so every folder gets its own entry, and both the
// path as given and its symlink-free form are recorded.
//
// ~/.claude.json holds much other state that Claude Code rewrites itself, so
// it is edited narrowly: read, change one key of one entry, keep every other
// byte of every other value and the key order, write to a temporary file and
// rename it over the original with the original mode. A file that changed
// while we worked is re-read and the edit re-applied, once.
package world

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hotkhwan/aiagent/internal/audit"
)

// TrustedRootsEnv overrides the configured roots when set and non-empty;
// entries are separated by the OS path-list separator (":" on Unix).
const TrustedRootsEnv = "AIAGENT_TRUSTED_ROOTS"

// ErrNotUnderRoot means the folder is outside every trusted root.
var ErrNotUnderRoot = errors.New("folder is not under a trusted root")

// NormalizeRoot turns an operator-supplied root into the absolute, cleaned
// form that is stored and compared. "/" is refused: it would trust every
// folder on the machine.
func NormalizeRoot(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if abs == string(filepath.Separator) || filepath.Dir(abs) == abs {
		return "", errors.New("refusing to trust the filesystem root")
	}
	return abs, nil
}

// LoadTrustedRoots returns the roots in effect: $AIAGENT_TRUSTED_ROOTS when
// set, otherwise trustedRoots in <stateDir>/policy.json.
func LoadTrustedRoots(stateDir string) []string {
	if v := os.Getenv(TrustedRootsEnv); strings.TrimSpace(v) != "" {
		return normalizeRoots(filepath.SplitList(v))
	}
	roots, _ := StoredTrustedRoots(stateDir)
	return roots
}

// StoredTrustedRoots reads trustedRoots from <stateDir>/policy.json.
func StoredTrustedRoots(stateDir string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(stateDir, "policy.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p struct {
		TrustedRoots []string `json:"trustedRoots"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("policy.json: %w", err)
	}
	return normalizeRoots(p.TrustedRoots), nil
}

func normalizeRoots(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range in {
		n, err := NormalizeRoot(r)
		if err != nil || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// SetTrustedRoots stores roots as trustedRoots in <stateDir>/policy.json,
// leaving every other key of that file as it was.
func SetTrustedRoots(stateDir string, roots []string) error {
	policyMu.Lock()
	defer policyMu.Unlock()
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(stateDir, "policy.json")
	var top jsonObject
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &top); err != nil {
			return fmt.Errorf("policy.json: %w", err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return err
	}
	if roots == nil {
		roots = []string{}
	}
	v, _ := json.Marshal(normalizeRootsKeepEmpty(roots))
	top.set("trustedRoots", v)
	out, err := top.encode()
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out, 0o600)
}

func normalizeRootsKeepEmpty(in []string) []string {
	if r := normalizeRoots(in); r != nil {
		return r
	}
	return []string{}
}

// insideRoot reports whether dir is root or below it. Both must be clean and
// absolute; the separator check keeps /a/bc out of /a/b.
func insideRoot(dir, root string) bool {
	if dir == root {
		return true
	}
	prefix := root
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	return strings.HasPrefix(dir, prefix)
}

// trustForms is the folder as given (cleaned, absolute) and, when it differs,
// its symlink-free form. ok says whether either lies under a root, the roots
// being compared both as configured and with their own symlinks resolved.
func trustForms(dir string, roots []string) (forms []string, ok bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, false
	}
	given := filepath.Clean(dir)
	forms = []string{given}
	if real, err := filepath.EvalSymlinks(given); err == nil && real != given {
		forms = append(forms, real)
	}
	var rs []string
	for _, r := range roots {
		r = filepath.Clean(r)
		if !filepath.IsAbs(r) {
			continue
		}
		rs = append(rs, r)
		if real, err := filepath.EvalSymlinks(r); err == nil && real != r {
			rs = append(rs, real)
		}
	}
	for _, f := range forms {
		for _, r := range rs {
			if insideRoot(f, r) {
				return forms, true
			}
		}
	}
	return forms, false
}

// EnsureFolderTrusted marks dir trusted in home/.claude.json when it is under
// one of roots. It returns the paths it changed (empty when they were trusted
// already), or ErrNotUnderRoot.
func EnsureFolderTrusted(home, dir string, roots []string) ([]string, error) {
	forms, ok := trustForms(dir, roots)
	if !ok {
		return nil, ErrNotUnderRoot
	}
	var changed []string
	err := editClaudeJSON(home, func(top *jsonObject) (bool, error) {
		changed = changed[:0]
		projects, err := top.child("projects")
		if err != nil {
			return false, err
		}
		for _, f := range forms {
			entry, err := projects.child(f)
			if err != nil {
				return false, err
			}
			if v, ok := entry.get("hasTrustDialogAccepted"); ok && bytes.Equal(bytes.TrimSpace(v), []byte("true")) {
				continue
			}
			entry.set("hasTrustDialogAccepted", json.RawMessage("true"))
			if err := projects.setObject(f, entry); err != nil {
				return false, err
			}
			changed = append(changed, f)
		}
		if len(changed) == 0 {
			return false, nil
		}
		return true, top.setObject("projects", projects)
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// trustFolder marks dir trusted for Claude Code when it is under a configured
// root. trusted says the folder is (now) trusted; changed says this call
// wrote it, i.e. a CLI already showing the question may be restarted.
// Outside every root nothing is written and the caller keeps today's error.
func (c *Collector) trustFolder(dir string) (trusted, changed bool) {
	if c.TrustedRoots == nil || dir == "" {
		return false, false
	}
	roots := c.TrustedRoots()
	if len(roots) == 0 {
		return false, false
	}
	got, err := EnsureFolderTrusted(c.serviceHome(), dir, roots)
	if errors.Is(err, ErrNotUnderRoot) {
		return false, false
	}
	if c.OnTrust != nil && (err != nil || len(got) > 0) {
		c.OnTrust(dir, got, err)
	}
	if err != nil {
		return false, false
	}
	return true, len(got) > 0
}

// auditTrust records an automatic trust as aiagent.autoTrust; the folder is
// in the service log, its hash in the audit line.
func (s *Service) auditTrust(dir string, changed []string, err error) {
	if err != nil {
		log.Printf("trusted roots: could not mark %s trusted: %v", dir, err)
	} else {
		log.Printf("trusted roots: marked trusted for Claude Code: %s", strings.Join(changed, ", "))
	}
	if s.Auditor == nil {
		return
	}
	r := audit.Record{Event: "result", RPC: "aiagent.autoTrust", AdminSub: "trustedRoots", ParamsHash: paramsHash(append([]string{dir}, changed...)...), Result: "ok"}
	if err != nil {
		r.Result, r.Err = "error", trimErr(err.Error())
	}
	_ = s.Auditor.Write(r)
}

// ParamsHash is the audit log's digest of a call's parameters, for the CLI.
func ParamsHash(parts ...string) string { return paramsHash(parts...) }

// ForgetFolderTrust removes hasTrustDialogAccepted from dir's entry in
// home/.claude.json, and the entry itself when nothing else is left in it.
// It reports whether anything changed.
func ForgetFolderTrust(home, dir string) (bool, error) {
	if !filepath.IsAbs(dir) {
		return false, errors.New("folder must be an absolute path")
	}
	dir = filepath.Clean(dir)
	changed := false
	err := editClaudeJSON(home, func(top *jsonObject) (bool, error) {
		changed = false
		projects, err := top.child("projects")
		if err != nil {
			return false, err
		}
		if _, ok := projects.get(dir); !ok {
			return false, nil
		}
		entry, err := projects.child(dir)
		if err != nil {
			return false, err
		}
		if !entry.remove("hasTrustDialogAccepted") {
			return false, nil
		}
		if len(*entry) == 0 {
			projects.remove(dir)
		} else if err := projects.setObject(dir, entry); err != nil {
			return false, err
		}
		changed = true
		return true, top.setObject("projects", projects)
	})
	return changed, err
}

// FolderTrusted reports whether home/.claude.json has dir trusted.
func FolderTrusted(home, dir string) (bool, error) {
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var top struct {
		Projects map[string]struct {
			HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(b, &top); err != nil {
		return false, err
	}
	return top.Projects[filepath.Clean(dir)].HasTrustDialogAccepted, nil
}

// editClaudeJSON applies edit to home/.claude.json and writes the result
// atomically. edit returns whether it changed anything. When the file changes
// on disk between our read and our write (Claude Code saved it meanwhile) the
// edit is applied again to the new content, once; a second clash is an error.
func editClaudeJSON(home string, edit func(*jsonObject) (bool, error)) error {
	path := filepath.Join(home, ".claude.json")
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real // keep a symlinked ~/.claude.json a symlink
	}
	for attempt := 0; attempt < 2; attempt++ {
		orig, err := os.ReadFile(path)
		mode := os.FileMode(0o600)
		switch {
		case err == nil:
			if fi, err := os.Stat(path); err == nil {
				mode = fi.Mode().Perm()
			}
		case errors.Is(err, os.ErrNotExist):
			orig = nil
		default:
			return err
		}
		var top jsonObject
		if len(bytes.TrimSpace(orig)) > 0 {
			if err := json.Unmarshal(orig, &top); err != nil {
				return fmt.Errorf("%s is not a JSON object: %w", path, err)
			}
		}
		changed, err := edit(&top)
		if err != nil || !changed {
			return err
		}
		out, err := top.encode()
		if err != nil {
			return err
		}
		if bytes.HasSuffix(orig, []byte("\n")) {
			out = append(out, '\n')
		}
		if beforeClaudeJSONCheck != nil {
			beforeClaudeJSONCheck()
		}
		// Did someone else write it while we worked?
		now, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if !bytes.Equal(now, orig) {
			continue
		}
		return writeFileAtomic(path, out, mode)
	}
	return fmt.Errorf("%s kept changing while it was being edited; try again", path)
}

// beforeClaudeJSONCheck lets a test write the file between our read and our
// write, the way a running Claude Code would.
var beforeClaudeJSONCheck func()

// writeFileAtomic writes data to a temporary file next to path and renames it
// over path, with mode. Where the directory cannot take a new file (a
// systemd unit that makes the home read-only and only the file itself
// writable) it falls back to rewriting the file in place.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".aiagent-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) || isReadOnlyFS(err) {
			return writeInPlace(path, data)
		}
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeInPlace(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func isReadOnlyFS(err error) bool {
	return errors.Is(err, syscall.EROFS)
}

// jsonObject is a JSON object that keeps its key order and the exact bytes of
// every value it was not asked to change.
type jsonObject []jsonMember

type jsonMember struct {
	Key string
	Val json.RawMessage
}

func (o *jsonObject) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return errors.New("not a JSON object")
	}
	*o = (*o)[:0]
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := t.(string)
		if !ok {
			return errors.New("object key is not a string")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		*o = append(*o, jsonMember{key, append(json.RawMessage(nil), v...)})
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data after JSON object")
	}
	return nil
}

func (o jsonObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		var k bytes.Buffer
		enc := json.NewEncoder(&k)
		enc.SetEscapeHTML(false) // as JSON.stringify writes it
		if err := enc.Encode(m.Key); err != nil {
			return nil, err
		}
		buf.Write(bytes.TrimRight(k.Bytes(), "\n"))
		buf.WriteByte(':')
		buf.Write(m.Val)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// encode renders the object the way Claude Code writes the file: two-space
// indentation, no HTML escaping. A trailing newline is the caller's choice.
func (o jsonObject) encode() ([]byte, error) {
	raw, err := o.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (o jsonObject) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.Key == key {
			return m.Val, true
		}
	}
	return nil, false
}

// set replaces key's value in place, or appends it.
func (o *jsonObject) set(key string, v json.RawMessage) {
	for i := range *o {
		if (*o)[i].Key == key {
			(*o)[i].Val = v
			return
		}
	}
	*o = append(*o, jsonMember{key, v})
}

func (o *jsonObject) remove(key string) bool {
	for i := range *o {
		if (*o)[i].Key == key {
			*o = append((*o)[:i], (*o)[i+1:]...)
			return true
		}
	}
	return false
}

// child returns key's value as an object (empty when absent). A value that
// is present but not an object is an error: overwriting it would lose data.
func (o jsonObject) child(key string) (*jsonObject, error) {
	c := &jsonObject{}
	v, ok := o.get(key)
	if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		return c, nil
	}
	if err := json.Unmarshal(v, c); err != nil {
		return nil, fmt.Errorf("%q is not an object", key)
	}
	return c, nil
}

func (o *jsonObject) setObject(key string, c *jsonObject) error {
	b, err := c.MarshalJSON()
	if err != nil {
		return err
	}
	o.set(key, b)
	return nil
}
