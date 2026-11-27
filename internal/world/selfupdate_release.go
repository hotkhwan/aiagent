// internal/world/selfupdate_release.go
//
// Updating aiagent from a published GitHub release (admin only), for machines
// that have no checkout and no Go. The git path in selfupdate.go stays for the
// ones that do.
//
// Check: GET /aiagent/api/update/release (also part of GET /aiagent/api/update)
// asks GitHub for the latest release at most every releaseCheckTTL; ?fresh=1
// asks now. The page's bell shows when it is newer than Version.
//
// Apply: POST /aiagent/api/update {"release": "v0.36.0"}, audited. The tag must
// be the latest release and newer than this one. The service downloads that
// release's SHA256SUMS, SHA256SUMS.sig and this platform's archive into
// <state-dir>/release-download, refusing anything whose signature (the key in
// release_pubkey.go) or checksum does not verify, then starts
// `aiagent self-update --release-dir <dir>`, which verifies the folder again
// itself, replaces the binaries next to the running one (keeping .prev) and
// restarts the service. Progress goes to update.json like the git path.
//
// What this trusts: only what the owner's release key signed, and only a
// version later than the running one (so an old signed release cannot be
// replayed to downgrade a machine).
package world

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/hotkhwan/aiagent/internal/release"
)

const (
	releaseCheckTTL    = 6 * time.Hour
	releaseErrorTTL    = 15 * time.Minute
	releaseFreshGap    = 30 * time.Second // even a fresh check waits this long between asks
	releaseDownloadDir = "release-download"
	releaseNotesMax    = 8000
)

var releaseTagRE = regexp.MustCompile(`^v[0-9]{1,6}\.[0-9]{1,6}\.[0-9]{1,6}$`)

// ReleaseInfo is what the bell shows.
type ReleaseInfo struct {
	Latest    string    `json:"latest,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	URL       string    `json:"url,omitempty"`
	Newer     bool      `json:"newer"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt,omitzero"`
}

type releaseUpdate struct {
	mu   sync.Mutex
	at   time.Time
	info ReleaseInfo
	rel  release.Release
	// client, key and launch are replaced in tests; nil means GitHub, the
	// compiled-in key and a detached `aiagent self-update`.
	client *release.Client
	key    ed25519.PublicKey
	launch func(to string, args ...string) error
}

func (s *Service) releaseClient() *release.Client {
	if s.release.client != nil {
		return s.release.client
	}
	return release.Default(Version)
}

func (s *Service) releaseKey() ed25519.PublicKey {
	if s.release.key != nil {
		return s.release.key
	}
	return ReleasePublicKey()
}

// releaseInfo is the latest release, from cache unless it is stale or an
// admin asked for a fresh look.
func (s *Service) releaseInfo(ctx context.Context, fresh bool) (ReleaseInfo, release.Release) {
	r := &s.release
	r.mu.Lock()
	defer r.mu.Unlock()
	ttl := releaseCheckTTL
	if r.info.Error != "" {
		ttl = releaseErrorTTL
	}
	age := time.Since(r.at)
	if r.at.IsZero() || age > ttl || (fresh && age > releaseFreshGap) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		rel, err := s.releaseClient().Latest(ctx)
		r.at = time.Now()
		r.rel = rel
		r.info = ReleaseInfo{CheckedAt: r.at.UTC()}
		if err != nil {
			r.info.Error = "ตรวจ release ล่าสุดบน GitHub ไม่ได้: " + truncateRunes(err.Error(), 200)
		} else {
			r.info.Latest = rel.Tag
			r.info.Notes = truncateRunes(rel.Notes, releaseNotesMax)
			if strings.HasPrefix(rel.URL, "https://github.com/hotkhwan/aiagent/releases/") {
				r.info.URL = rel.URL
			}
			r.info.Newer = release.Newer(rel.Tag, Version)
		}
	}
	return r.info, r.rel
}

func (s *Service) handleReleaseCheck(c fiber.Ctx) error {
	info, _ := s.releaseInfo(c.Context(), fiber.Query[bool](c, "fresh", false))
	return c.JSON(okEnv(fiber.Map{"current": Version, "release": info, "status": readUpdateStatus(s.Store.dir)}))
}

func (s *Service) handleReleaseApply(c fiber.Ctx, tag string) error {
	if !releaseTagRE.MatchString(tag) {
		return s.fail(c, ErrBadRequest(`expected {"release": "v<major>.<minor>.<patch>"}`))
	}
	return s.mutate(c, "aiagent.update.release", []string{tag}, func() (any, error) {
		if !release.Newer(tag, Version) {
			return nil, ErrConflict("v" + strings.TrimPrefix(tag, "v") + " ไม่ใหม่กว่าเวอร์ชันที่ติดตั้งอยู่ (v" + Version + ")")
		}
		if readUpdateStatus(s.Store.dir).Running {
			return nil, ErrConflict("กำลังอัปเดตอยู่แล้ว")
		}
		info, rel := s.releaseInfo(c.Context(), true)
		if info.Error != "" {
			return nil, ErrConflict(info.Error)
		}
		if info.Latest != tag {
			return nil, ErrConflict("release ล่าสุดคือ " + info.Latest + " ไม่ใช่ " + tag + ": ตรวจอีกครั้งแล้วค่อยกดอัปเดต")
		}
		to := strings.TrimPrefix(tag, "v")
		writeUpdateStatus(s.Store.dir, UpdateStatus{Running: true, From: Version, To: to, At: time.Now().UTC(), Log: "กำลังดาวน์โหลด " + tag + "…"})
		go s.fetchAndHandOff(rel, to)
		return fiber.Map{"started": true, "from": Version, "to": to}, nil
	})
}

