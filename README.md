# aiagent — AI Agent World

A pastel village in your browser that shows the AI coding agents actually
running on your machine — Claude Code and Codex — and lets you send them work
through a narrow, audited channel.

It is a **view onto real processes**, not a simulation. Every character moves
because a session's state changed on disk: no decorative animation stands in
for work that is not happening.

> **Status: alpha.** It has been run by its author on one machine. The security
> model below is real and tested, but this has not had an outside audit, and
> the UI is in Thai. See [Known gaps](#known-gaps).

## What you see

- **Houses** group sessions by where they came from: a tmux/dispatcher house, a
  Claude background-daemon house, and a read-only house for sessions you
  started in a terminal or VS Code.
- **Eight rooms** in workflow order — plan, research, build, test, system,
  deploy, waiting for you, resting — and an agent walks to the room that
  matches what it last actually did (classified from its most recent tool
  call).
- **In a room**: a TV with the latest message (click to enlarge), an NPC that
  explains the room, draggable characters, a sky that follows the time of day,
  and a warm or green glow around Claude and Codex agents respectively.
- **Right panel**: the session's conversation with secrets stripped out,
  sub-agents and what each was given, the model in use with a menu to change
  it, and a stop button.
- **Quota** is read, never guessed: Claude from its status line (5-hour and
  7-day windows), Codex from the rollout's `rate_limits`. When there is no
  reading, the UI says so instead of inventing a number.

## Install

Needs Go 1.26+ to build, and `tmux` only if you want to send messages.

```sh
git clone https://github.com/hotkhwan/aiagent.git
cd aiagent
./packaging/install.sh            # add --with-systemd for the unit file
```

```sh
aiagent enable        # prints a token ONCE; only its SHA-256 is stored
aiagent serve         # http://127.0.0.1:8890/aiagent
```

Open the URL, paste the token. Until you run `enable`, **every route answers
404** — the service is indistinguishable from one that is not installed.

### Reaching it from another device

The listener refuses any non-loopback address unless you give it a TLS
certificate. That is deliberate. Put a private network in front of it:

```sh
tailscale serve --bg 8890         # or an SSH tunnel, or a reverse proxy
```

There is no self-signed-certificate fallback: a certificate you must click
past teaches you to click past certificate warnings, and this service can type
into your agents.

### Status line (recommended)

Claude Code only reports plan usage to its status line, so this is the only way
to get real quota numbers. In `~/.claude/settings.json`:

```json
{ "statusLine": { "type": "command", "command": "aiagent statusline" } }
```

It prints a short line for you and saves just the usage windows to
`~/.claude/aiagent-usage.json`.

### Workspaces (optional — only needed to send work)

A workspace is a checkout an agent can be dispatched into. With none
configured the village is **view-only** and every dispatch is refused. Create
`<state-dir>/workspaces.json`:

```json
{
  "workspaces": [
    { "name": "web", "tmuxSession": "web", "dir": "/home/alice/src/web" }
  ]
}
```

`name` is what the UI shows, `tmuxSession` must be a session whose `agent`
window is running a real agent CLI, and `dir` must be absolute. Bad entries
fail at startup, not at dispatch time.

State lives in `$AIAGENT_STATE_DIR`, else `$XDG_STATE_HOME/aiagent`, else
`~/.local/state/aiagent`.

## Review pages

Instead of pasting a wall of text, an agent can hand you a page to look at:

```sh
aiagent review --title "What I changed" summary.html
```

It appears in the testing room. You open it in a sandboxed iframe behind a
single-use ticket that expires in two minutes, then press **ตกลง ปิดงาน** or
**ขอแก้ไข**, and your answer can be sent straight back to that session.

## Security

The short version: loopback by default, 404 until enabled, a hashed token, no
shell anywhere, secrets stripped before anything reaches the browser, and an
append-only audit log that records the intent before the action. The long
version, including what is **not** protected, is in
[docs/SECURITY.md](docs/SECURITY.md).

## Known gaps

- **The UI is Thai only.** The code and docs are English; the interface is not.
- **Linux-first.** Session and process discovery reads `/proc`, so the
  collector finds nothing on macOS or BSD. The CLI and the service build and
  run there; the village will be empty.
- **No tests for the browser code.** The Go side has tests and the embedded
  art is checked against its spec (`go test ./...`), but `web/app.js` itself
  has none.
- **Asset licensing is unresolved** — see [docs/ASSETS.md](docs/ASSETS.md).
  Until that is settled, treat the images as all rights reserved even though
  the code is Apache-2.0.
- Alpha: one author, one machine, no external security review.

## Licence

Code: [Apache-2.0](LICENSE). Images: **not** covered by that licence yet —
read [docs/ASSETS.md](docs/ASSETS.md) first.

"Claude" is a trademark of Anthropic and "Codex" of OpenAI. This project is not
affiliated with, endorsed by, or sponsored by either; it only talks to their
command-line tools, and uses neutral marks (`✻`, `>_`) rather than their logos.
