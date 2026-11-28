// internal/aiagent/relay.go
//
// Messaging a session directly (not a agcmd workspace):
//
//	agcmd   — the session is the CLI in an agcmd tmux agent pane, or attached
//	          there with `claude attach`: `agcmd dispatch <workspace>`.
//	relay   — an own background Claude session (kind=bg) that no agcmd pane
//	          attaches: this service keeps a private tmux session `aiagent-<id>` whose
//	          ONLY process is `claude attach <id>` (no shell; if attach exits,
//	          the tmux session ends), and types into it. Verified on a dev box:
//	          the attached session receives the text as a user turn, and other
//	          attached clients keep working.
//
//	          The relay is only ever an attach CLIENT. The session itself stays a
//	          background job of Claude Code's daemon, the one every other client
//	          (the operator's terminal, the agent view) opens too. It never runs
//	          `claude --resume` in the pane: that process would own the session,
//	          and everyone else is then told "Can't open — this session is
//	          running in another terminal", or handed a copy under a new id.
//	(none)  — interactive terminal / VS Code sessions: there is no supported
//	          way to type into someone else's terminal, so they stay read-only.
package world

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// relayName is the private tmux session used to reach a background session.
func relayName(short string) string { return "aiagent-" + short }

// reachable: a live session, or a background job that was stopped and is
// kept by the agent view as "done" — a message attaches to it again.
func (ss *Session) reachable() bool {
	return ss.Live || (ss.Kind == "bg" && ss.AgentState == "done")
}

