// internal/world/route.go
//
// Provider routing for missions. An operator who names a provider (claude or
// codex) instead of a workspace gets the first configured workspace for that
// provider whose agent window is running an agent CLI. "auto" tries the
// providers with the most plan headroom first; a provider with no actual
// reading goes last, never estimated.
package world

import (
	"context"
	"sort"
)

// providerQuota maps a routable provider to its quota provider id.
var providerQuota = map[string]string{"claude": "anthropic", "codex": "openai", "gemini": "google"}

// routableProviders is the order "auto" starts from before sorting by plan
// headroom; it is fixed so the result does not depend on map order.
var routableProviders = []string{"claude", "codex", "gemini"}

// Dispatch starts a mission in the named workspace, or with no workspace in
// the one routing picks for the provider.
func (c *Collector) Dispatch(ctx context.Context, workspace, provider, task string) (string, string, error) {
	if workspace == "" {
		w, err := c.routeMission(ctx, provider)
		if err != nil {
			return "", "", err
		}
		workspace = w.name
	}
	id, err := c.Mission(ctx, workspace, task)
	return id, workspace, err
}

func (c *Collector) routeMission(ctx context.Context, provider string) (workspaceEntry, error) {
	if provider != "auto" && providerQuota[provider] == "" {
		return workspaceEntry{}, ErrBadRequest("name a workspace, or a provider: claude, codex, gemini or auto")
	}
	return pickWorkspace(c.Workspaces, provider, c.Snapshot(ctx).Quotas, c.workspaceReady(ctx))
}

// workspaceReady reads tmux once and answers, per workspace, whether its
// session is up and running the CLI the workspace is configured for.
func (c *Collector) workspaceReady(ctx context.Context) func(workspaceEntry) bool {
	tm := c.readTmux(ctx)
	return func(w workspaceEntry) bool {
		_, up := tm.sessions[w.session]
		cmd := tm.agentCmd[w.session]
		// The pane has to be running the provider's own CLI, not just any
		// agent: readTmux has already named node panes by what they run.
		return up && agentReady(cmd) && (w.provider == "" || paneAgent(nil, cmd, 0) == w.provider)
	}
}

// pickWorkspace is the routing decision on plain values, so it can be tested
// without tmux or a snapshot.
func pickWorkspace(ws []workspaceEntry, provider string, quotas []Quota, ready func(workspaceEntry) bool) (workspaceEntry, error) {
	order := []string{provider}
	if provider == "auto" {
		order = append([]string(nil), routableProviders...)
		sort.SliceStable(order, func(i, j int) bool {
			return usedForRouting(quotas, order[i]) < usedForRouting(quotas, order[j])
		})
	}
	for _, p := range order {
		for _, w := range ws {
			if w.provider == p && !w.protected && ready(w) {
				return w, nil
			}
		}
	}
	return workspaceEntry{}, ErrConflict("no configured " + provider + " workspace has a running agent")
}

// usedForRouting is the plan share a provider has used, or past 100 when there
// is no actual reading, so an unknown provider is tried last.
func usedForRouting(quotas []Quota, provider string) float64 {
	if used, ok := worstUsed(quotas, providerQuota[provider]); ok {
		return used
	}
	return 101
}
