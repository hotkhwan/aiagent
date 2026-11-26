// internal/world/selfupdate.go
//
// Updating aiagent from the web page (admin only).
//
// The service knows the git checkout it was installed from: packaging/install.sh
// records it in <state-dir>/source (or --update-repo names one). The check
// fetches origin and says how far behind it is, which version that is and which
// commits it brings. An update is offered only when it is a fast-forward of a
// clean checkout, so nothing local is ever overwritten.
//
// Applying runs `aiagent self-update` in its own session (it must outlive the
// service it restarts): fetch and fast-forward, packaging/install.sh, restart. It
// writes its progress to <state-dir>/update.json and update.log.
//
// What this trusts: whatever is on the checkout's upstream branch. That is the
// same trust as running install.sh by hand; the button only saves the typing.
//
// Linux: the systemd unit runs the service read-only outside its state
// directory, so it cannot install or restart itself; there the check works but
// the update fails with that reason (see docs/SECURITY.md).
package world

import (
	"context"
	"encoding/json"
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
)

const (
	updateSourceFile = "source"
	updateStatusFile = "update.json"
	updateLogFile    = "update.log"
	updateCheckTTL   = time.Minute
)

var releaseVersionRE = regexp.MustCompile(`const Version = "([0-9A-Za-z.+-]{1,40})"`)

// UpdateCommit is one commit an update would bring.
type UpdateCommit struct {
	Sha     string `json:"sha"`
	Subject string `json:"subject"`
}

// UpdateStatus is what the last update did (or is doing).
type UpdateStatus struct {
	Running bool      `json:"running"`
	OK      bool      `json:"ok"`
	From    string    `json:"from,omitempty"`
	To      string    `json:"to,omitempty"`
	At      time.Time `json:"at,omitzero"`
	Log     string    `json:"log,omitempty"`
}

// UpdateInfo answers "is there a newer aiagent, and may it be installed?".
type UpdateInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	// UpstreamSha is the commit an update installs; the apply request names it.
	UpstreamSha string         `json:"upstreamSha,omitempty"`
	Behind      int            `json:"behind"`
	Ahead       int            `json:"ahead"`
	Dirty       bool           `json:"dirty"`
	Commits     []UpdateCommit `json:"commits"`
	CanUpdate   bool           `json:"canUpdate"`
	Reason      string         `json:"reason,omitempty"`
	Error       string         `json:"error,omitempty"`
	Status      UpdateStatus   `json:"status"`
}

type updateState struct {
	mu   sync.Mutex
	at   time.Time
	info UpdateInfo
}

// CheckUpdate fetches the checkout's upstream and compares it with HEAD.
func CheckUpdate(ctx context.Context, r Runner, repo string) UpdateInfo {
	info := UpdateInfo{Current: Version, Commits: []UpdateCommit{}}
	if repo == "" {
		info.Error = "ไม่รู้ว่าติดตั้งจากโฟลเดอร์ไหน: รัน packaging/install.sh ใหม่หนึ่งครั้ง หรือเริ่ม service ด้วย --update-repo"
		return info
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	git := func(args ...string) (string, error) {
		out, err := r.Run(ctx, "git", append([]string{"-C", repo}, args...)...)
		return strings.TrimSpace(string(out)), err
	}
	if _, err := git("fetch", "--quiet"); err != nil {
		info.Error = "ดึงข้อมูลเวอร์ชันใหม่จาก origin ไม่ได้"
		return info
	}
	up := upstreamOf(git)
	count := func(spec string) (int, error) {
		out, err := git("rev-list", "--count", spec)
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(out)
	}
	var err error
	if info.Behind, err = count("HEAD.." + up); err != nil {
		info.Error = "checkout ไม่มี upstream branch ให้เทียบ"
		return info
	}
	info.Ahead, _ = count(up + "..HEAD")
	if sha, err := git("rev-parse", up); err == nil {
		info.UpstreamSha = sha
	}
	if out, err := git("status", "--porcelain", "--untracked-files=no"); err == nil && out != "" {
		info.Dirty = true
	}
	if out, err := git("show", up+":internal/world/version.go"); err == nil {
		if m := releaseVersionRE.FindStringSubmatch(out); m != nil {
			info.Latest = m[1]
		}
	}
	if info.Behind > 0 {
		if out, err := git("log", "--format=%h%x09%s", "HEAD.."+up, "-n", "20"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				sha, subject, ok := strings.Cut(line, "\t")
				if ok {
					info.Commits = append(info.Commits, UpdateCommit{Sha: sha, Subject: truncateRunes(subject, 160)})
				}
			}
		}
	}
	switch {
	case info.Behind == 0:
		info.Reason = "เป็นเวอร์ชันล่าสุดแล้ว"
	case info.Ahead > 0:
		info.Reason = "checkout มี commit ที่ยังไม่ขึ้น origin: อัปเดตแบบ fast-forward ไม่ได้"
	case info.Dirty:
		info.Reason = "checkout มีไฟล์ที่แก้แล้วยังไม่ commit: อัปเดตแล้วจะทับงานนั้น"
	default:
		info.CanUpdate = true
	}
	return info
}