func (c *Collector) decideMessage(ss *Session) {
	if !ss.reachable() {
		return
	}
	switch {
	case ss.DispatchVia != "":
		ss.MessageVia = "agcmd"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg" && ss.home == c.serviceHome() && shortIDPattern.MatchString(shortID(ss.SessionID)):
		ss.MessageVia = "relay"
	case ss.RuntimeType == "claudeCode" && ss.Kind != "bg" && c.Channels.Live(ss.SessionID):
		// the session's own channel process takes the message (channels.go);
		// nothing is typed into the terminal or VS Code that holds it
		ss.MessageVia = "channel"
	case ss.RuntimeType == "claudeCode" && ss.Kind == "bg":
		ss.MessageBlocked = "background session ของผู้ใช้ " + ss.Owner + " — บริการนี้ส่งได้เฉพาะ session ของตัวเอง"
	default:
		ss.MessageBlocked = "เปิดอยู่ในเทอร์มินัล/VS Code (interactive) — ไม่มีวิธีพิมพ์เข้าเทอร์มินัลของคนอื่นอย่างปลอดภัย จึงดูได้อย่างเดียว"
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (c *Collector) sleep(d time.Duration) {
	if c.Sleep != nil {
		c.Sleep(d)
		return
	}
	time.Sleep(d)
}

// SendToSession types a message into one specific live session.
func (c *Collector) SendToSession(ctx context.Context, id, message string) (string, error) {
	message, err := cleanText(message)
	if err != nil {
		return "", err
	}
	c.invalidate()
	snap := c.Snapshot(ctx)
	var target *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil || !target.reachable() {
		return "", ErrNotFound("ไม่พบ session ที่เปิดอยู่ด้วยรหัสนี้")
	}
	switch target.MessageVia {
	case "agcmd":
		return "agcmd dispatch " + target.DispatchVia, c.Message(ctx, target.DispatchVia, message)
	case "relay":
		short := shortID(target.SessionID)
		for _, o := range snap.Sessions {
			if o.Live && o.SessionID != target.SessionID && strings.HasPrefix(o.SessionID, short) {
				return "", ErrConflict("รหัสย่อ " + short + " ซ้ำกับ session อื่น")
			}
		}
		return "relay " + relayName(short), c.relaySend(ctx, target, message)
	case "channel":
		return "channel", c.Channels.Send(target.SessionID, message)
	}
	return "", ErrConflict(target.MessageBlocked)
}

// ModelAliases are the short names; full ids (claude-haiku-5-5) are accepted
// too, nothing else (claudeModelRE).
var ModelAliases = []string{"opus", "sonnet", "haiku", "fable"}

// SetModel switches a Claude Code session's model by typing the /model
// command through the session's normal channel (agcmd or relay). Only the
// fixed aliases are accepted, so this cannot carry any other command.
func (c *Collector) SetModel(ctx context.Context, id, alias string) (string, error) {
	if !claudeModelRE.MatchString(alias) {
		return "", &APIError{400, "AIAGENT_BAD_MODEL", "โมเดลต้องเป็นหนึ่งใน " + strings.Join(ModelAliases, ", ") + " หรือรหัสโมเดล Claude แบบเต็ม (claude-haiku-5-5)"}
	}
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id && s.RuntimeType != "claudeCode" {
			return "", ErrConflict("สลับโมเดลจากเว็บได้เฉพาะ session ของ Claude Code")
		}
	}
	return c.SendToSession(ctx, id, "/model "+alias)
}

// RenameInCLI asks the session to rename itself, by typing /rename down the
// same channel a message goes down.
//
// There is no `claude rename` on the command line — `/rename` inside a running
// session is the only supported way to change the name Claude Code holds — so
// this is the only route that leaves the CLI and the web agreeing. A local
// alias can only ever disagree with one of them.
//
// The name is sanitised by cleanSessionName before it arrives: one line, no
// control characters, so it cannot carry a second command after the first.
func (c *Collector) RenameInCLI(ctx context.Context, id, name string) (string, error) {
	if name == "" {
		return "", ErrBadRequest("ต้องใส่ชื่อเพื่อเปลี่ยนชื่อใน CLI")
	}
	snap := c.Snapshot(ctx)
	for _, s := range snap.Sessions {
		if s.SessionID == id && s.RuntimeType != "claudeCode" {
			return "", ErrConflict("เปลี่ยนชื่อผ่าน CLI ได้เฉพาะ session ของ Claude Code")
		}
	}
	return c.SendToSession(ctx, id, "/rename "+name)
}

// ResumeSession starts a Claude Code session that is not running, on its own
// conversation in its own folder, and types nothing into it. The state says
// whether it was started now or was already running.
func (c *Collector) ResumeSession(ctx context.Context, id string) (string, error) {
	c.invalidate()
	snap := c.Snapshot(ctx)
	var target *Session
	for i := range snap.Sessions {
		if snap.Sessions[i].SessionID == id {
			target = &snap.Sessions[i]
		}
	}
	if target == nil {
		return "", ErrNotFound("ไม่พบ session รหัสนี้")
	}
	if target.Live {
		return "running", nil
	}
	if target.RuntimeType != "claudeCode" || target.home != c.serviceHome() {
		return "", ErrConflict("เปิดต่อได้เฉพาะ session Claude Code ของเครื่องนี้")
	}
	if target.Cwd == "" {
		return "", ErrConflict("session นี้ไม่มีโฟลเดอร์ให้เปิดต่อ")
	}
	short := shortID(target.SessionID)
	for _, o := range snap.Sessions {
		if o.Live && o.SessionID != target.SessionID && strings.HasPrefix(o.SessionID, short) {
			return "", ErrConflict("รหัสย่อ " + short + " ซ้ำกับ session อื่น")
		}
	}
	defer c.lockSend("relay:" + relayName(short))()
	if err := c.wakeInBackground(ctx, target, c.resumeDir(target)); err != nil {
		return "", err
	}
	c.invalidate()
	return "started", nil
}

// backgrounded matches the short id `claude --bg` prints on stdout for the job
// it started.
var backgrounded = regexp.MustCompile(`backgrounded · ([0-9a-f]{8})\b`)

// noJobReason is how `claude attach` refuses a session its daemon does not list.
const noJobReason = "No job matching"

// wakeInBackground brings a session the daemon does not list back as a
// background job under its own id, with `claude --bg --resume <id>`. Any client
// can attach to that job, this service's relay among them.
//
// It runs in dir, because the folder it is run in becomes the job's folder.
//
// The CLI continues the session itself only when no other process has it open.
// Otherwise it starts a COPY under a new id and still exits 0; the id it prints
// is then not the session's own (measured on Claude Code 2.1.325, which adds a
// note on stderr). A copy is a different conversation from the one the operator
// picked, so it is stopped and removed, and the caller is told the session is
// open elsewhere.
func (c *Collector) wakeInBackground(ctx context.Context, ss *Session, dir string) error {
	// The id becomes an argument of the CLI; nothing but a session id may.
	if !sessionUUIDRE.MatchString(ss.SessionID) {
		return ErrConflict("Claude Code เปิดต่อ session รหัสนี้ไม่ได้")
	}
	c.trustFolder(dir)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := c.runIn(cctx, dir, "", c.ClaudeBin(), "--bg", "--resume", ss.SessionID)
	if err != nil {
		return &APIError{502, "AIAGENT_RESUME_FAILED", "เปิด session ต่อไม่สำเร็จ: " + attachReason(out+"\n"+err.Error())}
	}
	m := backgrounded.FindStringSubmatch(out)
	if m == nil {
		return &APIError{502, "AIAGENT_RESUME_FAILED", "เปิด session ต่อแล้วแต่ไม่รู้ว่าเป็นงานไหน: " + attachReason(out)}
	}
	if m[1] != shortID(ss.SessionID) {
		_, _ = c.Runner.Run(cctx, c.ClaudeBin(), "stop", m[1])
		_, _ = c.Runner.Run(cctx, c.ClaudeBin(), "rm", m[1])
		return ErrConflict("session นี้เปิดอยู่ใน Claude Code อีกที่ (เทอร์มินัลหรือ VS Code) จึงทำต่อจากที่นี่ไม่ได้ ให้ปิดที่นั่นก่อน หรือส่งจากที่นั่น")
	}
	return nil
}

// resumeDir is where --resume must run: the folder the transcript is filed
// under. An agent that moved into a worktree reports that worktree as its cwd,
// and --resume run there finds no conversation and exits at once.
func (c *Collector) resumeDir(ss *Session) string {
	home := ss.home
	if home == "" {
		home = c.serviceHome()
	}
	m, _ := filepath.Glob(filepath.Join(home, ".claude", "projects", "*", ss.SessionID+".jsonl"))
	if len(m) == 0 {
		return ss.Cwd
	}
	slug := filepath.Base(filepath.Dir(m[0]))
	for d := ss.Cwd; d != "" && d != "."; d = filepath.Dir(d) {
		if projectSlug(d) == slug {
			return d
		}
		if d == "/" {
			break
		}
	}
	return ss.Cwd
}

// projectSlug is the name Claude Code files a folder's transcripts under.
func projectSlug(dir string) string {
	b := []byte(dir)
	for i, ch := range b {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

func (c *Collector) tmux(ctx context.Context, args ...string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := c.Runner.Run(cctx, c.TmuxPath, args...)
	return string(out), err
}

// attachReason pulls the CLI's own complaint off a dead relay pane, so the
// operator is told what the tool said rather than a guess about it.
func attachReason(screen string) string {
	for _, l := range strings.Split(screen, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "Pane is dead") {
			continue
		}
		return truncateRunes(Redact(l), 160)
	}
	return "จบการทำงานโดยไม่บอกเหตุผล"
}

// relayDead reports whether the relay's pane has exited, and what it last said.
func (c *Collector) relayDead(ctx context.Context, name string) (bool, string) {
	out, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_dead}")
	if err != nil {
		return false, ""
	}
	f := strings.Fields(strings.TrimSpace(out))
	if len(f) == 0 || f[0] != "1" {
		return false, ""
	}
	screen, _ := c.tmux(ctx, "capture-pane", "-p", "-J", "-t", "="+name+":")
	return true, attachReason(screen)
}

// relayWait waits for the CLI's prompt. It returns ready, or the reason the
// pane died; an empty reason with ready false means it is alive but busy.
//
// capture-pane needs "=name:" like the window options, NOT "=name": with the
// bare form it answers `can't find pane` on every attempt, so the screen was
// always empty, the prompt was never found, and every relayed message failed
// with RELAY_NOT_READY after the full 16-second wait.
func (c *Collector) relayWait(ctx context.Context, name, dir string) (bool, string) {
	for i := 0; i < 40; i++ {
		if dead, why := c.relayDead(ctx, name); dead {
			if why == "" {
				why = "จบการทำงานโดยไม่บอกเหตุผล"
			}
			return false, why
		}
		cmd, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_current_command}")
		if err == nil && agentReady(strings.TrimSpace(cmd)) {
			screen, _ := c.tmux(ctx, "capture-pane", "-p", "-t", "="+name+":")
			// The folder-trust dialog draws its own ❯ beside "No, exit": typed
			// text and Enter there would close the session, not message it.
			if strings.Contains(screen, trustDialog) {
				return false, trustReason(dir)
			}
			// Any other question or menu draws a ❯ beside its highlighted
			// option too, and Enter there picks that option.
			if onDialog(screen) || strings.Contains(screen, agentViewBox) {
				return false, dialogReason(name)
			}
			if strings.Contains(screen, "❯") {
				return true, ""
			}
		}
		c.sleep(400 * time.Millisecond)
	}
	return false, ""
}

