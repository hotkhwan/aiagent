# Architecture

One Go binary. It serves an embedded single-page UI, polls the machine for
agent activity, and executes a small fixed set of commands on request.

```
browser ──HTTP(loopback)──▶ cmd/aiagent ──▶ internal/world
                                              ├── collector: reads the machine
                                              ├── http:      routes + token gate
                                              ├── drivers:   writes (tmux/kcmd)
                                              └── internal/audit: append-only log
                            web/ (embedded UI: index.html, app.js, assets/)
```

## Packages

| Path | Role |
|---|---|
| `cmd/aiagent` | CLI and service entry point: `serve`, `enable`, `disable`, `rotate-token`, `status`, `statusline`, `review` |
| `cmd/agcmd` | The write path, as a binary of its own: resolves a workspace, checks the pane is an agent, pastes the message, keeps run records. The service shells out to it; a human can run the same commands |
| `internal/world` | Everything else; the package is deliberately flat |
| `internal/audit` | Append-only JSONL log: intent before an action, result after |
| `web` | The UI, `//go:embed`-ed. It is its own package because `//go:embed` cannot reach a parent directory, so the declaration must sit beside the files. Its tests check the art against `docs/ASSET_SPEC.md` |

### Inside `internal/world`

**Reading the machine (no side effects)**

- `collector.go` — builds a `Snapshot`, cached for a few seconds so many open
  tabs cost one collection
- `claude.go`, `codex.go` — each provider's on-disk session format
- `proc.go` — `/proc`: liveness, parent/child ancestry, open file handles.
  **The only Linux-specific part.** Without it the collector still reports
  sessions (the `claude agents` daemon path supplies them), but loses pid
  liveness, the pane-ancestry check behind `dispatchVia`, and Codex presence.
- `daemon.go`, `subagents.go`, `watches.go` — background sessions, sub-agents,
  monitored jobs
- `usage.go` — plan usage, from the Claude status line and the Codex rollout.
  Never estimated: no reading means the UI says "unknown"
- `messages.go` — conversation text **and the redaction rules**

**Writing (the narrow channel)**

- `agcmd.go` — the service half of dispatch: validates, then runs `agcmd`
  with a fixed argv, never a shell
- `relay.go` — a private tmux session for this service's own background
  sessions
- `workspaces.go` — the configurable workspace table and its two lookups

**Service**

- `http.go` — routes, token gate, lockout, rate limit, CSP. The on/off switch
  is **not** here; it is CLI-only
- `state.go` — the switch and the token hash (`0600`)
- `policy.go` — automatic model downgrade and idle proposals
- `reviews.go` — agent-authored review pages, ticketed and sandboxed
- `upload.go` — image attachments, verified by magic bytes
- `paths.go` — XDG state directory resolution

## Rules that hold the design together

1. **No shell, anywhere.** Every external command is a fixed argv. If you find
   yourself building a command string, the design is being broken.
2. **State before action, in the audit log.** Write the intent, act, write the
   result.
3. **Unknown is a value.** Never render an invented number for quota or
   progress; the UI has a state for "no reading yet" and it is used.
4. **Reading is wide, writing is narrow.** The collector may look at anything
   under the watched homes. The write surface is a short, enumerable list.
5. **Disabled means absent.** Not 403 — `404`, everywhere, including the page.
6. **Configuration over compiled-in facts.** Anything naming a particular
   machine, user or repository belongs in a config file. A table of someone's
   private checkouts is how this code got stuck in a private repository in the
   first place.

## Testing

```sh
go test ./...
```

The tests drive the real HTTP service against a fake filesystem and a fake
command runner, so dispatch, the token gate, redaction and the review flow are
exercised end to end.

`web/assets_test.go` is `docs/ASSET_SPEC.md` made executable: it checks the
embedded art for sprite-sheet grids, room completeness, serving-allowlist
names, and that every prop is actually cut out. These are the failures that a
code review cannot see and that reach the user as a village that looks wrong.
Run it after any art drop.

`web/app.js` itself has no tests — that is a known gap.

When you add a check, **break it once and watch it fail** before you trust it.