// fetchAndHandOff downloads and verifies the release, then starts the
// installer. Every failure ends in update.json for the page to show.
func (s *Service) fetchAndHandOff(rel release.Release, to string) {
	started := time.Now().UTC()
	fail := func(err error) {
		writeUpdateStatus(s.Store.dir, UpdateStatus{From: Version, To: to, At: started, Log: "ไม่สำเร็จ: " + err.Error()})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	dir := filepath.Join(s.Store.dir, releaseDownloadDir)
	v, err := s.releaseClient().Fetch(ctx, rel, dir, runtime.GOOS, runtime.GOARCH, s.releaseKey())
	if err != nil {
		fail(err)
		return
	}
	if v.Version != to || !release.Newer(v.Version, Version) {
		fail(errors.New("the downloaded release is not the one asked for"))
		return
	}
	args := []string{"self-update", "--release-dir", dir, "--state-dir", s.Store.dir}
	if unit := currentSystemdUnit(); unit != "" {
		args = append(args, "--unit", unit)
	}
	launch := s.release.launch
	if launch == nil {
		launch = s.launchSelfUpdate
	}
	if err := launch(to, args...); err != nil {
		fail(err)
	}
}

// launchSelfUpdate starts `aiagent <args>` so that it outlives the service it
// restarts. As root under systemd it runs as a transient unit of its own,
// outside this unit's read-only sandbox and cgroup (it must write the binary's
// folder, and `systemctl restart` would kill it with the service otherwise);
// elsewhere it runs in a session of its own, as the git path always has.
func (s *Service) launchSelfUpdate(to string, args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(s.Store.dir, updateLogFile)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	writeUpdateStatus(s.Store.dir, UpdateStatus{Running: true, From: Version, To: to, At: time.Now().UTC(), Log: "กำลังเริ่ม…"})
	var cmd *exec.Cmd
	if run, err := exec.LookPath("systemd-run"); err == nil && runtime.GOOS == "linux" && os.Geteuid() == 0 && os.Getenv("INVOCATION_ID") != "" {
		logf.Close()
		unit := "aiagent-self-update-" + strconv.FormatInt(time.Now().Unix(), 10)
		runArgs := []string{"--quiet", "--collect", "--unit", unit,
			"-p", "StandardOutput=append:" + logPath, "-p", "StandardError=append:" + logPath}
		// a transient unit starts with an empty environment; install.sh (the
		// git path) needs the service's HOME, USER and PATH
		for _, k := range []string{"HOME", "USER", "PATH", "XDG_STATE_HOME", "XDG_CONFIG_HOME", "AIAGENT_STATE_DIR"} {
			if v, ok := os.LookupEnv(k); ok {
				runArgs = append(runArgs, "--setenv="+k+"="+v)
			}
		}
		cmd = exec.Command(run, append(append(runArgs, "--", exe), args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			err = fmt.Errorf("systemd-run: %v %s", err, strings.TrimSpace(string(out)))
			writeUpdateStatus(s.Store.dir, UpdateStatus{From: Version, At: time.Now().UTC(), Log: "เริ่มอัปเดตไม่ได้: " + err.Error()})
			return err
		}
		return nil
	}
	cmd = exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logf.Close()
		writeUpdateStatus(s.Store.dir, UpdateStatus{From: Version, At: time.Now().UTC(), Log: "เริ่มอัปเดตไม่ได้: " + err.Error()})
		return err
	}
	go func() {
		_ = cmd.Wait()
		logf.Close()
	}()
	return nil
}

var unitRE = regexp.MustCompile(`^aiagent@[A-Za-z0-9_.-]{1,64}\.service$`)

// unitFromCgroup is the aiagent@ unit a /proc/self/cgroup names, if any.
func unitFromCgroup(cg string) string {
	for _, line := range strings.Split(cg, "\n") {
		i := strings.LastIndex(line, "/")
		if i < 0 {
			continue
		}
		if u := line[i+1:]; unitRE.MatchString(u) {
			return u
		}
	}
	return ""
}

func currentSystemdUnit() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	return unitFromCgroup(string(b))
}

