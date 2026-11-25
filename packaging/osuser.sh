#!/bin/bash
# packaging/osuser.sh add|del NAME — the OS user behind one user token.
# Only ever touches users in the group aiagent-users, which only "add" fills.
set -euo pipefail
act="${1:?}"; name="${2:?}"
[[ "$name" =~ ^[a-z][a-z0-9-]{1,30}$ ]] || { echo "bad user name"; exit 2; }
group=aiagent-users
tokens="${AIAGENT_STATE:-/root/.local/state/aiagent}/instances"
archive=/var/backups/aiagent-users
units=("aiagent-user@$name.service" "aiagent-user@$name.socket" "aiagent-user-tmux@$name.service")

managed() { id "$name" >/dev/null 2>&1 && id -nG "$name" | tr ' ' '\n' | grep -qx "$group"; }

remove() {
	systemctl disable --now "${units[@]}" >/dev/null 2>&1 || true
	if managed; then
		loginctl terminate-user "$name" >/dev/null 2>&1 || true
		pkill -KILL -u "$name" 2>/dev/null || true
		for _ in 1 2 3 4 5 6 7 8 9 10; do pgrep -u "$name" >/dev/null || break; sleep 0.5; done
		if [ "${1:-}" = archive ] && [ -d "/home/$name" ]; then
			mkdir -p -m 0700 "$archive"
			out="$archive/$name-$(date -u +%Y%m%d-%H%M%S).tar.gz"
			(umask 077; tar --one-file-system -C /home -czf "$out" "$name")
			echo "archived to $out"
		fi
		crontab -r -u "$name" 2>/dev/null || true
		userdel -r "$name" 2>/dev/null || userdel "$name"
	fi
	rm -rf --one-file-system "/run/aiagent-tmux/$name"
	rm -f "$tokens/$name.token"
}

case "$act" in
add)
	if id "$name" >/dev/null 2>&1 || [ -e "/home/$name" ] || getent group "$name" >/dev/null; then
		echo "the name $name is already in use on this machine"; exit 3
	fi
	getent group "$group" >/dev/null || groupadd --system "$group"
	trap 'remove' ERR
	useradd -m -s /bin/bash -K HOME_MODE=0700 -G "$group" "$name"
	passwd -l "$name" >/dev/null
	run() { runuser -u "$name" -- env -i HOME="/home/$name" USER="$name" PATH=/usr/local/bin:/usr/bin:/bin "$@"; }
	run mkdir -p "/home/$name/work" "/home/$name/.local/state/aiagent" "/home/$name/.claude" "/home/$name/.codex"
	run sh -c 'umask 077; cat > ~/.claude.json' <<EOF_J
{"hasCompletedOnboarding":true,"lastOnboardingVersion":"2.1.331","fullscreenUpsellSeenCount":3,"hasSeenAutoDefaultNotice":true,"projects":{"/home/$name/work":{"hasTrustDialogAccepted":true}}}
EOF_J
	run sh -c 'umask 077; cat > ~/.local/state/aiagent/workspaces.json' <<EOF_W
{"workspaces":[{"name":"$name","tmuxSession":"core","tmuxWindow":"$name","dir":"/home/$name/work","provider":"claude"}]}
EOF_W
	mkdir -p -m 0700 "$tokens"
	tok=$(run aiagent enable | sed -n 's/^ *token *\(kaw_[A-Za-z0-9_-]*\).*/\1/p')
	[ -n "$tok" ]
	(umask 077; printf '%s\n' "$tok" > "$tokens/$name.token")
	systemctl enable --now "aiagent-user-tmux@$name.service" "aiagent-user@$name.socket" >/dev/null 2>&1
	systemctl enable "aiagent-user@$name.service" >/dev/null 2>&1
	;;
del) remove archive ;;
*) echo "usage: osuser.sh add|del NAME"; exit 2 ;;
esac
