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
  to be running an agent CLI — an allowlist (`claude`, its versioned binary
  name such as `2.1.318`, `codex`, `gemini`), not a list of shells to avoid —
  so neither a bare shell nor an editor or REPL left in the agent window
  receives the text. A pane tmux reports as `node` counts only when the
  process tree under it is the codex or gemini script, never a bare node
  REPL. The session is matched exactly and the text goes to the pane id that
  was checked.
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
- A **room's preview page** is agent-written too, and lives on an origin that
  may serve other applications. It is framed by a small shell of this service
  and sandboxed without `allow-same-origin`, in the frame and again in the CSP
  of every response, errors included. Response headers from a room are an
  allowlist. Content is reached by a 192-bit capability bound to one room and
  one viewer, dead ten minutes after the shell closes, eight hours at most, or
  at once when the room's (or the admin's) token is revoked or replaced. The
  preview host answers nothing else, and the rest of aiagent does not answer on
  it. What this does not stop: a page can look like a sign-in form; anyone who
  gets a live capability address can read that one page; and a script running
  in another application **on the preview origin itself** can act as its viewer
  there, because it shares the origin.
- Scene images are served from an embedded filesystem through a name
  allowlist, so a path can never escape into the real filesystem.

## Input

- **Uploads accept images and zip archives only**, verified by content rather
  than by filename or declared type (an image by its magic bytes, at most
  3.5 MB; a zip by reading its directory, at most 50 MB, never empty), stored
  `0600` under a random name, and deleted automatically after 7 days. Maximum
  10 per message.
- **A zip is stored, never opened.** The service does not unpack it, does not
  turn a name inside it into a path, and does not serve it back to a browser
  (the read route admits picture names only). What an agent does with the
  archive happens in that agent's own sandbox, as with any file it is given.
- The HTTP body limit is 51 MB so that a zip can arrive. It applies to every
  route, and the body is read before the token is checked, so a request
  without a token can make the service hold up to that much for a moment.
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
- An upload that is neither an image nor a zip, or an image bomb by count.
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

### "Remember on this device" (2026-11-07)

The token lives in `sessionStorage`, so closing the tab forgets it. That is
still the default and still the right one on a shared machine.

A home-screen PWA gets a fresh session on every cold start, which meant
pasting the token in each time you glanced at your phone — enough friction to
defeat the point of having it there. The login now offers a checkbox that puts
the token in `localStorage` instead. It is **off unless ticked**, "Sign out"
clears both stores, and `aiagent rotate-token` still kills a remembered token
everywhere at once.

What ticking it means: anyone with the unlocked device has the service. On a
phone with a passcode that is the same bar as the phone itself; on a shared
desktop, leave it off.

### SSH on the same tunnel (owner, 2026-11-07)

The tunnel now carries a second hostname, `ssh.joinanny.com`, routed to
`ssh://localhost:22` so the owner can reach the shell from the MacBook. That
one **is** behind Cloudflare Access; the setup is in
[REMOTE_ACCESS.md](REMOTE_ACCESS.md).

The two gates are deliberately different. The web UI is a home-screen PWA with
no interactive login, so it carries a token. A shell is not that.

The order matters: the Access application has to exist before the tunnel
carries the route. Between the two, the hostname is a public SSH prompt, and
on a default macOS that prompt accepts passwords.

That happened for a few minutes on 2026-11-07. The route went live first,
because `lsof -nP -iTCP:22 -sTCP:LISTEN` printed nothing and was read as
"Remote Login is off" — but macOS socket-activates `sshd`, so launchd holds
the socket and a process listing shows nothing either way. The ingress is
commented out again and `ssh.joinanny.com` answers 404 until the Access
application exists.

Two lessons worth keeping:

- **Ask a service whether it is up by connecting to it.** `ssh -o BatchMode=yes
  hotkhwan@127.0.0.1 true` answers in one line; a process listing does not.
- **Keys only, before the route goes live.** With `PasswordAuthentication no`
  the same mistake is an annoyance, not an incident.

## Before you expose it

- [ ] `aiagent status` shows enabled, and you hold the token privately.
- [ ] `lsof -nP -iTCP:8890 -sTCP:LISTEN` shows `127.0.0.1`, never `0.0.0.0`.
- [ ] A private network or authenticating proxy is in front of it.
- [ ] The state directory is `0700` and owned by the service user.
- [ ] You have read what the token actually grants, above.
