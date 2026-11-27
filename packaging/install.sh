#!/bin/sh
# packaging/install.sh — build aiagent and agcmd, install them, and print the
# two commands that turn the service on.
#
#   ./packaging/install.sh                 build + install both binaries
#   ./packaging/install.sh --with-service  also install the service unit
#                                          (systemd on Linux, LaunchAgent on macOS)
#   sudo ./packaging/install.sh --os-users also set up one OS user per room
#                                          (Linux, systemd, as root)
#   --site NAME                            name of the tmux session (default: host name)
#   --code DIR                             code the agents may read (default: none)
#   ./packaging/install.sh --no-clis       leave the agent CLIs alone
#   ./packaging/install.sh --update-clis   also update the CLIs already there
#   ./packaging/install.sh --skills DIR    copy the skills in DIR to ~/.claude/skills
#
# Claude Code, Codex and Antigravity (agy) are installed when missing; one that is
# already there, and its sign-in, is left as it is.
#
# It does NOT enable AI Agent World and does NOT create a token: turning it on
# is a deliberate act, so you run `aiagent enable` yourself and keep the token
# it prints once.
set -eu

prefix="${PREFIX:-}"
with_service=no
clis=install
os_users=no
site=""
code=""
skills=""

while [ $# -gt 0 ]; do
	arg="$1"
	shift
	case "$arg" in
	--site)
		[ $# -gt 0 ] || { echo "--site needs a name" >&2; exit 2; }
		site="$1"
		shift
		;;
	--code)
		[ $# -gt 0 ] && [ -d "$1" ] || { echo "--code needs a directory" >&2; exit 2; }
		code=$(CDPATH= cd -- "$1" && pwd)
		shift
		;;
	--os-users) os_users=yes; with_service=yes ;;
	--no-clis) clis=skip ;;
	--update-clis) clis=update ;;
	--skills)
		[ $# -gt 0 ] && [ -d "$1" ] || { echo "--skills needs a directory" >&2; exit 2; }
		skills="$1"
		shift
		;;
	--with-service | --with-systemd) with_service=yes ;;
	-h | --help)
		sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown option: $arg" >&2
		exit 2
		;;
	esac
done

if [ "$os_users" = yes ]; then
	[ "$(uname -s)" = Linux ] && [ "$(id -u)" = 0 ] || { echo "--os-users needs Linux and root" >&2; exit 2; }
	prefix=/usr/local # rooms run as other users, so nothing may live in a home
fi

# What the build and the agents need. Only what is missing is installed, with
# the system's own package manager.
need=""
for t in go tmux git python3 curl; do
	command -v "$t" >/dev/null 2>&1 || need="$need $t"
done
if [ "$(uname -s)" = Linux ]; then
	command -v bwrap >/dev/null 2>&1 || need="$need bwrap" # Codex's sandbox
	command -v xz >/dev/null 2>&1 || need="$need xz"
fi
if [ -n "$need" ]; then
	echo "installing:$need"
	s=sudo
	[ "$(id -u)" = 0 ] && s=""
	pk=""
	if command -v apt-get >/dev/null 2>&1; then
		for n in $need; do
			case "$n" in go) pk="$pk golang-go" ;; bwrap) pk="$pk bubblewrap" ;; xz) pk="$pk xz-utils" ;; *) pk="$pk $n" ;; esac
		done
		$s apt-get update -qq
		# shellcheck disable=SC2086
		DEBIAN_FRONTEND=noninteractive $s apt-get install -y -qq --no-install-recommends ca-certificates $pk
	elif command -v dnf >/dev/null 2>&1; then
		for n in $need; do
			case "$n" in go) pk="$pk golang" ;; bwrap) pk="$pk bubblewrap" ;; *) pk="$pk $n" ;; esac
		done
		# shellcheck disable=SC2086
		$s dnf install -y $pk
	elif command -v brew >/dev/null 2>&1; then
		for n in $need; do
			case "$n" in go | tmux | git) pk="$pk $n" ;; esac
		done
		# shellcheck disable=SC2086
		[ -z "$pk" ] || brew install $pk
	fi
fi
command -v go >/dev/null 2>&1 || {
	echo "Go is needed to build aiagent and could not be installed; install Go 1.26+ and run this again" >&2
	exit 1
}
command -v tmux >/dev/null 2>&1 || echo "note: tmux is not installed — agcmd needs it to reach an agent" >&2

# Default to a per-user install so nothing needs root. Fall back to the
# system prefix only if the caller asks for it.
if [ -z "$prefix" ]; then
	bindir="$HOME/.local/bin"