// trustDialog is the text of Claude Code's folder-trust question.
const trustDialog = "trust this folder"

// agentViewBox is the placeholder of the agent view's input box. A CLI showing
// it has left the conversation: text typed there starts a new session.
const agentViewBox = "describe a task for a new session"

// dialogFooters are the key hints Claude Code prints under a question or menu
// it draws over the input box ("Teach auto mode…", the rewind menu, a picker).
// The idle prompt never ends with one. Seen 2026-11-23: two sessions sat on
// such dialogs, every send was typed into them and reported ok.
var dialogFooters = []string{"Esc to cancel", "Esc to clear", "Type to filter", "Enter to confirm", "Enter to continue", "Enter to select", "enter continue", "enter Select", "↑/↓ Navigate", "esc to cancel"}

var askSubmitRE = regexp.MustCompile(`^(❯\s*)?1\.\s+Submit answers$`)

// onDialog reports a dialog on screen, judged by the last few lines only, so a
// footer quoted in the conversation above the prompt does not count.
func onDialog(screen string) bool {
	lines := strings.Split(screen, "\n")
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 3; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		seen++
		// the Submit tab of AskUserQuestion prints no key hints
		if askSubmitRE.MatchString(l) {
			return true
		}
		for _, f := range dialogFooters {
			if strings.Contains(l, f) {
				return true
			}
		}
	}
	return false
}

