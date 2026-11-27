# Environments

The admin says, in the web (Accounts → **Environment**), which environment is
which: `netra-dev` is dev, `netra-uat` is uat, `netra-prod` is prod, and what
variables a program there needs (`DATABASE_URL`, `API_KEY`, ...).

**Owner decision:** a room's agent may hold credentials for **dev and UAT,
never prod**. Prod work goes through the admin, the same way merge requests do.

## What is stored

| File (state dir, `0600`) | Holds |
|---|---|
| `environments.json` | id, name, tier, note, variables (key, description), times. No value. |
| `environments-secrets.json` | environment id → key → value. Nothing else. |

- Name: `^[a-z0-9][a-z0-9-]{0,39}$` (it becomes a file name in a room's home).
- Tier: `dev`, `uat` or `prod`.
- Note: up to 300 characters, one line. Description: up to 200, one line.
- Key: `^[A-Z][A-Z0-9_]{0,63}$`. Reserved, refused: `PATH`, `HOME`, `USER`,
  `LOGNAME`, `SHELL`, `IFS`, `ENV`, `BASH_ENV`, `PS1`, `PS4`, `PROMPT_COMMAND`,
  `TMUX`, `TMUX_TMPDIR`, `TERM`, `PWD`, and anything starting `LD_`, `DYLD_`,
  `AIAGENT_`, `BASH_FUNC_`, `CLAUDE_`, `GIT_`.
- Value: up to 4096 bytes, valid UTF-8, one line, no control characters
  (tab allowed). Not trimmed.
- At most 30 environments, 60 variables each; a request body over 16 KiB is
  refused (413) before it is parsed.

A value is **write-only**: it is sent once and never returned. `GET` lists each
variable with `"set": true|false`. Saving a variable with an empty value keeps
the stored one (or leaves it unset); deleting the variable deletes the value.
Deleting an environment deletes all its values.

## API (admin only)

All under `/aiagent/api`. None is in `userRoutes`, so a user token gets 403
before a handler runs. All are in `localPath`, so while the admin looks at a
room (`X-Aiagent-As`) they are still answered here and never forwarded to the
room's instance. Every write goes through `mutate` (rate limit + audit intent
and result); the audit parameters are the id, name/tier or key, never the value.

| Method | Path | Body |
|---|---|---|
| GET | `/environments` | |
| POST | `/environments` | `{name, tier, note}` |
| POST | `/environments/:id/edit` | `{name, tier, note}` |
| POST | `/environments/:id/remove` | |
| POST | `/environments/:id/vars` | `{key, description, value}` (add or change) |
| POST | `/environments/:id/vars/:key/remove` | |

## Who gets what

`deliverTo(duty, tier)` in `internal/world/environments.go` is the one place
that decides:

| Room duty | dev | uat | prod |
|---|---|---|---|
| frontend, backend, fullstack, admin | yes | yes | **never** |
| tester | no | no | **never** |
| none set | no | no | **never** |

Tester gets nothing: it runs no app and tests the shared UAT through the
browser. A room whose token is being removed gets nothing. (`fullstack` is
accepted here already; it is delivered as soon as `roomduties.go` knows it.)

## Delivery

Each room gets one file per environment:
`/home/<room>/.config/aiagent/env/<name>.env`, mode `0600`, owned by the room,
folder `0700`:

```sh
# netra-dev (dev), from the admin's aiagent. Replaced on every sync; do not edit.
DATABASE_URL='...'
```

Values are single-quoted for a POSIX shell, so an agent loads one for a single
command: `set -a; . ~/.config/aiagent/env/netra-dev.env; set +a; <command>`.
A variable with no value is left out.

**Who writes it.** The admin service runs with `ProtectSystem=strict` and a
read-only `/home`, so it cannot. `aiagent room-env-sync` does, from
`packaging/aiagent-room-env.service` (root oneshot), started

- by the admin service after every environment write and after a duty change
  (`systemctl start --no-block aiagent-room-env.service`, the same way
  `osuser.sh` units are started), and
- by `aiagent-room-env.timer` every minute, which also catches a duty edited
  by hand in `/etc/aiagent/room-duties.conf`.