else
	bindir="$prefix/bin"
fi
mkdir -p "$bindir"

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "building..."
(cd "$root" && go build -trimpath -o "$tmp/aiagent" ./cmd/aiagent)
# the web page's update button pulls and reinstalls from this checkout
state_dir="${AIAGENT_STATE_DIR:-${XDG_STATE_HOME:-$HOME/.local/state}/aiagent}"
if mkdir -p "$state_dir" 2>/dev/null; then
	(cd "$root" && pwd -P) > "$state_dir/source" 2>/dev/null || true
fi
(cd "$root" && go build -trimpath -o "$tmp/agcmd" ./cmd/agcmd)

# macOS remembers what a program may open (an external disk, Documents) by its
# signature. A fresh build is signed ad hoc, so every update looked like a new
# program and asked again, holding up whatever the service was doing until
# someone pressed Allow. Signed with one certificate and one identifier, an
# Allow given once lasts across updates. Put the certificate's SHA-1 (from
# `security find-identity -v -p codesigning`) in
# ~/.config/aiagent/codesign-identity to turn this on; without it nothing changes.
sign_id_file="${XDG_CONFIG_HOME:-$HOME/.config}/aiagent/codesign-identity"
if [ "$(uname -s)" = Darwin ] && [ -s "$sign_id_file" ]; then
	sign_id=$(head -n 1 "$sign_id_file" | tr -d '[:space:]')
	for b in aiagent agcmd; do
		if codesign --force --sign "$sign_id" --identifier "world.aiagent.$b" "$tmp/$b" 2>"$tmp/sign.err"; then
			echo "signed $b with a stable identity"
		else
			echo "could not sign $b, leaving it ad hoc: $(head -n 1 "$tmp/sign.err")" >&2
		fi
	done
fi

for b in aiagent agcmd; do
	echo "installing $bindir/$b"
	if [ -w "$bindir" ]; then
		install -m 0755 "$tmp/$b" "$bindir/$b"
	else
		sudo install -m 0755 "$tmp/$b" "$bindir/$b"
	fi
done

# Shell completion. zsh looks in a directory on $fpath; bash needs the file
# sourced. Both are optional — the tools work without them.
compDir="${COMPDIR:-$HOME/.local/share/zsh/site-functions}"
case "$(basename "${SHELL:-sh}")" in
zsh)
	mkdir -p "$compDir"
	install -m 0644 "$root/packaging/completions/_agcmd" "$compDir/_agcmd"
	echo "installed zsh completion to $compDir/_agcmd"
	# $fpath only exists inside zsh, and this script is /bin/sh, so ask zsh.
	if ! zsh -c 'print -l $fpath' 2>/dev/null | grep -qx "$compDir"; then
		completionNote="$compDir"
	fi
	;;
bash)
	mkdir -p "$HOME/.local/share/bash-completion"
	install -m 0644 "$root/packaging/completions/agcmd.bash" "$HOME/.local/share/bash-completion/agcmd.bash"
	echo "installed bash completion to $HOME/.local/share/bash-completion/agcmd.bash"
	completionNote="bash"
	;;
esac

if [ "$clis" != skip ]; then
	sh "$root/packaging/install-clis.sh" "$clis" || echo "note: some agent CLIs were not installed (see above); aiagent itself is fine" >&2
fi
if [ -n "$skills" ]; then
	sh "$root/packaging/install-skills.sh" "$skills"
fi

# The site: one tmux session, and a work folder per agent. An existing one is kept.
site=$(printf %s "${site:-$(uname -n)}" | sed 's/\..*//' | tr 'A-Z' 'a-z' | tr -c 'a-z0-9_-' '-')
if [ "$(id -u)" = 0 ]; then agentsConf=/etc/aiagent/agents.conf; else agentsConf="$HOME/.config/aiagent/agents.conf"; fi
if [ -e "$agentsConf" ]; then
	site=$(sed -n 's/^SESSION=//p' "$agentsConf")
	work=$(sed -n 's/^WORK=//p' "$agentsConf")
else
	work="${code:-$HOME}/ai-work"
	mkdir -p "$(dirname "$agentsConf")"
	printf 'SESSION=%s\nCODE=%s\nWORK=%s\nHIDE=\n' "$site" "$code" "$work" > "$agentsConf"
fi
mkdir -p "$work/codex" "$work/agy"

