# aiagent — AI Agent World

A pastel village in your browser that shows the AI coding agents actually
running on your machine — Claude Code and Codex — and lets you send them work
through a narrow, audited channel.

It is a **view onto real processes**, not a simulation. Every character moves
because a session's state changed on disk: no decorative animation stands in
for work that is not happening.

> **Status: alpha.** It has been run by its author on one machine. The security
> model below is real and tested, but this has not had an outside audit, and
> the UI defaults to Thai. See [Known gaps](#known-gaps).

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
- **Markdown is rendered**, not shown raw — headings, lists, tables, code
  blocks and links, in the conversation and on the room monitor.
- **Room to read**: every dialog has a full-screen toggle (or press **F**) and
  a drag corner; the side panel has a drag edge and can be thrown up as a
  full-size sheet. Widths are remembered.
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
./packaging/install.sh            # add --with-service for a systemd unit or LaunchAgent
```

It also installs Claude Code, Codex and Gemini CLI when they are missing. One
already on the machine is left alone, sign-in included; `--update-clis` updates
it, `--no-clis` skips this step. `--skills DIR` copies a folder of skills into
`~/.claude/skills`; memory and settings are never copied. Sign Claude and Codex
in from the web (Accounts); Gemini is signed in from a terminal for now.

This installs two binaries into `~/.local/bin`: **`aiagent`**, the service and
its switch, and **`agcmd`**, the small tool that actually talks to the agents
through tmux. A clone is usable immediately — there is nothing else to fetch.

```sh
aiagent enable        # prints a token ONCE; only its SHA-256 is stored
aiagent serve         # http://127.0.0.1:8890/aiagent
```

Open the URL, paste the token. Until you run `enable`, **every route answers
404** — the service is indistinguishable from one that is not installed.

### First run with rooms (Linux)

A *room* is an OS user with its own aiagent instance, tmux server and Claude
session. Rooms keep one person's lines of work apart, each with its own rights.

```sh
sudo ./packaging/install.sh --os-users
```

Then once, as root, **in your own terminal** (not through an agent or a chat,
where the token would stay in the transcript):

```sh
systemctl daemon-reload
systemctl enable --now aiagent-tmux@root aiagent@root
aiagent enable                # admin token, shown once
claude setup-token            # sign in with your own Claude account
aiagent-claude-token set      # paste that token (hidden)
```

| Command | What it does |
| --- | --- |
| `aiagent-claude-token status` | whether a token is set |
| `aiagent-claude-token set` | set or replace it; running rooms restart |
| `aiagent-claude-token remove` | rooms go back to their own sign-in |

The token is kept in `/etc/aiagent/claude.env` (root only). A room's agent can
read the token it runs with. When it expires, run the two token commands again.

Make a room on the web: **Accounts → user tokens → Create token**. A session
named after it is open at once. Revoking the token stops the room, archives its
home to `/var/backups/aiagent-users` and removes the OS user.

| | Admin token | User token (a room) |
| --- | --- | --- |
| Own session | read, send | read, send, rename |
| Other rooms | enter and manage | read only |
| Admin sessions | read, send | state only |
| PM, tasks, accounts, tokens, CLI updates | yes | no |
| Choose the model | yes | no, the admin's rule |

Rooms run the latest Opus. **Accounts → Model of user rooms** sets the weekly
usage above which they move to Haiku 5.5. The reading comes from Claude Code's
status line, so set it for the whole machine in
`/etc/claude-code/managed-settings.json`:

```json
{ "statusLine": { "type": "command", "command": "aiagent statusline" } }
```

A room is a fresh home: it has none of your skills, memory or `CLAUDE.md`. Put
what every room should know in `/usr/local/share/aiagent/room/` (root-owned):
`CLAUDE.md` and `skills/<name>/`. Rooms link to it, so an update reaches them
all. Nothing is copied from your own home; keep secrets out of it.

Rooms get uids from 700000 up, away from the ids containers use. Outbound
network limits are the host's job.

### One person, several rooms

Rooms that share one Claude sign-in are for **one person**: the subscriber,
keeping their own work apart on their own machine. Anthropic's
[Consumer Terms](https://www.anthropic.com/legal/consumer-terms) do not allow
sharing a Free, Pro or Max account, or making it available to anyone else, and
[Claude Code's terms](https://code.claude.com/docs/en/legal-and-compliance)
expect each end user to authenticate with their own credentials.

- Do not give a room's token to another person while it uses your sign-in.
- If someone else needs a room, do not use `aiagent-claude-token` on that
  machine (it applies to every room); each person signs in to their own room
  with their own account.
- Usage limits are the account's, shared by every room.

This is a summary, not legal advice; the linked terms decide.

### Reaching it from another device

The listener refuses any non-loopback address unless you give it a TLS
certificate. That is deliberate. Put a private network in front of it:

```sh
tailscale serve --bg 8890         # or an SSH tunnel, or a reverse proxy
```

There is no self-signed-certificate fallback: a certificate you must click
past teaches you to click past certificate warnings, and this service can type
into your agents.

### On an iPad or a phone

Open it in Safari or Chrome and use **Add to Home Screen**. It installs as an
icon and opens without an address bar, full screen. The token is kept in that
install's own storage, so you log in once.

Regenerate the icons after an art change:

```sh
go run ./tools/appicon -in web/assets/house-kcmd.png -out web/assets
```

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

Check what is actually reachable:

```sh
agcmd ws list        # NAME, TMUX SESSION, what that pane is running, DIR
```

`agcmd` is also how you drive an agent from a terminal, and the quickest way
to tell whether a problem is in the service or in tmux:

```sh
agcmd dispatch web "run the tests"       # send a message
agcmd eng "fix the login tests" web      # send it as a tracked run
agcmd eng status                         # the run board
agcmd restart web                        # interrupt what it is doing
```

It refuses the same things the service refuses: an unconfigured workspace
name, and a pane that is running a shell rather than an agent — typing into a
shell would execute your text as a command.

### Trusted roots (optional)

Claude Code asks whether to trust a folder the first time it runs there, and
a session resumed from the web stalls on that question. Name the folders you
trust and aiagent answers Yes for any folder at or below them, in the
service user's `~/.claude.json`, before it starts or resumes a session there:

```
aiagent trust-roots add ~/Projects       # macOS, as yourself
sudo -u alice -H aiagent trust-roots add /srv/projects   # Linux, as the service user
aiagent trust-roots list | remove DIR
```

Nothing is trusted until a root is added. `AIAGENT_TRUSTED_ROOTS` (`:`
separated) overrides the stored list. Every automatic trust is audited as
`aiagent.autoTrust`.

### Tab completion

`install.sh` sets it up for zsh and bash. Workspace names and run ids are
completed from the live configuration, so tab shows what this machine can
actually reach rather than a list baked in at install time:

```
agcmd dispatch <TAB>       → your configured workspaces
agcmd eng approve <TAB>    → the run ids that exist
```

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

- **The UI is Thai first.** It ships Thai and English (`web/i18n.js`, `?lang=en`
  or the TH|EN switch); server-side reason strings are still Thai only.
- **Codex liveness needs `/proc`.** Process discovery reads `/proc` on Linux
  and `ps` where there is none (macOS), so pid-based liveness of Claude
  sessions, the pane-ancestry check behind dispatch, `claude attach` panes and
  telling codex or gemini apart from a node REPL work on both. What still
  needs `/proc` is Codex presence and the open-rollout check, which read a
  process's working directory and file descriptors; on macOS Codex sessions
  show from their rollout files only.
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