Root never writes into a room's home itself: a room owns its home and could
plant a symlink there. For each room, root

1. checks the account is one `osuser.sh` made: in group `aiagent-users`, uid
   700000-799999, home exactly `/home/<room>`; anything else is refused;
2. starts `aiagent room-env-write` **as that room's uid/gid, with no
   supplementary groups**, an environment of only `HOME`, `PATH`, `LANG`, and the
   room's files as JSON on stdin;
3. the writer (which refuses to run as root) makes `~/.config/aiagent/env`
   hold exactly those files and removes every other `*.env` there. Files that
   are not `*.env` are left alone.

So removing a variable or an environment, moving one to prod, or changing a
room's duty to tester takes the file away on the next sync. One room failing
does not stop the others; errors name the room, never a value.

The unit: `NoNewPrivileges`, `CapabilityBoundingSet=CAP_SETUID CAP_SETGID`,
`PrivateNetwork`, `PrivateTmp`, `ProtectSystem=strict` with only `/home` and
`/etc/aiagent` writable. It reads the state from `/root/.local/state/aiagent`
(the admin service's default); if the admin runs with another state dir, change
`--state-dir` in the unit.

**What the rooms are told.** `room-env-sync` also writes
`/etc/aiagent/room-environments.conf` (`0644`, `name=tier` lines, no variable
and no value). `room-share.py` turns it into an "Environments" table in the
rooms' shared `CLAUDE.md`: each name, its tier, and where the file is (or, for
prod, that it goes through the admin), with the rule never to print, log,
commit or paste a value. `packaging/room-settings.json` also denies Claude's
`Read` tool on `~/.config/aiagent/env/**`, so the file's contents are not read
into a transcript by accident.

## Threat model

| Threat | Answer |
|---|---|
| A user token reads or changes environments | Not in `userRoutes`: 403 before any handler. Tested. |
| The admin, looking at a room, sends a value to that room's instance | Paths are `localPath`: answered by the main service. Tested. |
| A value comes back to the browser | Never in any response; the list carries `set` only. Tested. |
| A value in logs or audit | Audit stores a digest of id/name/key; errors carry no value. Tested for the audit log. |
| A prod credential reaches a room | `deliverTo` returns false for prod for every duty; the plan never holds prod. Tested. |
| A room keeps a credential after its duty changed or the variable was removed | Every room is synced every time (an empty set removes its files); a duty change starts a sync. Tested. |
| Another room reads the file | `0600`, owned by the room, folder `0700`, homes are `0700`. |
| A room plants a symlink to make root overwrite a system file | Root never writes in a home; the writer runs as the room and can only touch what the room could. |
| A crafted variable changes the room's shell (`PATH`, `LD_PRELOAD`, `BASH_ENV`) | Reserved names refused; values one line, single-quoted. |
| A huge request | 16 KiB body limit, field limits, count limits, one write per second. |
| The room's agent prints the value | Not preventable by this service once the agent has it: that is the accepted cost of dev/uat credentials in rooms. Mitigated by the rules in the shared `CLAUDE.md`, the `Read` deny, and the page's redaction of known secret shapes. Never give a room a credential you would not accept leaking from dev or UAT. |
| Someone with root on the host | Out of scope: root can read everything, as with the vault. |

## Not verified

The tests run on macOS: the plan, the write-only API, file modes, stale-file
removal and shell quoting are tested; `roomAccount` is tested to refuse
non-rooms. The real path on Linux, root starting the writer as a room's uid
under the systemd unit, and the files ending up owned by the room, has not been
run. Check it on the first install:

```sh
systemctl daemon-reload && systemctl enable --now aiagent-room-env.timer
systemctl start aiagent-room-env.service; systemctl status aiagent-room-env.service
ls -la /home/<room>/.config/aiagent/env/     # -rw------- <room> <room>
```

Existing machines get the units from `packaging/install.sh --os-users`; the
`room-settings.json` deny is only installed where that file is absent, so add
`"Read(~/.config/aiagent/env/**)"` to `/usr/local/share/aiagent/room/settings.json`
by hand.