stateDir="${XDG_STATE_HOME:-$HOME/.local/state}/aiagent"
mkdir -p "$stateDir"
if [ ! -e "$stateDir/workspaces.json" ]; then
	cat > "$stateDir/workspaces.json" <<EOF_WS
{
  "workspaces": [
    { "name": "codex", "tmuxSession": "$site", "tmuxWindow": "codex", "dir": "$work/codex", "provider": "codex" },
    { "name": "agy", "tmuxSession": "$site", "tmuxWindow": "agy", "dir": "$work/agy", "provider": "gemini" }
  ]
}
EOF_WS
	chmod 600 "$stateDir/workspaces.json"
	echo "wrote $stateDir/workspaces.json"
fi

if [ "$with_service" = yes ]; then
	case "$(uname -s)" in
	Linux)
		unitdir="${UNITDIR:-/etc/systemd/system}"
		# the unit must run the binary this script just installed
		sed "s|/usr/local/bin/aiagent|$bindir/aiagent|" "$root/packaging/aiagent@.service" > "$tmp/aiagent@.service"
		cp "$root/packaging/aiagent-tmux@.service" "$tmp/aiagent-tmux@.service"
		cp "$root"/packaging/aiagent-user@.socket "$root"/packaging/aiagent-user@.service "$root"/packaging/aiagent-user-tmux@.service "$root"/packaging/aiagent-osuser-*.service "$root"/packaging/aiagent-agents@.service "$root"/packaging/aiagent-keep@.service "$root"/packaging/aiagent-keep@.timer "$root"/packaging/aiagent-quota@.service "$root"/packaging/aiagent-quota@.timer "$tmp/"
		for u in aiagent@.service aiagent-tmux@.service aiagent-user@.socket aiagent-user@.service aiagent-user-tmux@.service aiagent-osuser-add@.service aiagent-osuser-del@.service aiagent-agents@.service aiagent-keep@.service aiagent-keep@.timer aiagent-quota@.service aiagent-quota@.timer; do
			echo "installing $unitdir/$u"
			if [ -w "$unitdir" ]; then
				install -m 0644 "$tmp/$u" "$unitdir/$u"
			else
				sudo install -m 0644 "$tmp/$u" "$unitdir/$u"
			fi
		done
		s=sudo
		[ "$(id -u)" = 0 ] && s=""
		for f in host-menu.py agent-box.sh agents-up.sh claude-keep.py ai-quota.py; do
			$s install -D -m 0755 "$root/packaging/$f" "/usr/local/lib/aiagent/$f"
		done
		dropin="$unitdir/aiagent-tmux@$(id -un).service.d"
		if [ ! -e "$dropin" ]; then
			printf '[Service]\nEnvironment=AIAGENT_TMUX=%s\n' "$site" > "$tmp/site.conf"
			$s install -D -m 0644 "$tmp/site.conf" "$dropin/site.conf"
		fi
		if [ "$os_users" = yes ]; then
			install -D -m 0755 "$root/packaging/osuser.sh" /usr/local/lib/aiagent/osuser.sh
			install -m 0755 "$root/packaging/agent-loop.sh" /usr/local/lib/aiagent/agent-loop.sh
			# what every agent knows: the admin's rules, skills and marked memories
			install -m 0755 "$root/packaging/room-share.py" /usr/local/lib/aiagent/room-share.py
			install -m 0644 "$root/packaging/aiagent-room-share.service" "$root/packaging/aiagent-room-share.timer" "$unitdir/"
			# the rooms' dev/uat environment files (docs/ENVIRONMENTS.md)
			install -m 0644 "$root/packaging/aiagent-room-env.service" "$root/packaging/aiagent-room-env.timer" "$unitdir/"
			install -m 0755 "$root/packaging/capture-box.sh" /usr/local/lib/aiagent/capture-box.sh
			install -m 0755 "$root/packaging/preview-routes.py" /usr/local/lib/aiagent/preview-routes.py
			install -m 0644 "$root/packaging/aiagent-capture@.service" "$root/packaging/aiagent-preview-routes.service" "$root/packaging/aiagent-preview-routes.timer" "$unitdir/"
			mkdir -p /usr/local/share/aiagent/room /etc/aiagent
			# off until HOST is set; the routes' timer is not enabled here
			[ -e /etc/aiagent/preview.conf ] || install -m 0644 "$root/packaging/preview.conf.example" /etc/aiagent/preview.conf
			[ -e /etc/aiagent/room-base.md ] || install -m 0644 "$root/packaging/room-base.md" /etc/aiagent/room-base.md
			[ -e /usr/local/share/aiagent/room/settings.json ] || install -m 0644 "$root/packaging/room-settings.json" /usr/local/share/aiagent/room/settings.json
			[ -e /etc/aiagent/share.conf ] || printf 'BASE=/etc/aiagent/room-base.md\nRULES=\nSKILLS=%s/.claude/skills\nMEMORY=\n' "$HOME" > /etc/aiagent/share.conf
			install -m 0755 "$root/packaging/claude-token.sh" /usr/local/sbin/aiagent-claude-token
			c=$(PATH="$HOME/.local/bin:$PATH" command -v claude || true)
			if [ -n "$c" ] && [ "$c" != /usr/local/bin/claude ]; then
				install -m 0755 "$(readlink -f "$c")" /usr/local/bin/claude
			fi
			mkdir -p "$unitdir/aiagent@root.service.d"
			printf '[Service]\nExecStart=\nExecStart=%s/aiagent serve --listen 127.0.0.1:8890 --os-users\n' "$bindir" > "$unitdir/aiagent@root.service.d/osusers.conf"
			cat <<'EOF_FIRST'

