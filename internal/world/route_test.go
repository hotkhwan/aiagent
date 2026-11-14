package world

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func routeWorkspaces() []workspaceEntry {
	return []workspaceEntry{
		{name: "lit", session: "lit", dir: "/home/dev/lit", provider: "claude"},
		{name: "lit-codex", session: "lit-codex", dir: "/home/dev/lit", provider: "codex"},
	}
}

func usedRow(providerID string, used float64) Quota {
	u := used
	return Quota{ProviderID: providerID, Confidence: "actual", UsedPercent: &u}
}

func allReady(workspaceEntry) bool { return true }

func TestPickWorkspaceNamedProviderUsesThatProvidersWorkspace(t *testing.T) {
	got, err := pickWorkspace(routeWorkspaces(), "codex", nil, allReady)
	if err != nil || got.name != "lit-codex" {
		t.Fatalf("codex → %q, %v; want lit-codex", got.name, err)
	}
}

func TestPickWorkspaceAutoPrefersTheProviderWithMoreHeadroom(t *testing.T) {
	quotas := []Quota{usedRow("anthropic", 72), usedRow("openai", 10)}
	got, err := pickWorkspace(routeWorkspaces(), "auto", quotas, allReady)
	if err != nil || got.name != "lit-codex" {
		t.Fatalf("auto with claude 72%% and codex 10%% → %q, %v; want lit-codex", got.name, err)
	}
}

func TestPickWorkspaceAutoTriesAProviderWithNoReadingLast(t *testing.T) {
	quotas := []Quota{usedRow("anthropic", 40)}
	got, err := pickWorkspace(routeWorkspaces(), "auto", quotas, allReady)
	if err != nil || got.name != "lit" {
		t.Fatalf("auto with claude 40%% and no codex reading → %q, %v; want lit", got.name, err)
	}
}

func TestPickWorkspaceAutoSkipsAWorkspaceWhoseAgentIsNotRunning(t *testing.T) {
	quotas := []Quota{usedRow("anthropic", 72), usedRow("openai", 10)}
	onlyClaude := func(w workspaceEntry) bool { return w.name == "lit" }
	got, err := pickWorkspace(routeWorkspaces(), "auto", quotas, onlyClaude)
	if err != nil || got.name != "lit" {
		t.Fatalf("auto with codex not running → %q, %v; want lit", got.name, err)
	}
}

func TestPickWorkspaceRefusesWhenNoConfiguredAgentIsReady(t *testing.T) {
	none := func(workspaceEntry) bool { return false }
	if _, err := pickWorkspace(routeWorkspaces(), "auto", nil, none); err == nil {
		t.Fatal("auto with no ready agent must be refused, not sent somewhere")
	}
	if _, err := pickWorkspace(nil, "claude", nil, allReady); err == nil {
		t.Fatal("a provider with no configured workspace must be refused")
	}
}

func TestLoadWorkspacesReadsAndChecksProvider(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	good := write("good.json", `{"workspaces":[{"name":"lit","tmuxSession":"lit","dir":"/home/dev/lit","provider":"codex"}]}`)
	ws, err := LoadWorkspaces(good)
	if err != nil || len(ws) != 1 || ws[0].provider != "codex" {
		t.Fatalf("provider not read: %+v, %v", ws, err)
	}
	bad := write("bad.json", `{"workspaces":[{"name":"lit","tmuxSession":"lit","dir":"/home/dev/lit","provider":"gemini"}]}`)
	if _, err := LoadWorkspaces(bad); err == nil {
		t.Fatal("an unknown provider must fail at startup")
	}
}

func TestMissionByProviderRoutesToThatProvidersWorkspace(t *testing.T) {
	e := newEnv(t)
	tok, _ := e.store.Enable("test")
	e.col.Workspaces = routeWorkspaces()
	e.runner.sessions = "lit\t1793750000\nlit-codex\t1793750000\n"
	e.runner.panes = "lit\tagent\t1\tclaude\nlit-codex\tagent\t1\tcodex\n"
	e.runner.agcmdOut = "ENG-20261114-001  RUNNING  lit-codex  ตรวจ"
	time.Sleep(writeSpacing)

	code, m, _ := e.do(t, "POST", "/aiagent/api/missions", tok, `{"provider":"codex","task":"ตรวจ test"}`)
	if code != 200 {
		t.Fatalf("mission by provider: %d %v", code, m)
	}
	calls := e.runner.agcmdCalls()
	want := []string{"eng", "ตรวจ test", "lit-codex"}
	if len(calls) != 1 || strings.Join(calls[0], "|") != strings.Join(want, "|") {
		t.Fatalf("agcmd argv = %q, want %q", calls, want)
	}
	if d, _ := m["details"].(map[string]any); d["workspace"] != "lit-codex" {
		t.Fatalf("workspace not reported: %v", m)
	}

	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/missions", tok, `{"provider":"gemini","task":"x"}`); code != 400 {
		t.Fatalf("unknown provider must be a 400: %d %v", code, m)
	}
	time.Sleep(writeSpacing)
	if code, m, _ := e.do(t, "POST", "/aiagent/api/missions", tok, `{"task":"x"}`); code != 400 {
		t.Fatalf("no workspace and no provider must be a 400: %d %v", code, m)
	}
}
