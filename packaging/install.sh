#!/bin/sh
# packaging/install.sh — build aiagent and agcmd, install them, and print the
# two commands that turn the service on.
#
#   ./packaging/install.sh                 build + install both binaries
#   ./packaging/install.sh --with-service  also install the service unit
#                                          (systemd on Linux, LaunchAgent on macOS)
#   ./packaging/install.sh --no-clis       leave the agent CLIs alone
#   ./packaging/install.sh --update-clis   also update the CLIs already there
#   ./packaging/install.sh --skills DIR    copy the skills in DIR to ~/.claude/skills
#
# Claude Code, Codex and Gemini CLI are installed when missing; one that is
# already there, and its sign-in, is left as it is.
#
# It does NOT enable AI Agent World and does NOT create a token: turning it on
# is a deliberate act, so you run `aiagent enable` yourself and keep the token
# it prints once.
set -eu

prefix="${PREFIX:-}"
with_service=no
clis=install
skills=""

while [ $# -gt 0 ]; do
	arg="$1"
	shift
	case "$arg" in
	--no-clis) clis=skip ;;
	--update-clis) clis=update ;;
	--skills)
		[ $# -gt 0 ] && [ -d "$1" ] || { echo "--skills needs a directory" >&2; exit 2; }
		skills="$1"
		shift
		;;
	--with-service | --with-systemd) with_service=yes ;;
	-h | --help)
		sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown option: $arg" >&2
		exit 2
		;;
	esac
done

command -v go >/dev/null 2>&1 || {
	echo "go is required to build aiagent" >&2
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
(cd "$root" && go build -trimpath -o "$tmp/agcmd" ./cmd/agcmd)

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

stateDir="${XDG_STATE_HOME:-$HOME/.local/state}/aiagent"
mkdir -p "$stateDir"
if [ ! -e "$stateDir/workspaces.json" ]; then
	cat > "$stateDir/workspaces.json" <<'EOF_WS'
{
  "workspaces": []
}
EOF_WS
	chmod 600 "$stateDir/workspaces.json"
	echo "wrote an empty $stateDir/workspaces.json (view-only until you fill it in)"
fi

if [ "$with_service" = yes ]; then
	case "$(uname -s)" in
	Linux)
		unitdir="${UNITDIR:-/etc/systemd/system}"
		# the unit must run the binary this script just installed
		sed "s|/usr/local/bin/aiagent|$bindir/aiagent|" "$root/packaging/aiagent@.service" > "$tmp/aiagent@.service"
		cp "$root/packaging/aiagent-tmux@.service" "$tmp/aiagent-tmux@.service"
		cp "$root"/packaging/aiagent-user@.socket "$root"/packaging/aiagent-user@.service "$root"/packaging/aiagent-user-tmux@.service "$root"/packaging/aiagent-osuser-*.service "$tmp/"
		for u in aiagent@.service aiagent-tmux@.service aiagent-user@.socket aiagent-user@.service aiagent-user-tmux@.service aiagent-osuser-add@.service aiagent-osuser-del@.service; do
			echo "installing $unitdir/$u"
			if [ -w "$unitdir" ]; then
				install -m 0644 "$tmp/$u" "$unitdir/$u"
			else
				sudo install -m 0644 "$tmp/$u" "$unitdir/$u"
			fi
		done
		echo "  systemctl daemon-reload && systemctl enable --now aiagent@\$USER"
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
