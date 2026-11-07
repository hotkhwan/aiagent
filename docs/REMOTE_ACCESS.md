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

## The one ordering rule

**Create the Access application before switching Remote Login on.**

Between those two steps `ssh.joinanny.com` is a plain TCP path to port 22 for
anyone who asks for it, and the only thing left is the host key. The steps
below are in the order that never opens that window: the tunnel can route to
port 22 all it likes while nothing is listening there.

---

## 1. On the Mac mini — tunnel config (done)

`~/.cloudflared/config.yml` already has the rule:

```yaml
ingress:
  - hostname: aiagent.joinanny.com
    service: http://127.0.0.1:8890
  - hostname: ssh.joinanny.com
    service: ssh://localhost:22
  - service: http_status:404
```

Verified with `cloudflared tunnel ingress validate` → OK, and
`cloudflared tunnel ingress rule https://ssh.joinanny.com` → matches rule #1.

The DNS record and the tunnel restart are **not** done — both are blocked for
this session. Run them yourself:

```sh
cloudflared tunnel route dns aiagent ssh.joinanny.com
launchctl kickstart -k gui/$(id -u)/com.aiagent.cloudflared
```

Until the DNS record exists the hostname does not resolve; after it exists and
before Remote Login is on, it resolves and the tunnel answers
`connection refused`. Both states are safe.

## 2. In the Cloudflare Zero Trust dashboard — the Access application

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

Nothing else in the application needs changing. Browser-rendered SSH — a
terminal inside the Cloudflare dashboard — is a separate feature and is not
what this sets up; this is a real SSH session from a real terminal.

## 3. On the Mac mini — switch Remote Login on

Needs your password, so it cannot be done from here:

```sh
sudo systemsetup -setremotelogin on
```

or System Settings → General → Sharing → Remote Login.

Check it took:

```sh
lsof -nP -iTCP:22 -sTCP:LISTEN
```

**Optional, and worth considering.** If you only ever want to reach this
machine through the tunnel and from the machine itself — never from another
box on the LAN — add `ListenAddress 127.0.0.1` to `/etc/ssh/sshd_config`.
That matches what the tunnel config says about the origin being loopback only.
Skip it if you also SSH in over the local network.

## 4. On the MacBook — the client

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
Access session is cached for as long as the session duration you set, and
`ssh macmini` just connects.

### The Mac mini has no authorized_keys yet

There is no `~/.ssh/authorized_keys` on the Mac mini, so the first connection
will ask for the account password. That is fine — Access has already
established who you are before the password prompt is reached — but install a
key and stop using the password:

```sh
# on the MacBook, after the first successful ssh
ssh-copy-id macmini
```

If the MacBook has no key yet, `ssh-keygen -t ed25519` first.

---

## Why not Tailscale

Tailscale is installed on this machine and would also do this. Cloudflare
Access was chosen because the Zero Trust account already exists, the tunnel is
already running for the web UI, and this adds one hostname to it rather than a
second overlay network with its own identity and its own thing to keep logged
in.

## What this does not change

- The web UI still authenticates with its own token. No Access application is
  in front of `aiagent.joinanny.com`, so the PWA on the iPad keeps working
  from its home-screen icon.
- The aiagent service still listens on loopback only and still answers 404 for
  everything that is not its own path.
- Nothing about the agent write surface changes: `agcmd`, the relay and the
  audit log are exactly as they were.
