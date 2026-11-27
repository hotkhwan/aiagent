# IDE sessions: driving a terminal or VS Code Claude Code session from the web

Goal (owner): a Claude Code session opened in VS Code (the extension) or in a
plain terminal / cmux stays driven from there, and the same session can also
take messages and answers from the AI Agent World web page or the phone app.

Until now aiagent could only reach its own background jobs (`claude attach`
through a private tmux relay, `internal/world/relay.go`, `askrelay.go`) and
refused anything a terminal or VS Code holds (`heldElsewhere`), because typing
into someone else's terminal is not safe. This document records what Claude
Code itself offers for the job, checked on 2026-11-28 against the docs and
the local CLI (`claude --version` = 2.1.325), and what was built.

## Findings

### (a) Channels — the sanctioned way to push messages into a running session

Sources: [Channels](https://code.claude.com/docs/en/channels),
[Channels reference](https://code.claude.com/docs/en/channels-reference),
[MCP: push messages with channels](https://code.claude.com/docs/en/mcp#push-messages-with-channels).

- A channel is an MCP server Claude Code spawns over stdio that declares
  `capabilities.experimental["claude/channel"]` and emits
  `notifications/claude/channel` `{content, meta}`. The event arrives in the
  session that is already open as `<channel source="<server name>" ...>`; if
  Claude is busy, events queue and are delivered on the next turn.
- Permission relay: a server that also declares
  `claude/channel/permission` receives
  `notifications/claude/channel/permission_request`
  `{request_id, tool_name, description, input_preview}` whenever a tool
  approval dialog opens, and answers with
  `notifications/claude/channel/permission` `{request_id, behavior: allow|deny}`.
  The local dialog stays open; **whichever answer comes first wins**. Ids are
  five letters `[a-km-z]`; an unknown or repeated id is dropped silently.
  `description` / `input_preview` are sanitised and credential-masked by
  Claude Code (v2.1.211 / v2.1.234+), but are still to be treated as untrusted.
- Relay covers tool approvals (Bash, Write, Edit, MCP tools). Project trust
  and MCP-consent dialogs never relay. **AskUserQuestion is not mentioned as
  relayed**; the docs only speak of tool-use approvals.
- Enabling, per session: being in `.mcp.json` is not enough; the server must
  be named at startup with `--channels plugin:<name>@<marketplace>` (only
  plugins on Anthropic's allowlist, or the org's `allowedChannelPlugins`) or,
  for anything else, `--dangerously-load-development-channels server:<name>`,
  which shows a full-screen warning that must be accepted at every start.
  Neither flag appears in `claude --help` (confirmed locally); they work anyway.
- **Research preview.** Flag syntax and protocol "may change". Needs
  claude.ai or Console API-key auth (not Bedrock/Vertex/Foundry). Pro/Max/Ultra
  without an org: available, opt-in per session. Team/Enterprise: blocked until
  an Owner sets `channelsEnabled`. On the v2 MCP runtime a server that
  negotiates MCP revision 2026-07-28 is not registered as a channel.
- The development flag is **ignored in `-p` and through the Agent SDK**
  ("Run the development flag in an interactive session"). The VS Code
  extension's graphical panel hosts Claude Code through the Agent SDK
  (see the `canUseTool` note in [hooks](https://code.claude.com/docs/en/hooks#notification)),
  and the [VS Code page](https://code.claude.com/docs/en/vs-code) has no
  setting for extra CLI flags and does not mention channels at all. So:
  - terminal / cmux / tmux `claude`: works.
  - VS Code **terminal mode** (`claudeCode.useTerminal: true`): the CLI runs
    interactively; the flag can be added with a `claudeProcessWrapper` script.
    Plausible, **unverified**.
  - VS Code graphical panel: **probably does not work** (SDK-hosted, no way to
    pass the flag, dev flag ignored there). Unverified either way.
- Security (from the docs): "An ungated channel is a prompt injection vector";
  gate on the sender, and only declare permission relay when the sender is
  authenticated, since anyone who can reply can approve tool use.

### (b) Hooks — work everywhere, including the VS Code panel

Source: [Hooks reference](https://code.claude.com/docs/en/hooks).

- "Claude Code fires the same hook events wherever it runs: sessions in the
  terminal, IDE extensions, the Desktop app, and cloud sessions." Hooks come
  from `settings.json` (user / project / local) and need workspace trust in
  interactive sessions. Not a preview feature.
- `PermissionRequest` can return `decision.behavior` allow/deny. For a call
  that goes to the Agent SDK's `canUseTool` (how VS Code hosts Claude Code)
  "the hooks run alongside your host, and whichever decides first applies" —
  so in VS Code a hook can wait for a web answer while the IDE dialog stays
  usable. In a plain terminal the docs do not say whether the dialog shows
  while a hook is still running; a waiting hook may hold the dialog back.
- **AskUserQuestion can be answered by a hook**: `PreToolUse` or
  `PermissionRequest` returning allow plus `updatedInput` that echoes
  `questions` and adds `answers: {"<question text>": "<label>"}`.
- `Notification` (`permission_prompt`, `idle_prompt`, `elicitation_dialog`)
  is a signal only (about 6 s / 60 s late).
- `type: "http"` hooks POST the event to a URL; header values may use
  environment variables listed in `allowedEnvVars` (so no secret in the
  settings file). `asyncRewake` command hooks can wake an idle session with
  text, but it arrives as a system reminder, not as a user message, and its
  timeout is enforced; it is a poor substitute for channels.
- `Stop` with `decision: "block"` can keep Claude going with a reason; it only
  helps at the end of a turn and is not a message path either.

### (c) Remote Control

Source: [Remote Control](https://code.claude.com/docs/en/remote-control).

- Works for CLI, Desktop and the VS Code extension (`/remote-control` or the
  "Enable Remote Control for all sessions" toggle, setting
  `remoteControlAtStartup`). Permission prompts and AskUserQuestion stay open
  for a remote answer, and messages can be sent from claude.ai/code or the
  Claude app.
- But the only remote surfaces are claude.ai/code and the Claude apps, on a
  claude.ai subscription (no API keys). There is **no documented API for a
  third-party app**, so aiagent cannot be the other end. It is the zero-code
  answer for the owner (use the Claude app), not something aiagent can build on.

### (d) Others checked

- `claude --bg` / `claude attach` (what the relay already uses): only for
  background jobs; attaching to a session a terminal or VS Code owns is what
  `heldElsewhere` correctly refuses.
- `--input-format stream-json` / the Agent SDK: only for a process aiagent
  starts itself, not for one the IDE owns.
- MCP elicitation: a server can ask the user a question, not the reverse.

### Recommendation

1. **Messages and tool approvals from the web: channels** (built below). The
   only documented way to put text into a session someone else started,
   without touching its terminal. Fully works for terminal sessions; for VS Code
   use terminal mode until Anthropic documents channels for the panel.
2. **AskUserQuestion, and approvals in the VS Code panel: a `PermissionRequest`
   hook** (next step, not built). An `aiagent ask-hook` command hook, or an
   `http` hook to a loopback route, that posts the question to the same hub
   and waits for the web's answer, returning `updatedInput.answers`. In VS Code
   it races the IDE dialog (first answer wins), which is exactly the wanted
   "both ways". Open question to measure before building: in a plain terminal,
   is the dialog drawn while the hook waits?
3. Do not type into terminals. Remote Control is fine for the owner personally
   but cannot carry aiagent's web or app.

## What was built (prototype, off by default)

- `aiagent serve --channels` (default **off**). On start it always deletes
  `<state-dir>/channel.key`; with the flag it writes a fresh random key there
  (0600) and creates the in-memory hub (`internal/world/channels.go`). Without
  the flag the channel routes answer 404 and nothing changes. Refused together
  with `--os-users` for now.
- `aiagent channel` (`cmd/aiagent/channel.go`, `internal/world/channelmcp.go`):
  a stdio MCP server, no SDK, that declares `claude/channel` and
  `claude/channel/permission`, never offers MCP 2026-07-28, and talks only to a
  loopback `--url` (default `http://127.0.0.1:8890`, `AIAGENT_URL`).
  - Session: its parent's entry in Claude Code's pid registry
    (`~/.claude/sessions/<ppid>.json`), else `CLAUDE_CODE_SESSION_ID`; re-read
    before every poll, so `/clear` or `/resume` re-registers.
  - Credentials: reads the key file (refused if group/other-readable),
    registers, and gets a **per-session secret**. Nothing is in argv; stdout is
    MCP only; errors never include headers.
  - Pushes each web message as `notifications/claude/channel`
    (`meta.via = "aiagent"`), relays permission prompts to the service and
    the web's verdict back. No reply tool: replies reach the web through the
    transcript aiagent already reads.
- Service routes (`internal/world/channels_http.go`), outside the token gate,
  404 unless channels are on **and** the caller is a direct loopback peer with
  no `Cf-Connecting-Ip` / `X-Forwarded-For` / `Forwarded` (the tunnel and
  proxies are never accepted); wrong key/secret counts toward the lockout:
  - `POST /aiagent/channel/register` key → `{secret}`; re-registering revokes the old secret.
  - `POST /aiagent/channel/poll` secret → `{events}`, long-polls up to 25 s.
  - `POST /aiagent/channel/permission` secret → shows the prompt on the web.
- Web side: a live (polled within 60 s) interactive session gets
  `messageVia: "channel"`; `POST /aiagent/api/sessions/:id/message` goes to its
  channel through the usual token gate, audit and send-once; the relayed
  permission prompt appears as the session's `prompt` (1 Yes / 2 No, input
  preview in the Yes option's detail) and `POST /aiagent/api/sessions/:id/answer
  {"option": n}` returns the verdict. No web/app UI change was needed.

## How to enable

1. Run the service with channels on (it is off by default):
   `aiagent serve --channels` (add it to the launchd/systemd unit's arguments).
2. Register the channel for Claude Code, at user scope so every project has
   it (or in a project's `.mcp.json`). The server **must be named `aiagent`**:
   the name becomes the `<channel source>` the instructions refer to.

   ```json
   {
     "mcpServers": {
       "aiagent": { "command": "/usr/local/bin/aiagent", "args": ["channel"] }
     }
   }
   ```

   or `claude mcp add --scope user aiagent -- /usr/local/bin/aiagent channel`.
   Use the absolute path of the installed binary.
3. Terminal / cmux: start the session with
   `claude --dangerously-load-development-channels server:aiagent` and accept
   the warning ("I am using this for local development"). Only sessions started
   this way take web messages; others stay read-only as before.
4. VS Code (unverified): switch the extension to terminal mode and launch the
   CLI through a wrapper that adds the flag. `settings.json` (VS Code user settings):

   ```json
   {
     "claudeCode.useTerminal": true,
     "claudeCode.claudeProcessWrapper": "/Users/<you>/bin/claude-with-aiagent"
   }
   ```

   `~/bin/claude-with-aiagent` (chmod 755). The extension passes the bundled
   binary path as the first argument when it has one:

   ```sh
   #!/bin/sh
   if [ -x "$1" ]; then bin="$1"; shift; else bin=claude; fi
   exec "$bin" --dangerously-load-development-channels server:aiagent "$@"
   ```

   In the graphical panel the flag is expected to be ignored (SDK-hosted);
   check `/mcp` and the startup notice before relying on it.
5. Check: the startup banner says messages from `server:aiagent` "inject
   directly in this session"; in the web the session's message box opens
   instead of "ดูได้อย่างเดียว".

To turn it off: restart the service without `--channels` (the key is deleted
on start, so every channel stops registering), or start sessions without the
flag.

## Risks and what is unverified

- Nothing here has been run against a real Claude Code session: the tests
  drive the MCP server with a fake Claude Code over pipes and the real service
  over a loopback socket. Not verified: that Claude Code 2.1.325 registers this
  Go server as a channel, the exact `initialize` it sends, `<channel>`
  rendering, and permission relay end to end. **VS Code is entirely unverified.**
- Research preview: the flags and the notification contract may change; the
  development flag's warning appears at every start.
- Authority: anyone holding an aiagent web token that may message the session
  can now put text in front of Claude and **approve tool calls** in a terminal
  session the owner started. That is the same authority the relay already gives
  over background jobs, but it now reaches interactive sessions too. Keep the
  switch off on machines where user tokens exist that should not have that.
- Same OS user: any process running as the service's user can read the key and
  register for any session (it could read the session's files anyway). The
  per-session secret limits a channel to its own session; it is not a boundary
  against the same user.
- A permission prompt answered in the terminal is not reported back by Claude
  Code, so the web may keep showing it until the next prompt or a channel
  restart; a late web answer is then dropped by Claude Code (stale id). Phone
  push does not fire for channel prompts yet.
- AskUserQuestion is not reachable through channels; it needs the hook step.
- `--os-users` rooms are not supported (each room's sessions would have to
  register with that room's own instance).
