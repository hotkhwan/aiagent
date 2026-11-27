# Security

This service can read your agents' conversations and type into them. That is
the whole point of it, and it is also exactly why the boundaries below exist.
Read the [threat model](#threat-model) before you expose it to anything.

## Reporting a vulnerability

Open a [private security advisory](https://github.com/hotkhwan/aiagent/security/advisories/new)
on the GitHub repository rather than a public issue. Leave tokens, pairing QR
codes and other secrets out of the report; describe how to obtain them instead. There is one maintainer, so expect a human-speed reply, not an SLA.

## Access

- **Off by default.** Until `aiagent enable` has run, every route — including
  the page itself — answers `404`. A disabled install is indistinguishable
  from an absent one, so scanning for it tells an attacker nothing.
- **Token.** `aiagent enable` and `aiagent rotate-token` print a `kaw_…` token
  **once**. Only `sha256(token)` is written, to a `0600` file. If that file
  cannot be read, every request is refused rather than allowed.
- **Lockout.** More than 10 wrong tokens from one address in 5 minutes locks
  that address out. The token is checked first and a valid one is always let
  in, so a lockout only stops guessing: behind the tunnel the address is the
  `Cf-Connecting-Ip` a loopback peer names, which anyone on the machine can
  set, and they must not be able to lock the owner out. A full failure table
  (4096 addresses) drops its stalest address, never the whole table. Pairing
  redeems (`POST /aiagent/pair`) follow the same rule: a live code is taken
  from any address, only misses are counted.
- **Write rate limit.** One write per second per identity: the admin (every
  admin token and admin phone together) has its own, and each user token
  (with its phones) its own, so a user can never slow the admin or another
  user. Uploads likewise: 30 a minute for the admin, 30 for each user.
- **Host names (optional).** `serve --allowed-hosts a.example,b.example` (or
  `AIAGENT_ALLOWED_HOSTS`) answers only those Host names and loopback ones
  (`localhost`, `127.0.0.0/8`, `::1`); any other name gets 404 on every route,
  page included. Off by default, so existing installs keep working; set it to
  the tunnel's hostname to shut out DNS-rebinding pages and stray names
  pointed at the box. A room's instance (`--listen systemd`) ignores it: it
  is reached only over its own socket.
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
- **A user token cannot type CLI commands**, with or without `--os-users`
  (`internal/world/usertext.go`, checked in the gate before a request is
  answered or forwarded to a room): text starting with `!` (shell mode), `&`
  (cloud session), their full-width forms, or `/` is refused with 403
  `AIAGENT_COMMAND_BLOCKED`, except the slash commands `/compact`, `/clear`,
  `/context`, `/usage` and `/rename`. Leading spaces, control, format and
  combining characters are skipped first, so `"  !x"` or a zero-width
  character before `/login` changes nothing. A message that starts with a
  path (`/home/x/file`) is refused too: put a word first; the web's
  attachment lines always start with a label. It covers `POST /messages`,
  `POST /sessions/:id/message` (and what the outbox later types from it) and
  the free-text answers of `POST /sessions/:id/answer`. A review answer is
  not checked because the service always begins it with its own fixed line,
  and a session name is typed after `/rename ` on one line. The admin is not
  restricted.
- **Protected workspaces are the admin's whole.** A user sees a protected
  session by name and state only, and no workspace, pending message or run
  (agcmd task and its decisions) of a protected workspace at all.
- **A room cannot claim another room's ids.** With `--os-users` the admin's
  service learns which room a session or review page lives in from the rooms'
  own lists. A session id or review id that two rooms both list is routed to
  neither (and logged): the admin's message or answer to it is never sent to
  a room that merely claims it, and the review is left out of the list.

## What leaves the machine

- **Secrets are stripped** before any conversation text reaches the browser:
  tokens, keys, JWTs, passwords, private keys, and URLs carrying credentials.
- **Monitor commands are never sent to the browser at all**, because the
  command line of a watched job can itself contain a credential.
- Redaction is pattern-based. It is a strong second line of defence, not a
  guarantee: assume anything an agent prints could in principle reach the page.
- **Phone notifications** (`internal/world/push.go`) go through a relay
  (`--push-relay`, default `https://aiagent-push.hotkhwan.workers.dev`;
  `--push-relay off` turns them off). Per item the relay receives the
  device's handle, the kind (`answer` / `waiting` / `review` / `done`), the
  session's or review page's id, the character's look, and the session's name
  or the review page's title. **That name or title can be agent-written** (an
  agent can rename its session; a review page's title is the agent's), so it
  is the one piece of agent text that leaves through the relay, and the relay
  operator sees it. A phone that registers with `{"handle": …, "quiet": true}`
  is sent an empty name (the payload keeps its shape), and the relay itself
  drops names for a phone set to hide them. Never a token, never an agent's
  messages, never a page's contents. The relay writes the notification text
  from fixed phrases around that name.
- A phone is registered with the token it presents (`POST /push/devices`) and
  bound to that token's SHA-256 in `push.json` (`0600`). Revoking, reissuing or
  rotating the token stops its pushes at the next tick and drops the device.
  A user's phone only hears about what that user may see on the web. Users'
  phones are capped (200 together, 20 per user, 5 per token); the admin's are
  not counted against those, so users cannot fill the admin out.
- The connect code (`aiagent://connect?u=…&t=…`) holds a token. Its QR is drawn
  by this service (`POST /connect/qr`, `Cache-Control: no-store`, not audited
  so the token never reaches a log); a user can draw only their own, an admin
  also one they just made. No outside QR service is ever used. The web page
  shows it only on the tokens page, for a token just made; it is kept for
  older app builds and the App Review demo.
- The header's **📱 App** dialog shows a pairing code instead
  (`internal/world/pair.go`): `aiagent://pair?c=pair_<43 base64url>&u=<origin>`.
  `POST /api/connect/pair` (any valid token) stores, in memory only,
  sha256(ticket) → the presented token, for 10 minutes; a new code ends the
  older codes of the same token, closing the dialog ends them
  (`POST /api/connect/pair/cancel`), at most 50 live tickets, and a restart
  empties the table. The app redeems it at `POST /aiagent/pair` (no token, 404
  while the service is off): the ticket is deleted on the first try, so one
  redeem wins, and the page's token is checked again so a revoked or reissued
  one pairs nothing. Every failure (bad shape, unknown, used, expired, revoked)
  answers the same 404 `AIAGENT_PAIR_INVALID`; 10 failures a minute from one
  address answer 429 to further misses (a live code is still taken). Every
  redeem writes `aiagent.pairRedeem` ok|denied with
  the caller's address to the audit log before anything is sent — never the
  code or a token. Both endpoints send `Cache-Control: no-store`.
- **The phone gets a token of its own** (`internal/world/devices.go`), never
  the page's: a new `kaw_` token with exactly the page's scope (the admin's,
  or one room's), stored as its SHA-256 in the state file. `GET
  /api/connect/devices` lists the phones in the caller's scope (the admin
  sees all, a room's user only theirs) with no hashes; `POST
  /api/connect/devices/:id/revoke` ends one at once, audited as
  `aiagent.deviceRevoke`. `aiagent rotate-token` ends every admin-scoped
  phone, since whoever held the old token could have paired one; reissuing or
  revoking a room's token ends that room's phones; `disable` forgets them all.
  A phone's token cannot draw a pairing code (403), so a stolen phone cannot
  mint tokens that outlive its own revoke.
  Phones paired before this change hold the page's token and keep working
  until it is rotated.
- **What a phone's token may call** is an allowlist
  (`internal/world/devicegate.go`), like a user token's: sessions (read,
  message, answer, stop, name, look, model, resume), reviews, the snapshot,
  faces, uploads, its own push registration and the device list. Vault,
  updates, CLI updates, provider accounts, policy, room policy (`POST
  /rooms`), Codex model, environments and any route added later answer 403
  `AIAGENT_DEVICE_FORBIDDEN` to a phone, admin or not; those stay on the web
  with the admin's own token. A room's phone is held to the user allowlist as
  well.
- **User tokens made from an admin phone end with it.** An admin phone may
  list, make, reissue, revoke and set the duty of user tokens, because every
  token it makes or reissues records that phone (`createdByDevice` in the
  state file). Revoking the phone, or `aiagent rotate-token` (which ends
  every admin phone), gives each of those a hash no one holds (they stop at
  once; the rows and rooms stay, to be reissued from the web) and ends the
  phones paired under them. A token reissued on the web is no
  longer tied to any phone.
  Phones are capped per scope: 100 for the admin, 20 per room.

- **Photo faces** (`internal/world/faces.go`, any role, each token only its own;
  answered by the main service even in `--os-users` mode):
  - `GET /api/faces` → `{"accountForAll","characterOnly":[ids],"account":"<v>"|"","sessions":{id:"<v>"}}`,
    `<v>` = first 16 hex of sha256 of the stored PNG.
  - `GET|PUT|DELETE /api/faces/account` and `/api/faces/sessions/:id`: GET is
    `image/png` with `ETag: "<v>"`, `Cache-Control: private, no-cache`, 404 when
    none; PUT takes the raw `image/png` or `image/jpeg` body (≤ 2 MB, 16–2048 px
    a side, else 415/413/400) and answers `{"v"}`; DELETE is idempotent.
  - `PUT /api/faces/settings` `{"accountForAll":bool,"characterOnly":[≤ 500 ids]}`
    answers the manifest.
  - Stored in `<state-dir>/faces/<owner>/` (`admin`, or the user token's id):
    `account.png`, `s_<sessionId>.png` (at most 300, 409 when full),
    `settings.json`. Directories `0700`, files `0600`, written by temp file and
    rename; path parts come only from checked ids (`[A-Za-z0-9-]{1,64}`).
  - A photo is decoded and encoded again as a PNG of at most 512 px a side, so
    EXIF, GPS and every other chunk or segment the phone wrote is dropped; the
    original bytes are never kept. Writes are audited and rate limited like
    every other write, with ids and sizes only, never the picture.
  - Revoking a user token deletes that user's photos. A reissued token keeps
    them (the id stays).

- **Updating from the page** (`internal/world/selfupdate.go`, admin only, audited):
  the service fetches the upstream of the checkout `packaging/install.sh` was
  run from (recorded in `<state-dir>/source`, or `--update-repo`) and offers an
  update only as a clean fast-forward. Applying runs `aiagent self-update` in its
  own session: `git pull --ff-only`, `packaging/install.sh`, restart. It trusts
  the upstream branch exactly as running `install.sh` by hand does. On Linux the
  systemd unit keeps the service read-only outside its state directory, so the
  check works but the update fails there with that reason; update by hand.

- **Updating from a release** (`internal/world/selfupdate_release.go`,
  `internal/release`, admin only, audited as `aiagent.update.release`): the
  🔔 asks `api.github.com` for the latest release at most every 6 hours (an
  admin may ask now). `POST /aiagent/api/update {"release":"vX.Y.Z"}` accepts
  only the current latest release, and only when it is newer than the running
  version, so an old signed release cannot be replayed to downgrade. Downloads
  go only over https to `github.com`, `objects.githubusercontent.com` and
  `release-assets.githubusercontent.com`, redirects included, from the
  release's own download URLs, with size caps. Nothing is installed or run until
  `SHA256SUMS` verifies against the ed25519 release key compiled into aiagent
  (`internal/world/release_pubkey.go`), the archive's sha256 is the one listed,
  and the archive holds exactly `aiagent` and `agcmd` as plain top-level files.
  `aiagent self-update --release-dir` verifies the folder again itself (it sits
  in the writable state directory), stages and optionally codesigns the
  binaries beside the live ones, keeps `.prev` copies, renames them into place
  and restarts the service. What this trusts: the owner's release key, and only
  that; whoever controls the GitHub account or the network cannot pass a
  release without it. The private key lives off the repo on the release Mac
  (`packaging/release.sh`). On Linux, as root under systemd, the installer runs
  as a transient unit (`systemd-run`), outside the service's read-only sandbox;
  a non-root systemd instance still cannot write its binary and fails with that
  reason.

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
- A **room's agent** runs every command in Claude Code's bubblewrap sandbox,
  where `~/.claude` is read-only and unix sockets are blocked. It files reviews
  and asks for captures by leaving a file in `~/work/.aiagent/in/`, which the
  room's own instance (the same user, outside that sandbox) collects. The
  instance never follows a link there, refuses hard links, fifos, files of
  another owner and world-writable folders, writes only names it chose
  (created fresh, then renamed), and checks that a page is UTF-8 HTML of at
  most 3 MB. A capture is started by the admin service only: it polls each
  room's instance over that room's root-owned socket, so the room is the socket
  it dialled; the only thing it takes from a room is a 32-hex request id. It
  captures that room's page alone, with the viewer grant checks, one at a time,
  20 an hour per room, audited, and returns the pictures over the same socket.
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

- **Anyone who has the admin token.** It can read every watched conversation
  and type into any dispatchable session. Treat it as equivalent to a shell as
  that user. A user token (a room) is narrower — its own sessions, read-only
  elsewhere, see the admin/user table under *Rooms* — but it still types
  into that room's agent, which runs commands as the room's OS user.
- **Anyone with local read access to the state directory or the agent homes.**
  File permissions are the only boundary there.
- **A malicious or compromised agent.** This service faithfully relays what
  agents write. Prompt injection reaching an agent is outside its control; it
  can only reduce what that reaches *you*.
- **Mutually distrustful tenants.** There are two roles, admin and user, and a
  user's requests go to their own room's instance, but it is still one
  operator's machine: the admin sees every room, and rooms share the host.
- **A room opening the service to the internet.** A room's agent runs on the
  same machine, so it can reach the service's loopback port and could start a
  tunnel to it on its user's say-so. Outbound network limits for rooms are the
  host's job.
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
- [ ] A private network or authenticating proxy is in front of it — or you
      have decided, as in the posture above, that the token is the only gate,
      and you keep it private and rotate it if it may have leaked.
- [ ] Pairing codes: only the newest code per token works, closing the 📱 App
      dialog ends it, and every redeem is in the audit log
      (`aiagent.pairRedeem`). A code on screen can pair one phone until then.
- [ ] Paired phones: each has its own token, listed in the 📱 App dialog;
      revoke a lost phone there (it takes the user tokens it made with it).
- [ ] Behind a tunnel or proxy with one public name, consider
      `--allowed-hosts <that name>`.
- [ ] The state directory is `0700` and owned by the service user.
- [ ] You have read what the token actually grants, above.