// RunReleaseUpdate is `aiagent self-update --release-dir`: verify the folder
// (again: it sits in a directory the service can write), install the two
// binaries next to the running one, restart.
func RunReleaseUpdate(dir, stateDir, unit string) error {
	var log strings.Builder
	st := UpdateStatus{Running: true, From: Version, At: time.Now().UTC()}
	logf := func(format string, a ...any) {
		fmt.Fprintf(&log, format+"\n", a...)
		writeUpdateStatus(stateDir, UpdateStatus{Running: true, From: st.From, To: st.To, At: st.At, Log: log.String()})
	}
	fail := func(err error) error {
		st.Running, st.OK = false, false
		st.Log = strings.TrimSpace(log.String() + "\nไม่สำเร็จ: " + err.Error())
		writeUpdateStatus(stateDir, st)
		return err
	}
	if unit != "" && !unitRE.MatchString(unit) {
		return fail(errors.New("not an aiagent unit: " + unit))
	}
	if dir == "" || !filepath.IsAbs(dir) {
		return fail(errors.New("--release-dir must be an absolute path"))
	}
	logf("$ verify %s", dir)
	v, err := release.Verify(dir, runtime.GOOS, runtime.GOARCH, ReleasePublicKey())
	if err != nil {
		return fail(err)
	}
	st.To = v.Version
	if !release.Newer(v.Version, Version) {
		return fail(fmt.Errorf("v%s is not newer than v%s", v.Version, Version))
	}
	logf("signed by the aiagent release key: v%s for %s/%s", v.Version, runtime.GOOS, runtime.GOARCH)
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return fail(err)
	}
	if err := installRelease(v.Files, filepath.Dir(exe), codesigner(logf), logf); err != nil {
		return fail(err)
	}
	st.Running, st.OK = false, true
	st.Log = strings.TrimSpace(log.String() + "\nติดตั้งแล้ว กำลังเริ่ม service ใหม่")
	writeUpdateStatus(stateDir, st)
	return restartService(unit, stateDir, &st)
}

// installRelease writes the binaries next to each other in binDir: each is
// staged as .<name>.new (and signed there, when sign is set), then the live
// one is kept as <name>.prev and the staged one renamed over it.
func installRelease(files map[string][]byte, binDir string, sign func(path, name string) error, logf func(string, ...any)) error {
	for _, n := range release.Binaries {
		if len(files[n]) == 0 {
			return fmt.Errorf("the release has no %s", n)
		}
	}
	staged := map[string]string{}
	cleanup := func() {
		for _, p := range staged {
			os.Remove(p)
		}
	}
	for _, n := range release.Binaries {
		p := filepath.Join(binDir, "."+n+".new")
		os.Remove(p)
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
		if err != nil {
			cleanup()
			return fmt.Errorf("cannot write %s (update by hand, or install where this user may write): %w", binDir, err)
		}
		staged[n] = p
		_, err = f.Write(files[n])
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = os.Chmod(p, 0o755)
		}
		if err != nil {
			cleanup()
			return err
		}
		if sign != nil {
			if err := sign(p, n); err != nil {
				logf("could not sign %s, leaving it as built: %v", n, err)
			}
		}
	}
	for _, n := range release.Binaries {
		live := filepath.Join(binDir, n)
		if _, err := os.Lstat(live); err == nil {
			prev := live + ".prev"
			os.Remove(prev)
			if err := os.Link(live, prev); err != nil {
				logf("could not keep %s.prev: %v", n, err)
			}
		}
		if err := os.Rename(staged[n], live); err != nil {
			cleanup()
			return err
		}
		delete(staged, n)
		logf("installed %s", live)
	}
	return nil
}

// codesigner signs a staged binary the way packaging/install.sh does, when
// ~/.config/aiagent/codesign-identity names a certificate (macOS only).
func codesigner(logf func(string, ...any)) func(path, name string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		home, _ := os.UserHomeDir()
		cfg = filepath.Join(home, ".config")
	}
	b, err := os.ReadFile(filepath.Join(cfg, "aiagent", "codesign-identity"))
	id := ""
	if err == nil {
		id, _, _ = strings.Cut(string(b), "\n")
		id = strings.Join(strings.Fields(id), "")
	}
	if id == "" || strings.HasPrefix(id, "-") {
		return nil
	}
	return func(path, name string) error {
		out, err := exec.Command("codesign", "--force", "--sign", id, "--identifier", "world.aiagent."+name, path).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
		}
		logf("signed %s with a stable identity", name)
		return nil
	}
}

// restartService restarts the service this update belongs to; the restart
// ends the process that started the update, not this one.
func restartService(unit, stateDir string, st *UpdateStatus) error {
	var restart *exec.Cmd
	if runtime.GOOS == "darwin" {
		restart = exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.aiagent.service", os.Getuid()))
	} else {
		if unit == "" {
			unit = "aiagent@" + os.Getenv("USER")
		}
		restart = exec.Command("systemctl", "restart", unit)
	}
	if out, err := restart.CombinedOutput(); err != nil {
		st.Log = strings.TrimSpace(st.Log + "\nเริ่ม service ใหม่ไม่ได้: " + strings.TrimSpace(string(out)) + " — เริ่มเองด้วยมือ")
		writeUpdateStatus(stateDir, *st)
		return err
	}
	return nil
}