// upstreamOf is the branch the checkout tracks, or origin/main when it tracks
// none (a plain clone's main often does not).
func upstreamOf(git func(args ...string) (string, error)) string {
	if out, err := git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err == nil && out != "" {
		return out
	}
	return "origin/main"
}

// updateRepo is the checkout to update: --update-repo, else the one install.sh
// recorded. A path that is not a git checkout counts as none.
func (s *Service) updateRepo() string {
	repo := s.UpdateRepo
	if repo == "" {
		b, err := os.ReadFile(filepath.Join(s.Store.dir, updateSourceFile))
		if err != nil {
			return ""
		}
		repo = strings.TrimSpace(string(b))
	}
	if repo == "" || !filepath.IsAbs(repo) {
		return ""
	}
	if fi, err := os.Stat(filepath.Join(repo, ".git")); err != nil || fi == nil {
		return ""
	}
	return repo
}

func readUpdateStatus(dir string) UpdateStatus {
	var st UpdateStatus
	if b, err := os.ReadFile(filepath.Join(dir, updateStatusFile)); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	// a run that never finished (the machine stopped, the process died)
	if st.Running && time.Since(st.At) > 20*time.Minute {
		st.Running = false
		st.Log = strings.TrimSpace(st.Log + "\n(ค้างนานเกินไป ถือว่าไม่สำเร็จ)")
	}
	return st
}

func writeUpdateStatus(dir string, st UpdateStatus) {
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		tmp := filepath.Join(dir, updateStatusFile+".tmp")
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, filepath.Join(dir, updateStatusFile))
		}
	}
}

func (s *Service) updateInfo(ctx context.Context, fresh bool) UpdateInfo {
	s.update.mu.Lock()
	defer s.update.mu.Unlock()
	if fresh || s.update.at.IsZero() || time.Since(s.update.at) > updateCheckTTL {
		runner := s.Collector.Runner
		if runner == nil {
			runner = ExecRunner{}
		}
		s.update.info = CheckUpdate(ctx, runner, s.updateRepo())
		s.update.at = time.Now()
	}
	info := s.update.info
	info.Status = readUpdateStatus(s.Store.dir)
	return info
}

