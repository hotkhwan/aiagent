# Security

This service can read your agents' conversations and type into them. That is
the whole point of it, and it is also exactly why the boundaries below exist.
Read the [threat model](#threat-model) before you expose it to anything.

## Reporting a vulnerability

Open a private security advisory on the GitHub repository rather than a public
issue. There is one maintainer, so expect a human-speed reply, not an SLA.

## Access

- **Off by default.** Until `aiagent enable` has run, every route — including
  the page itself — answers `404`. A disabled install is indistinguishable
  from an absent one, so scanning for it tells an attacker nothing.
- **Token.** `aiagent enable` and `aiagent rotate-token` print a `kaw_…` token
  **once**. Only `sha256(token)` is written, to a `0600` file. If that file
  cannot be read, every request is refused rather than allowed.
- **Lockout.** More than 10 wrong tokens from one address in 5 minutes locks
  that address out.
- **Write rate limit.** One write per second, globally.
- **Audit.** An append-only JSONL log records the *intent* before an action and
  the *result* after it, so a crash in between still leaves evidence that
  someone asked. Parameters are stored as a digest, never verbatim.

## Dispatch

- **No endpoint runs a shell.** Every external command is executed with a fixed
  argv. There is no `sh -c`, no string interpolation into a command line, and
  no path from request text to a shell.
- **Only configured workspaces.** A workspace name from the browser must match
  an entry in `workspaces.json` or the request is refused; the name itself
  never reaches a command line. With no config, nothing is dispatchable.
- **Only real agent panes.** A message is typed only into a tmux pane confirmed
  to be running an agent CLI (`claude`/`codex`/`node`), never a bare shell —
  otherwise the dispatcher would create an empty session and type your text
  into bash.
- **Read-only by design:** sub-agents, and sessions you started yourself in a
  terminal or VS Code, can be watched but not driven.
- Text is length-limited, stripped of control characters, and rejected if it is
  a reserved dispatcher keyword.

## What leaves the machine

- **Secrets are stripped** before any conversation text reaches the browser:
  tokens, keys, JWTs, passwords, private keys, and URLs carrying credentials.
- **Monitor commands are never sent to the browser at all**, because the
  command line of a watched job can itself contain a credential.
- Redaction is pattern-based. It is a strong second line of defence, not a
  guarantee: assume anything an agent prints could in principle reach the page.

## The browser surface

- Strict CSP: `default-src 'self'`, `script-src 'self'`, `frame-ancestors
  'none'`, `base-uri 'none'`, `form-action 'none'`. **No inline script.**
- `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`.
- A **review page is agent-authored HTML** and is therefore treated as hostile:
  it opens in an iframe with `sandbox="allow-scripts"` and `default-src 'none'`,
  behind a single-use ticket that expires in two minutes.
- Scene images are served from an embedded filesystem through a name
  allowlist, so a path can never escape into the real filesystem.

## Input

- **Uploads accept images only**, verified by magic bytes rather than by
  filename or declared type, stored `0600`, and deleted automatically after
  7 days. Maximum 10 per message.
- Model names are checked against a regex or the provider's real list.
  `~/.codex/config.toml` is edited only on its top-level `model` line.

## Accounts

Switching accounts runs the provider's own login flow; this service never
handles a password and never stores a provider credential.

> **`codex login` logs you out the moment it starts.** If the flow is
> interrupted you are left signed out, and you must finish it on the machine to
> get back in. This is the CLI's behaviour, not something this service can fix.

## Privileges

Run it as the **same unprivileged user as the agents it watches**. It needs
read access to `~/.claude`, `~/.codex` and `/proc`, and it needs to run `tmux`,
`claude` and `codex`. It needs nothing else — in particular it does not need
root. The systemd unit uses `ProtectSystem=strict` with a single writable path.

Root is only justified if you deliberately want one instance to watch several
users, and it widens the blast radius of any bug to all of them.

## Threat model

**Defended against**

- An unauthenticated network client: 404 while disabled, 401 without the token,
  lockout on guessing.
- Command injection through any request field: there is no shell to inject
  into, and workspaces resolve through a fixed table.
- A hostile review page: sandboxed, single-use ticket, no same-origin access.
- A non-image upload, or an image bomb by count.
- Accidental credential leakage into the page: redaction plus the blanket rule
  on monitor commands.

**NOT defended against**

- **Anyone who has the token.** There is one role. A token holder can read
  every watched conversation and type into any dispatchable session. Treat it
  as equivalent to a shell as that user.
- **Anyone with local read access to the state directory or the agent homes.**
  File permissions are the only boundary there.
- **A malicious or compromised agent.** This service faithfully relays what
  agents write. Prompt injection reaching an agent is outside its control; it
  can only reduce what that reaches *you*.
- **Multi-tenant use.** It is a single-operator tool. There are no roles, no
  per-user scoping, and no audit separation between operators.
- **Traffic interception.** There is no transport security of its own on
  loopback. Anything beyond this machine must be wrapped by you.

## Posture chosen for this deployment (owner, 2026-11-06)

The owner's instance runs behind a Cloudflare Tunnel on a public hostname with
**the token as the only gate** — Cloudflare Access was offered and declined.

Written down plainly so nobody has to guess later: that means the whole of
"NOT defended against" above applies to anyone on the internet who obtains the
token, and the lockout (10 wrong tokens per address per 5 minutes) is the only
thing standing between a guesser and the service. It is a 32-byte random
token, so guessing is not the realistic risk; losing or pasting it is. Rotate
with `aiagent rotate-token` the moment you suspect it has leaked — the old one
stops working immediately.

### SSH on the same tunnel (owner, 2026-11-07)

The tunnel now carries a second hostname, `ssh.joinanny.com`, routed to
`ssh://localhost:22` so the owner can reach the shell from the MacBook. That
one **is** behind Cloudflare Access; the setup is in
[REMOTE_ACCESS.md](REMOTE_ACCESS.md).

The two gates are deliberately different. The web UI is a home-screen PWA with
no interactive login, so it carries a token. A shell is not that.

The order matters and the runbook says so: the Access application has to exist
before Remote Login is switched on. In between, the hostname is a plain TCP
path to port 22 and the host key is all that is left. Routing to a port with
nothing listening on it is safe; routing to a live sshd with no Access
application is not.

## Before you expose it

- [ ] `aiagent status` shows enabled, and you hold the token privately.
- [ ] `lsof -nP -iTCP:8890 -sTCP:LISTEN` shows `127.0.0.1`, never `0.0.0.0`.
- [ ] A private network or authenticating proxy is in front of it.
- [ ] The state directory is `0700` and owned by the service user.
- [ ] You have read what the token actually grants, above.