// OnDialog is onDialog for agcmd: text typed into a question answers it, and
// on an agent CLI that can mean approving a command.
func OnDialog(screen string) bool { return onDialog(screen) }

// dialogReasonPrefix starts every dialogReason, so the sender can tell it
// from a refused attach.
const dialogReasonPrefix = "Claude Code กำลังแสดงคำถามหรือเมนู"

func dialogReason(name string) string {
	return dialogReasonPrefix + " ใน session นี้ ถ้าพิมพ์จะกลายเป็นคำตอบ ให้เปิดด้วย `tmux attach -t " + name + "` แล้วตอบหรือกด Esc จากนั้นส่งใหม่"
}

// trustReason tells the operator where to answer the trust question. It is
// answered for them only under a trusted root (trust.go): trusting a folder
// lets the CLI run its code.
func trustReason(dir string) string {
	if dir == "" {
		return "Claude Code ถามว่าเชื่อถือโฟลเดอร์นี้หรือไม่ ให้เปิด session ในเทอร์มินัลหนึ่งครั้งแล้วเลือก Yes"
	}
	return "Claude Code ถามว่าเชื่อถือโฟลเดอร์นี้หรือไม่: " + dir + " ซึ่งไม่อยู่ใต้ trusted root ให้เพิ่มด้วย `aiagent trust-roots add <folder>` หรือรัน claude ในโฟลเดอร์นั้นหนึ่งครั้งแล้วเลือก \"Yes, I trust this folder\" จากนั้นส่งใหม่"
}

// trustReasonPrefix starts every trustReason, so a caller can tell the trust
// question from other refusals.
const trustReasonPrefix = "Claude Code ถามว่าเชื่อถือโฟลเดอร์นี้หรือไม่"

// relayOnTrust reports a live relay pane that is showing the trust question.
// Such a pane never loaded the conversation, so replacing it loses nothing.
func (c *Collector) relayOnTrust(ctx context.Context, name string) bool {
	if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
		return false
	}
	screen, _ := c.tmux(ctx, "capture-pane", "-p", "-t", "="+name+":")
	// The agent view likewise: that CLI has left the conversation.
	return strings.Contains(screen, trustDialog) || strings.Contains(screen, agentViewBox)
}

// relayHoldsSession reports a relay pane running the CLI itself rather than an
// attach client: one started with `claude --resume` before the relay became
// attach-only. Such a pane owns the session and locks every other client out.
func (c *Collector) relayHoldsSession(ctx context.Context, name string) bool {
	cmd, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_start_command}")
	return err == nil && strings.Contains(cmd, "--resume") && !strings.Contains(cmd, " attach ")
}

