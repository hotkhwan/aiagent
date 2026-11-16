// internal/aiagent/proc.go
package world

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

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