func (s *Service) handleUpdateCheck(c fiber.Ctx) error {
	return c.JSON(okEnv(s.updateInfo(c.Context(), fiber.Query[bool](c, "fresh", false))))
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (s *Service) handleUpdateApply(c fiber.Ctx) error {
	var body struct {
		Sha string `json:"sha"`
	}
	_ = json.Unmarshal(c.Body(), &body)
	if !commitRE.MatchString(body.Sha) {
		return s.fail(c, ErrBadRequest(`expected {"sha": "<the commit the check showed>"}`))
	}
	return s.mutate(c, "aiagent.update", []string{body.Sha}, func() (any, error) {
		info := s.updateInfo(c.Context(), true)
		// what the admin saw is what is installed: a newer upstream means a new look
		if info.UpstreamSha != body.Sha {
			return nil, ErrConflict("มี commit ใหม่ขึ้นมาหลังจากตรวจ: ตรวจอีกครั้งแล้วค่อยกดอัปเดต")
		}
		if info.Status.Running {
			return nil, ErrConflict("กำลังอัปเดตอยู่แล้ว")
		}
		if !info.CanUpdate {
			why := info.Reason
			if info.Error != "" {
				why = info.Error
			}
			return nil, ErrConflict(why)
		}
		if err := s.startSelfUpdate(s.updateRepo(), info.Latest, body.Sha); err != nil {
			return nil, err
		}
		return fiber.Map{"started": true, "from": Version, "to": info.Latest}, nil
	})
}

// startSelfUpdate runs `aiagent self-update` in a session of its own, so the
// restart it ends with does not take it down with the service.
func (s *Service) startSelfUpdate(repo, to, sha string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(s.Store.dir, updateLogFile), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	writeUpdateStatus(s.Store.dir, UpdateStatus{Running: true, From: Version, To: to, At: time.Now().UTC(), Log: "กำลังเริ่ม…"})
	cmd := exec.Command(exe, "self-update", "--repo", repo, "--state-dir", s.Store.dir, "--to", sha)
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

// RunSelfUpdate is `aiagent self-update`: pull, install, restart. It writes its
// progress to the state directory for the page to read.
func RunSelfUpdate(repo, stateDir, sha string) error {
	var log strings.Builder
	st := UpdateStatus{Running: true, From: Version, At: time.Now().UTC()}
	step := func(name string, timeout time.Duration, dir, bin string, args ...string) error {
		fmt.Fprintf(&log, "$ %s\n", name)
		writeUpdateStatus(stateDir, UpdateStatus{Running: true, From: st.From, At: st.At, Log: log.String()})
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		tail := string(out)
		if len(tail) > 4000 {
			tail = "…" + tail[len(tail)-4000:]
		}
		log.WriteString(tail)
		return err
	}
	fail := func(err error) error {
		st.Running, st.OK = false, false
		st.Log = strings.TrimSpace(log.String() + "\nไม่สำเร็จ: " + err.Error())
		writeUpdateStatus(stateDir, st)
		return err
	}
	if repo == "" || !filepath.IsAbs(repo) {
		return fail(errors.New("no source checkout"))
	}
	up := upstreamOf(func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	})
	if err := step("git fetch", 3*time.Minute, repo, "git", "fetch", "--quiet"); err != nil {
		return fail(err)
	}
	// install exactly the commit that was approved, if one was named
	target := up
	if sha != "" {
		now, err := exec.Command("git", "-C", repo, "rev-parse", up).Output()
		if err != nil || strings.TrimSpace(string(now)) != sha {
			return fail(errors.New("upstream moved since the check; check again"))
		}
		target = sha
	}
	if err := step("git merge --ff-only "+target, time.Minute, repo, "git", "merge", "--ff-only", target); err != nil {
		return fail(err)
	}
	if b, err := os.ReadFile(filepath.Join(repo, "internal/world/version.go")); err == nil {
		if m := releaseVersionRE.FindSubmatch(b); m != nil {
			st.To = string(m[1])
		}
	}
	if err := step("packaging/install.sh", 15*time.Minute, repo, filepath.Join(repo, "packaging", "install.sh")); err != nil {
		return fail(err)
	}
	st.Running, st.OK = false, true
	st.Log = strings.TrimSpace(log.String() + "\nติดตั้งแล้ว กำลังเริ่ม service ใหม่")
	writeUpdateStatus(stateDir, st)
	// the restart ends the service that started this; this process lives on
	var restart *exec.Cmd
	if runtime.GOOS == "darwin" {
		restart = exec.Command("launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/com.aiagent.service", os.Getuid()))
	} else {
		restart = exec.Command("systemctl", "restart", "aiagent@"+os.Getenv("USER"))
	}
	if out, err := restart.CombinedOutput(); err != nil {
		st.Log = strings.TrimSpace(st.Log + "\nเริ่ม service ใหม่ไม่ได้: " + strings.TrimSpace(string(out)) + " — เริ่มเองด้วยมือ")
		writeUpdateStatus(stateDir, st)
		return err
	}
	return nil
}