First run (as root), in this order:

  systemctl daemon-reload
  systemctl enable --now aiagent-tmux@root aiagent@root aiagent-agents@root aiagent-keep@root.timer aiagent-quota@root.timer aiagent-room-share.timer aiagent-room-env.timer
  aiagent enable              keep the admin token it prints (shown once)
  claude setup-token          sign in with YOUR Claude account, copy the token
  aiagent-claude-token set    paste it (hidden); every room uses it from now on
  then on the web, Accounts:  sign in to Claude, Codex and Google Antigravity

Run the last two in your own terminal, never through an agent or a chat: the
token would stay in the transcript. This is for rooms you use yourself; anyone
else needs their own Claude account (see "One person, several rooms" in docs/INSTALL.md).
EOF_FIRST
		else
			echo "  systemctl daemon-reload && systemctl enable --now aiagent-tmux@\$USER aiagent@\$USER aiagent-agents@\$USER aiagent-keep@\$USER.timer aiagent-quota@\$USER.timer"
		fi
		;;
	Darwin)
		plist="$HOME/Library/LaunchAgents/com.aiagent.service.plist"
		mkdir -p "$HOME/Library/LaunchAgents" "$HOME/Library/Logs"
		sed -e "s|@HOME@|$HOME|g" -e "s|@BIN@|$bindir/aiagent|g" \
			"$root/packaging/com.aiagent.service.plist.in" > "$plist"
		plutil -lint "$plist" >/dev/null
		echo "wrote $plist"
		echo "  launchctl bootstrap gui/\$(id -u) $plist"
		;;
	*)
		echo "no service unit for $(uname -s); run 'aiagent serve' yourself" >&2
		;;
	esac
fi

cat <<EOF

Installed to $bindir. Next:

  aiagent enable          turn it on; keep the token it prints (shown once)
  aiagent serve           run it on 127.0.0.1:8890

To send work to an agent, list your checkouts in
$stateDir/workspaces.json and check them with:

  agcmd ws list

Reach the service from another device over a private network (Tailscale
Serve, a Cloudflare Tunnel, an SSH tunnel, or a reverse proxy that terminates
TLS). Do not publish the port directly.
EOF

if [ -n "${completionNote:-}" ]; then
	case "$(basename "${SHELL:-sh}")" in
	zsh)
		cat <<EOF_COMP

Tab completion is installed but not yet on \$fpath. Add to ~/.zshrc, above
any compinit line:

  fpath=($completionNote \$fpath)
  autoload -Uz compinit && compinit
EOF_COMP
		;;
	bash)
		cat <<EOF_COMP

Tab completion is installed. Enable it:

  echo 'source $HOME/.local/share/bash-completion/agcmd.bash' >> ~/.bashrc
EOF_COMP
		;;
	esac
fi

case ":$PATH:" in
*":$bindir:"*) ;;
*)
	# .zprofile / .profile rather than .zshrc or .bashrc: login shells do not
	# read the interactive files, and the macOS LaunchAgent runs the service
	# through `zsh -lc`. Putting it only in .zshrc leaves the service unable
	# to find agcmd while your own terminal finds it fine.
	case "$(basename "${SHELL:-sh}")" in
	zsh) rc="$HOME/.zprofile" ;;
	*) rc="$HOME/.profile" ;;
	esac
	cat <<EOF_PATH

$bindir is not on your PATH. Add it:

  echo 'export PATH="$bindir:\$PATH"' >> $rc

then open a new terminal.
EOF_PATH
	;;
esac
