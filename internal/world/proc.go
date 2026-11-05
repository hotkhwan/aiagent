// internal/aiagent/proc.go
package world

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// procStartTime returns field 22 (starttime, clock ticks since boot) of
// /proc/<pid>/stat, the same value Claude Code records as procStart. Matching
// both pid AND start time proves the recorded process is still the one alive
// (a recycled pid has a different start time).
func procStartTime(procRoot string, pid int) (string, bool) {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(s[i+1:])
	// fields[0] is field 3 (state) → starttime (field 22) is fields[19].
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}

// parentPid returns the ppid (field 4) of a process.
func parentPid(procRoot string, pid int) int {
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

// descendsFrom reports whether pid is ancestor itself or one of its
// descendants (walks at most 16 parents).
func descendsFrom(procRoot string, pid, ancestor int) bool {
	for i := 0; i < 16 && pid > 1; i++ {
		if pid == ancestor {
			return true
		}
		pid = parentPid(procRoot, pid)
	}
	return false
}

// attachTarget looks below a tmux pane process (up to 3 levels) for a
// `claude attach <id|name>` client and returns its target argument. Text typed
// into that pane is delivered to the attached background session, so the
// session it names — not the attach client process — is the dispatch target.
func attachTarget(procRoot string, panePid int, children map[int][]int) string {
	queue := []int{panePid}
	for depth := 0; depth < 4 && len(queue) > 0; depth++ {
		var next []int
		for _, pid := range queue {
			b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
			if err == nil {
				argv := strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
				if len(argv) >= 3 && filepath.Base(argv[0]) == "claude" && argv[1] == "attach" && argv[2] != "" {
					return argv[2]
				}
			}
			next = append(next, children[pid]...)
		}
		queue = next
	}
	return ""
}

// childMap builds ppid → children for the whole process table.
func childMap(procRoot string) map[int][]int {
	out := map[int][]int{}
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if pp := parentPid(procRoot, pid); pp > 0 {
			out[pp] = append(out[pp], pid)
		}
	}
	return out
}

// openFiles maps absolute paths of the wanted files to the pid holding them
// open. Only paths with one of the given suffixes are kept, so the scan never
// collects unrelated descriptors.
func openFiles(procRoot string, suffix string) map[string]int {
	out := map[string]int{}
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return out
	}
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fdDir := filepath.Join(procRoot, e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil || !strings.HasSuffix(target, suffix) {
				continue
			}
			out[target] = pid
		}
	}
	return out
}

// classify maps a tool name plus an optional command line to an activity
// type. The command text is only inspected here, server-side; it is never
// returned to the browser.
func classify(tool, command string) string {
	t := strings.ToLower(tool)
	cmd := strings.ToLower(command)
	switch t {
	case "edit", "write", "multiedit", "notebookedit", "apply_patch":
		return ActivityCodeEdit
	case "read", "grep", "glob", "webfetch", "websearch", "ls":
		return ActivityResearch
	case "monitor":
		if containsAny(cmd, "jenkins", "/job/", "queue/item", "wfapi", "argocd", "kubectl") {
			return ActivityDeploy
		}
		return ActivityCommand
	case "agent", "task", "todowrite", "taskcreate", "taskupdate", "enterplanmode", "exitplanmode", "skill":
		return ActivityPlanning
	}
	if t == "bash" || t == "exec" || t == "shell" || t == "exec_command" || t == "local_shell" {
		switch {
		case containsAny(cmd, "kubectl", "helm ", "argocd", "ansible", "systemctl", "docker push", "deploy", "jenkins", "/job/", "buildwithparameters", "queue/item", "wfapi", "gh pr merge"):
			return ActivityDeploy
		case containsAny(cmd, " test", "go test", "bun test", "npm test", "pytest", "playwright", "vitest", "make test", "jest"):
			return ActivityTestRun
		case containsAny(cmd, "apply_patch", "sed -i", "gofmt -w"):
			return ActivityCodeEdit
		case containsAny(cmd, "grep", "rg ", "cat ", "sed -n", "find ", "ls", "git log", "git diff", "git show", "head ", "tail "):
			return ActivityResearch
		}
		return ActivityCommand
	}
	if t == "" {
		return ActivityUnknown
	}
	return ActivityCommand
}

func containsAny(s string, subs ...string) bool {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return true
		}
	}
	return false
}

// repoID derives a short repository label from a working directory: the part
// before any /.claude/worktrees/ segment, last path element.
func repoID(cwd string) string {
	base := cwd
	if i := strings.Index(base, "/.claude/worktrees/"); i >= 0 {
		base = base[:i]
	}
	return filepath.Base(base)
}