// heldElsewhere reports a live Claude Code process, other than this relay's own
// pane, that holds the session. The registry (<home>/.claude/sessions/<pid>.json)
// names each process and its kind: a terminal or VS Code CLI is "interactive",
// while a daemon job the relay may attach to is "bg". Only a registry entry
// whose process is still the one recorded (pid and procStart both match) counts,
// so a stale file from a dead CLI does not block a send.
//
// It only refuses; it never signals or types into the holder. The snapshot
// keeps one row per session id, so the registry is read again here rather
// than trusting that row.
func (c *Collector) heldElsewhere(ctx context.Context, ss *Session, name string) bool {
	home := ss.home
	if home == "" {
		home = c.serviceHome()
	}
	regs, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	if len(regs) == 0 {
		return false
	}
	pt := c.readProcTable(ctx)
	pane := c.relayPanePid(ctx, name)
	for _, p := range regs {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var reg claudeRegistry
		if json.Unmarshal(b, &reg) != nil || reg.SessionID != ss.SessionID || reg.Pid <= 0 || reg.Kind == "bg" {
			continue
		}
		start, alive := pt.startTime(reg.Pid)
		if !alive || (reg.ProcStart != "" && start != reg.ProcStart) {
			continue
		}
		if pane > 0 && pt.descendsFrom(reg.Pid, pane) {
			continue
		}
		return true
	}
	return false
}

// relayPanePid is the pid of the relay's pane process, or 0 when there is no
// such pane. Its descendants are ours, not another holder.
func (c *Collector) relayPanePid(ctx context.Context, name string) int {
	out, err := c.tmux(ctx, "list-panes", "-t", "="+name, "-F", "#{pane_pid}")
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(out))
	return pid
}

