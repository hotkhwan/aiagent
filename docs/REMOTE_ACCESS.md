# Remote access — SSH to the Mac mini over Cloudflare Access

The Mac mini runs the agents. This is how to reach its shell from the MacBook
(or anywhere) without opening a port on the router, and without the SSH
service being reachable by anyone who finds the hostname.

The same tunnel already serves the web UI. It gains a second hostname:

| Hostname | Goes to | Who is let in |
|---|---|---|
| `aiagent.joinanny.com` | `http://127.0.0.1:8890` | the aiagent token, checked by the service itself |
| `ssh.joinanny.com` | `ssh://localhost:22` | **Cloudflare Access**, checked before the connection reaches this machine |

Two different gates on purpose. The web UI is a PWA that has to work from a
home-screen icon with no interactive login, so it carries a token. A shell is
not that, and gets a real identity check.

---

## State right now

| | |
|---|---|
| DNS `ssh.joinanny.com` | **created**, CNAME to the tunnel |
| Tunnel ingress for it | **commented out in `~/.cloudflared/config.yml`** |
| Cloudflare Access application | **not created** — needs the dashboard |
| Remote Login on the Mac mini | **already on** |
| `~/.ssh/authorized_keys` on the Mac mini | **does not exist** |
| SSH password authentication | **on** (macOS default) |

So `ssh.joinanny.com` resolves but the tunnel has no rule for it and answers
404. Nothing is reachable. Finish it with §1 and §2 below, in that order.

### What went wrong on 2026-11-07, so it is not repeated

The ingress was added and the tunnel restarted before the Access application
existed, on the belief that Remote Login was off and the route would therefore
dial a dead port. That belief came from `lsof -nP -iTCP:22 -sTCP:LISTEN`
printing nothing — which on macOS means nothing, because launchd holds the
socket and only starts `sshd` when a connection arrives. `launchctl print
system/com.openssh.sshd` agreed, reporting `state = not running`, for the same
reason.

Remote Login was on. For a few minutes `ssh.joinanny.com` was a public SSH
prompt that accepted password attempts. The ingress was removed and the tunnel
restarted as soon as a real client test showed it.

**The check that actually answers the question is a connection, not a
process listing:**

```sh
ssh -o BatchMode=yes -o ConnectTimeout=6 hotkhwan@127.0.0.1 true
# "Permission denied (publickey,password,…)" means sshd is running.
# "Connection refused" means it is not.
```

---

## 1. Create the Access application

**one.dash.cloudflare.com → Access → Applications → Add an application →
Self-hosted**

| Field | Value |
|---|---|
| Application name | `Mac mini SSH` |
| Session duration | whatever suits — 24 h is reasonable |
| Public hostname | subdomain `ssh`, domain `joinanny.com`, path empty |

Then **Add a policy**:

| Field | Value |
|---|---|
| Policy name | `owner` |
| Action | `Allow` |
| Include | `Emails` → `hotkhwan@hotmail.com` |

Add the second address too if you want to sign in with either. Save.

Browser-rendered SSH — a terminal inside the Cloudflare dashboard — is a
separate feature and is not what this sets up. This is a real SSH session from
a real terminal.

## 2. Turn the route back on

Delete the four comment lines in `~/.cloudflared/config.yml` so the rule is
live again:

```yaml
  - hostname: ssh.joinanny.com
    service: ssh://localhost:22
```

Then:

```sh
cloudflared tunnel ingress validate          # from ~/.cloudflared
launchctl kickstart -k gui/$(id -u)/com.aiagent.cloudflared
```

The tunnel is **locally managed**, so routes live in that file and the
dashboard will not let you edit them — it only lists them.

## 3. On the MacBook — the client

```sh
brew install cloudflared
```

Add to `~/.ssh/config`:

```
Host macmini
    HostName ssh.joinanny.com
    User hotkhwan
    ProxyCommand /opt/homebrew/bin/cloudflared access ssh --hostname %h
```

Then:

```sh
ssh macmini
```

The first connection opens a browser for the Access login. After that the
Access session is cached for the duration you set and `ssh macmini` just
connects.

## 4. Install a key, then turn password auth off

The Mac mini has no `authorized_keys`, so the first connection asks for the
account password. Access has already established who you are by then, so that
is survivable — but do not leave it there:

```sh
# on the MacBook, after the first successful ssh
ssh-keygen -t ed25519          # only if you have no key yet
ssh-copy-id macmini
```

Once the key works, close the password door. In `/etc/ssh/sshd_config` on the
Mac mini:

```
PasswordAuthentication no
KbdInteractiveAuthentication no
```

then `sudo launchctl kickstart -k system/com.openssh.sshd`. This is what makes
a future mistake cheap: with keys only, a hostname briefly exposed is an
annoyance rather than an incident.

**Optional.** If you never SSH in from another machine on the local network,
add `ListenAddress 127.0.0.1` as well. The tunnel reaches `localhost:22`, so
it keeps working, and the LAN surface disappears. Skip it if you do use LAN
SSH.

---

## Why not Tailscale

Tailscale is installed on this machine and would also do this. Cloudflare
Access was chosen because the Zero Trust account already exists, the tunnel is
already running for the web UI, and this adds one hostname to it rather than a
second overlay network with its own identity to keep logged in.

## What this does not change

- The web UI still authenticates with its own token. No Access application is
  in front of `aiagent.joinanny.com`, so the PWA on the iPad keeps working
  from its home-screen icon.
- The aiagent service still listens on loopback only and still answers 404 for
  everything that is not its own path.
- Nothing about the agent write surface changes: `agcmd`, the relay and the
  audit log are exactly as they were.
