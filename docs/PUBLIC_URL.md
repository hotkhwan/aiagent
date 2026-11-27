# A public https address for aiagent — a playbook

aiagent listens on `http://127.0.0.1:8890/aiagent`. That address only exists on
the computer itself. The phone app needs an **https** address it can reach, so
someone has to put one in front of aiagent. aiagent does not run a tunnel for
you: you make one on your own account, or on your own server.

This page is written so that **one of your own AI agents can follow it** for
you. Open the dashboard, press **📱 App**, pick an option, and press *Send to an
agent* — or copy the instruction and paste it into any agent yourself. A human
can follow it the same way: every command is here.

| | What you get | Needs |
|---|---|---|
| [A. Try it now](#a-try-it-now--cloudflare-quick-tunnel) | a random `https://….trycloudflare.com` that changes every restart | nothing — no account |
| [B. Your own address](#b-your-own-address--a-named-cloudflare-tunnel) | `https://phone.your-domain.com`, permanent | a free Cloudflare account and a domain on it |
| [C. Cloud server](#c-a-cloud-server-with-a-public-ip) | `https://your-server-name`, permanent | a VM with a public IP and a DNS name |
| [D. Private](#d-private--tailscale) | `https://machine.tailnet.ts.net`, only your devices | Tailscale on the computer and the phone |

## What protects it once it is public

With options A, B and C, anyone on the internet can open the address. From
then on the aiagent token is the **only** thing between them and typing into
your agents. That token is long and random, failed guesses are rate limited,
and it never travels in a URL, so guessing it is not the risk; **losing** it
is. Keep it in a password manager, never paste it in a chat, and run
`aiagent rotate-token` if it may have leaked. A pairing code on screen is the
token too, until the phone redeems it or the dialog closes.

aiagent refuses to listen on anything but loopback without TLS. A tunnel does
not change that: `cloudflared` connects to `127.0.0.1` from the same machine,
and the https certificate is Cloudflare's, at Cloudflare's edge. Traffic
between the phone and Cloudflare is encrypted; between Cloudflare and aiagent
it stays on this computer.

If only your own devices need it, **D (Tailscale)** keeps it off the public
internet altogether, and is the safer choice. With option B you can also put a
**Cloudflare Access** policy (Zero Trust → Access → Applications) in front of
the hostname, so a browser must sign in to Cloudflare before aiagent sees a
request. The phone app does not sign in to Cloudflare Access, so a policy that
covers the whole hostname stops the app as well; use it only if you reach
aiagent from a browser, and check the app after any change.

To reach this computer's **shell** remotely, that is a different job with a
different gate: see [REMOTE_ACCESS.md](REMOTE_ACCESS.md). Nothing on this page
exposes ssh.

---

## Rules for the agent — read before running anything

These are not suggestions. If a step would break one, stop and tell the human.

1. **Expose only aiagent.** The only thing that may be reachable from the
   internet is `http://127.0.0.1:8890` (or the port aiagent really listens on —
   see *Before you start*). Never expose ssh (`ssh://`, port 22), never another
   port, never a catch-all proxy, never `tailscale funnel`.
2. **Say what will be exposed, then wait for a yes.** Before any command that
   opens this machine to the internet (starting a tunnel, adding a DNS route,
   starting a service, opening a firewall port), tell the human in one or two
   plain sentences what address will point at what, and who can reach it. Run
   it only after they answer yes. Asking once per option is enough; asking
   again for the undo is not needed.
3. **The human logs in, never you.** `cloudflared tunnel login` and every
   Cloudflare or Tailscale sign-in happens in the human's browser. **STOP and
   ask the human to finish the browser login**, then continue when they say it
   is done. Never ask for, accept, or type a Cloudflare password, API token,
   Global API Key, or Tailscale auth key in chat. If the human pastes one
   anyway, do not use it; tell them to revoke it.
4. **Secrets stay where they are.** Never print, `cat`, copy into chat, commit,
   or write elsewhere: the aiagent admin token, `~/.cloudflared/cert.pem`, a
   tunnel credentials file (`~/.cloudflared/<UUID>.json`), or anything under
   aiagent's state directory. Refer to them by path only.
5. **Do not weaken aiagent.** Do not change its token, do not turn off its
   switch to "test", do not add `--listen 0.0.0.0` without TLS (aiagent refuses
   it anyway), do not edit its code.
6. **Keep what is already there.** If `~/.cloudflared/config.yml`, a Caddyfile,
   a firewall rule or a Tailscale serve config already exists, do not
   overwrite or delete it. This playbook uses its own file names so it never
   has to. If an existing config already routes this hostname, stop and ask.
7. **Leave an undo.** When you finish, tell the human the address, how to stop
   it, and the undo steps for the option you used (each option has them).

### Before you start

```sh
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8890/aiagent/
```

`200` means aiagent is running and its switch is on. `404` on every path means
the switch is off — ask the human to run `aiagent enable`; do not run it for
them unless they ask. Nothing answering means aiagent is not running, or not
on 8890: ask the human which address the dashboard opens on and use that port
everywhere below.

Install `cloudflared` (options A and B):

```sh
# macOS
brew install cloudflared

# Debian / Ubuntu — Cloudflare's own package repository
sudo mkdir -p --mode=0755 /usr/share/keyrings
curl -fsSL https://pkg.cloudflare.com/cloudflare-main.gpg | sudo tee /usr/share/keyrings/cloudflare-main.gpg >/dev/null
echo 'deb [signed-by=/usr/share/keyrings/cloudflare-main.gpg] https://pkg.cloudflare.com/cloudflared any main' | sudo tee /etc/apt/sources.list.d/cloudflared.list
sudo apt-get update && sudo apt-get install -y cloudflared

# Fedora / RHEL
curl -fsSL https://pkg.cloudflare.com/cloudflared.repo | sudo tee /etc/yum.repos.d/cloudflared.repo
sudo dnf install -y cloudflared
```

Installing a package changes nothing that is reachable from outside; it does
not need the yes of rule 2, but tell the human you are installing it.

---

## A. Try it now — Cloudflare Quick Tunnel

No account. Cloudflare gives a random `https://<words>.trycloudflare.com` that
reaches aiagent while the command runs, and is gone when it stops. A new run
gets a new address, so the phone has to be paired again each time. Use it to
try the app; use B or C to keep it.

**What is exposed:** anyone who learns the random address reaches aiagent's
login page. They still need the aiagent token; wrong guesses are locked out
per visitor (Cloudflare sends the visitor's address in `Cf-Connecting-Ip`).

After the human's yes:

```sh
cloudflared tunnel --no-autoupdate --url http://127.0.0.1:8890
```

Keep it in the foreground, in a terminal or a tmux window the human can see,
**only while trying**. Do not install it as a service. The address is printed
in a box a few lines into the output:

```
|  https://example-words-here.trycloudflare.com  |
```

Give the human `https://<that address>/aiagent/` and go to [Finish](#finish).

Notes:
- If `~/.cloudflared/config.yml` exists, Cloudflare does not support a quick
  tunnel on that machine. Do not move or edit that file; tell the human and
  offer option B instead.
- Quick tunnels have no uptime guarantee and a limit on concurrent requests.
  That is fine for trying; it is why B exists.

**Undo:** stop the command (Ctrl-C, or kill that process). The address stops
working at once. Nothing was created on any account.

---

## B. Your own address — a named Cloudflare tunnel

A permanent `https://<host>` on the human's own Cloudflare account, pointing
only at aiagent. Needs a free Cloudflare account and a domain whose DNS is on
that account. Below, `aiagent-phone` is the tunnel name and
`phone.example.com` is the hostname — **ask the human which hostname they
want**, and use theirs everywhere.

**What is exposed:** `https://<host>` reaches aiagent's login page from
anywhere. Nothing else on the machine: every other hostname and path the
tunnel sees answers 404 at the tunnel itself.

### 1. Log in — the human does this

```sh
cloudflared tunnel login
```

It prints a URL and opens the browser. **STOP here.** Tell the human: "A
Cloudflare page is open in your browser — sign in and pick the domain
`example.com`, then tell me when it says it is done." Wait. Do not ask for
their password or an API token, and do not try another way in. When they
confirm, check that the login left its certificate, without printing it:

```sh
test -s ~/.cloudflared/cert.pem && echo logged-in
```

### 2. Create the tunnel and its DNS name — after the human's yes

```sh
cloudflared tunnel create aiagent-phone
cloudflared tunnel route dns aiagent-phone phone.example.com
```

`create` prints the tunnel's UUID and writes its credentials to
`~/.cloudflared/<UUID>.json`. Do not print that file. If `route dns` says the
name already exists, stop: something else uses it, ask the human.

If `~/.cloudflared/cert.pem` was **not** there before step 1, delete it now:

```sh
rm ~/.cloudflared/cert.pem
```

`cert.pem` is the login from step 1. It can create tunnels and DNS records for
the human's whole Cloudflare account, and every program running as this user,
every agent included, can read it. The running tunnel needs only
`~/.cloudflared/<UUID>.json`. If `cert.pem` was already there before step 1,
leave it (something else of the human's uses it) and tell the human what it
allows. Either way, the undo below needs a fresh `cloudflared tunnel login`
first when `cert.pem` is gone.

### 3. Its own config file — only this hostname, then 404

Write `~/.cloudflared/aiagent-phone.yml` (a separate file, so an existing
`config.yml` is never touched). Replace `<UUID>` and the home path:

```yaml
tunnel: <UUID>
credentials-file: /Users/<you>/.cloudflared/<UUID>.json   # /home/<you>/… on Linux

ingress:
  - hostname: phone.example.com
    service: http://127.0.0.1:8890
  - service: http_status:404
```

This shape is the rule, not an example:
- exactly one rule with a `hostname`, and it points at aiagent's loopback
  address;
- the **last rule is `service: http_status:404`** — anything that is not the
  chosen hostname gets 404 at the tunnel;
- never a rule without a hostname that points at a real service (that is a
  catch-all proxy), never `ssh://`, never `tcp://`, never another port.

Check it before running:

```sh
cloudflared tunnel --config ~/.cloudflared/aiagent-phone.yml ingress validate
cloudflared tunnel --config ~/.cloudflared/aiagent-phone.yml ingress rule https://phone.example.com/aiagent/
cloudflared tunnel --config ~/.cloudflared/aiagent-phone.yml ingress rule https://other.example.com/
```

The second must name `http://127.0.0.1:8890`, the third `http_status:404`.

### 4. Run it as a service

**macOS — a LaunchAgent.** Write
`~/Library/LaunchAgents/com.aiagent.cloudflared-phone.plist`, with the real
path from `command -v cloudflared` and the real home:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
  <key>Label</key><string>com.aiagent.cloudflared-phone</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/cloudflared</string>
    <string>--no-autoupdate</string>
    <string>tunnel</string>
    <string>--config</string>
    <string>/Users/you/.cloudflared/aiagent-phone.yml</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/Users/you/Library/Logs/cloudflared-phone.log</string>
  <key>StandardErrorPath</key><string>/Users/you/Library/Logs/cloudflared-phone.log</string>
</dict>
</plist>
```

```sh
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.aiagent.cloudflared-phone.plist
```

It runs while the human is logged in, like aiagent's own LaunchAgent.

**Linux — a systemd unit** running as the same user who owns
`~/.cloudflared`. Write `/etc/systemd/system/aiagent-cloudflared-phone.service`:

```ini
[Unit]
Description=Cloudflare tunnel to aiagent (phone)
After=network-online.target
Wants=network-online.target

[Service]
User=you
ExecStart=/usr/bin/cloudflared --no-autoupdate tunnel --config /home/you/.cloudflared/aiagent-phone.yml run
Restart=on-failure
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now aiagent-cloudflared-phone
```

### 5. Verify

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://phone.example.com/aiagent/
```

`200` is right. A DNS name can take a minute to appear; try again before
changing anything. Then go to [Finish](#finish).

**Undo:**

```sh
# macOS
launchctl bootout gui/$(id -u)/com.aiagent.cloudflared-phone
rm ~/Library/LaunchAgents/com.aiagent.cloudflared-phone.plist
# Linux
sudo systemctl disable --now aiagent-cloudflared-phone
sudo rm /etc/systemd/system/aiagent-cloudflared-phone.service && sudo systemctl daemon-reload
# both: the tunnel, then its file
cloudflared tunnel delete aiagent-phone
rm ~/.cloudflared/aiagent-phone.yml
```

`cloudflared` cannot remove the DNS record it made: the human deletes the
`phone` CNAME in the Cloudflare dashboard (DNS → Records). Stopping the
service alone already makes the address unreachable.

---

## C. A cloud server with a public IP

On a VM that already has a public IP and a DNS name pointing at it, no tunnel
is needed. Keep aiagent on `127.0.0.1:8890` and put a reverse proxy with
automatic certificates in front.

**What is exposed:** port 443 of the server, answering only for that name and
passing everything to aiagent's login page.

### Caddy (recommended)

Install Caddy from its official packages (caddyserver.com/docs/install), then
put this in `/etc/caddy/Caddyfile` (add it beside what is there; do not delete
other sites):

```caddy
aiagent.example.com {
	reverse_proxy 127.0.0.1:8890 {
		header_up Cf-Connecting-Ip {remote_host}
	}
}
```

The `header_up` line is not optional. Behind a proxy every request reaches
aiagent from loopback, and for loopback aiagent counts wrong-token guesses by
the `Cf-Connecting-Ip` header. Without this line a visitor could send that
header themselves and get a fresh guess counter for every try; with it, the
header is always the visitor's real address.

**A proxy that is not on this machine** (a Kubernetes ingress such as Istio,
a load balancer): aiagent cannot use loopback there. Start it with
`--trusted-proxies <the proxy's addresses, CIDR>` (or the environment variable
`AIAGENT_TRUSTED_PROXIES`), e.g. `--trusted-proxies 10.42.0.0/16`. aiagent then
counts guesses by the right-most `X-Forwarded-For` address that is not one of
those proxies; nobody else may name a visitor. Never give `0.0.0.0/0`.

```sh
sudo systemctl reload caddy
```

Caddy gets and renews the certificate by itself.

### Or aiagent's own TLS

`aiagent serve` takes `--listen ADDR:PORT --tls-cert FILE --tls-key FILE`. A
non-loopback `--listen` is refused without both TLS flags. With a real
certificate (for example from certbot), for example
`--listen 0.0.0.0:8443 --tls-cert /etc/letsencrypt/live/<name>/fullchain.pem --tls-key /etc/letsencrypt/live/<name>/privkey.pem`.
Renewal and file permissions are then yours to manage; Caddy is less to look
after.

### Firewall

Open **443/tcp only** for this (and 8443 instead if you use aiagent's own TLS
on that port). Caddy can get its certificate over 443 alone. Never open 8890:
it is plain http. Before turning a firewall on, keep the server's existing SSH
access exactly as it is — this playbook does not add, remove or change it — or
the human is locked out of their own server.

```sh
sudo ufw allow 443/tcp      # plus the provider's security group / cloud firewall
```

Verify from somewhere else, then go to [Finish](#finish):

```sh
curl -sS -o /dev/null -w '%{http_code}\n' https://aiagent.example.com/aiagent/
```

**Undo:** remove the site block and `sudo systemctl reload caddy` (or drop the
TLS flags), and `sudo ufw delete allow 443/tcp` if nothing else uses it.

---

## D. Private — Tailscale

The address reaches only devices signed in to the human's tailnet; nothing is
on the public internet. The phone needs the Tailscale app, signed in to the
same tailnet, connected whenever the AI Agent World app is used.

The app only accepts an **https** address, so a plain `http://100.x.y.z:8890`
does not work. In the Tailscale admin console the human turns on **MagicDNS**
and **HTTPS Certificates** (DNS page) — their login, their browser. Then, after
their yes:

```sh
tailscale serve --bg 8890
tailscale serve status
```

The address is `https://<machine>.<tailnet>.ts.net/aiagent/`. Never use
`tailscale funnel`: that publishes it to the whole internet.

**Undo:** `tailscale serve reset` (or `tailscale serve --https=443 off` if
other things are served).

---

## Finish

1. On the computer, open the dashboard through the **new https address**
   (`https://<host>/aiagent/`), not `127.0.0.1`, and log in with the token as
   usual. The agent does not do this step and never handles the token.
2. Press **📱 App**. The pairing QR is made from the address the page was
   opened with, so a QR shown on the `127.0.0.1` page will not work on the
   phone; this one will.
3. In the AI Agent World app press **Scan QR**. The code works once, within
   ten minutes.

For a quick tunnel (A), the address changes every restart: pair again after
each one.
