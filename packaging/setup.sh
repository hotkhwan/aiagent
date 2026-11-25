#!/bin/sh
# packaging/setup.sh - everything in one go on a fresh Linux machine, as root:
# what is missing is installed, aiagent is built and installed with rooms, the
# services are started and the admin token is printed once.
#
#   sudo ./packaging/setup.sh [--site NAME] [--code DIR] [--listen ADDR:PORT]
#                             [--agy-sandbox]
#
#   --site NAME     name of the tmux session (default: the host name)
#   --code DIR      code the Codex and Antigravity windows may read
#   --listen A:P    where the web listens (default 127.0.0.1:8890)
#   --agy-sandbox   on Ubuntu, add the AppArmor profile Antigravity needs for its
#                   command sandbox (it lets that one program make a user
#                   namespace, nothing else)
#
# Run it in your own terminal: the token it prints is shown once.
set -eu
[ "$(uname -s)" = Linux ] && [ "$(id -u)" = 0 ] || {
	echo "setup.sh is for Linux, as root; elsewhere use packaging/install.sh" >&2
	exit 2
}
command -v systemctl >/dev/null 2>&1 || { echo "systemd is needed" >&2; exit 2; }
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
listen=""
agy_sandbox=no
site=""
code=""
while [ $# -gt 0 ]; do
	case "$1" in
	--site)
		[ $# -gt 1 ] || { echo "--site needs a name" >&2; exit 2; }
		site="$2"
		shift
		;;
	--code)
		[ $# -gt 1 ] && [ -d "$2" ] || { echo "--code needs a directory" >&2; exit 2; }
		code="$2"
		shift
		;;
	--listen)
		[ $# -gt 1 ] || { echo "--listen needs ADDR:PORT" >&2; exit 2; }
		case "$2" in *[!0-9a-fA-F.:\[\]]*) echo "--listen: an address and a port" >&2; exit 2 ;; esac
		listen="$2"
		shift
		;;
	--agy-sandbox) agy_sandbox=yes ;;
	-h | --help)
		sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown option: $1" >&2
		exit 2
		;;
	esac
	shift
done
PATH="$HOME/.local/bin:/usr/local/bin:$PATH"
export PATH

set -- --os-users
[ -z "$site" ] || set -- "$@" --site "$site"
[ -z "$code" ] || set -- "$@" --code "$code"
"$root/packaging/install.sh" "$@"

if [ -n "$listen" ]; then
	printf '[Service]\nExecStart=\nExecStart=/usr/local/bin/aiagent serve --listen %s --os-users\n' "$listen" \
		> /etc/systemd/system/aiagent@root.service.d/osusers.conf
fi
if [ "$agy_sandbox" = yes ] && command -v apparmor_parser >/dev/null 2>&1 && [ -x "$HOME/.local/bin/agy" ]; then
	cat > /etc/apparmor.d/aiagent-agy <<EOF_AA
abi <abi/4.0>,
include <tunables/global>
profile aiagent-agy $HOME/.local/bin/agy flags=(unconfined) {
  userns,
}
EOF_AA
	apparmor_parser -r /etc/apparmor.d/aiagent-agy
fi

systemctl daemon-reload
systemctl enable --now aiagent-tmux@root aiagent@root aiagent-agents@root \
	aiagent-keep@root.timer aiagent-quota@root.timer aiagent-room-share.timer

echo
# `aiagent enable` on a service that is already on would replace the token.
if /usr/local/bin/aiagent status 2>/dev/null | grep -q ': ENABLED'; then
	echo "aiagent is already enabled; the admin token is unchanged."
else
	/usr/local/bin/aiagent enable
fi
cat <<EOF_DONE

Done. The web answers on ${listen:-127.0.0.1:8890}, path /aiagent. Keep it behind
a private network or a reverse proxy that terminates TLS.

Next, in this terminal:
  claude setup-token && aiagent-claude-token set     one Claude sign-in for every room
On the web, with the admin token:
  Accounts: sign in to Claude, Codex and Google Antigravity; create rooms
To see the agents:   tmux attach -t $(sed -n 's/^SESSION=//p' /etc/aiagent/agents.conf)
To remove it all:    sudo ./packaging/uninstall.sh
EOF_DONE