// relaySend types into the session, opening a relay for it if there is none.
//
// The way in is always `claude attach <id>`: it joins the daemon's job, waking
// it when it is finished or stopped (measured on Claude Code 2.1.325), and
// leaves the session open to every other client. A session the daemon does not
// list at all answers "No job matching <id>"; the operator still wants to give
// it more work (owner, 2026-11-14: "complete แล้วจริง แต่ก็ต้องพิมพ์สั่งเพิ่มได้"), so
// it is first brought back as a background job (wakeInBackground) and then
// attached the same way.
func (c *Collector) relaySend(ctx context.Context, ss *Session, message string) error {
	short := shortID(ss.SessionID)
	name := relayName(short)
	defer c.lockSend("relay:" + name)()
	if c.heldElsewhere(ctx, ss, name) {
		return &APIError{409, "AIAGENT_RELAY_HELD", "session นี้เปิดอยู่ใน Claude Code ที่เทอร์มินัลหรือ VS Code อยู่แล้ว " +
			"ถ้าเปิด relay ซ้ำ เครื่องนั้นจะเปิด session ต่อไม่ได้ ให้ส่งข้อความจากที่นั่น หรือปิดที่นั่นก่อนแล้วส่งใหม่"}
	}
	dir := c.resumeDir(ss)
	// A relay left on the trust question by an earlier try blocked every later
	// send (owner, 2026-11-22: "ส่งให้ agent" did nothing). It never loaded the
	// conversation, so it is replaced.
	if c.relayOnTrust(ctx, name) {
		c.trustFolder(dir)
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	}
	if c.relayHoldsSession(ctx, name) {
		// Free the session, but only from a CLI that is idle at its prompt:
		// ending one mid-task would lose the work, and a dead one says nothing.
		if dead, _ := c.relayDead(ctx, name); !dead {
			if ready, why := c.relayWait(ctx, name, dir); !ready {
				if strings.HasPrefix(why, dialogReasonPrefix) {
					return &APIError{409, "AIAGENT_RELAY_DIALOG", why}
				}
				return &APIError{504, "AIAGENT_RELAY_NOT_READY",
					"session ยังไม่พร้อมรับข้อความ น่าจะยังทำงานอยู่ ลองใหม่เมื่อว่าง"}
			}
		}
		_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
	}
	// Two goes. remain-on-exit leaves a dead pane behind when an attach ends,
	// and a leftover one is not evidence that the CLI would refuse now — the
	// first pass replaces it, and only a fresh attach that also dies is
	// reported as a refusal. Without this every send after the first exit
	// failed instantly with "refused to attach".
	reason := ""
	woken := false
	trustRetried := false
	for attempt := 0; attempt < 2; attempt++ {
		if c.relayOnTrust(ctx, name) {
			c.trustFolder(dir)
			_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		}
		if dead, why := c.relayDead(ctx, name); dead {
			if why != "" {
				reason = why
			}
			_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
		}
		if _, err := c.tmux(ctx, "has-session", "-t", "="+name); err != nil {
			if attempt > 0 && !woken && strings.Contains(reason, noJobReason) {
				if err := c.wakeInBackground(ctx, ss, dir); err != nil {
					return err
				}
				woken = true
			}
			argv := []string{"new-session", "-d", "-s", name, "-x", "220", "-y", "50"}
			if dir != "" {
				argv = append(argv, "-c", dir)
				c.trustFolder(dir)
			}
			argv = append(argv, c.ClaudeBin(), "attach", short)
			if _, err := c.tmux(ctx, argv...); err != nil {
				return &APIError{502, "AIAGENT_RELAY_FAILED", "เปิด relay ไม่สำเร็จ: " + err.Error()}
			}
			// Keep the pane after the client exits, so a refused attach can be
			// read back instead of vanishing. `claude attach` answers "No job
			// matching <id>" for a session its daemon does not list, and without
			// this the tmux session was simply gone a moment later — which the
			// wait below could only report as "not ready yet", i.e. as a busy
			// agent. That sent the operator off waiting for an agent to finish
			// something it was never doing.
			_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "remain-on-exit", "on")
		}
		// pin the pane size: with tmux's default window-size a smaller client
		// (or a resize) can shrink the pane until Claude's input box is no
		// longer drawn, and the relay then never sees the prompt
		// window-size is a window option: target the session's window ("=name:"),
		// a bare "=name" is rejected as "no such window" and the size reverts
		_, _ = c.tmux(ctx, "set-option", "-w", "-t", "="+name+":", "window-size", "manual")
		_, _ = c.tmux(ctx, "resize-window", "-t", "="+name+":", "-x", "220", "-y", "50")
		ready, why := c.relayWait(ctx, name, dir)
		if ready {
			reason = ""
			break
		}
		if !trustRetried && strings.HasPrefix(why, trustReasonPrefix) {
			// Stuck on the question: trust the folder if a root covers it and
			// start this attempt again, once.
			if _, changed := c.trustFolder(dir); changed {
				trustRetried = true
				_, _ = c.tmux(ctx, "kill-session", "-t", "="+name)
				attempt--
				continue
			}
		}
		if strings.HasPrefix(why, dialogReasonPrefix) {
			// Not a refusal and not busy: a second attempt would find the same
			// dialog, and only the operator can answer it.
			return &APIError{409, "AIAGENT_RELAY_DIALOG", why}
		}
		if why == "" {
			// Alive, just busy: the outbox holds the message and tries later.
			return &APIError{504, "AIAGENT_RELAY_NOT_READY",
				"session ยังไม่พร้อมรับข้อความ น่าจะยังทำงานอยู่ ลองใหม่เมื่อว่าง"}
		}
		reason = why
		if attempt > 0 && !strings.Contains(why, noJobReason) {
			// Refused for a reason waking cannot cure, such as the session
			// being open in another terminal: say what the CLI said.
			break
		}
	}
	if reason != "" {
		return &APIError{502, "AIAGENT_RELAY_FAILED", "CLI ของ agent ไม่ยอมให้ต่อ: " + reason}
	}
	// "=name:" again, not "=name": send-keys takes a pane target too.
	switch c.submit(ctx, "="+name+":", message).(type) {
	case nil:
	case *ErrOnDialog:
		return &APIError{409, "AIAGENT_RELAY_DIALOG", dialogReason(name)}
	case *ErrNotSubmitted:
		return errNotSubmitted()
	case *ErrTypedIntoDialog:
		ae := typedIntoDialogError()
		ae.Msg += " (`tmux attach -t " + name + "`)"
		return ae
	default:
		return &APIError{502, "AIAGENT_RELAY_FAILED", "พิมพ์ข้อความเข้า agent ไม่สำเร็จ"}
	}
	return nil
}
