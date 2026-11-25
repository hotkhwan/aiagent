#!/bin/sh
# packaging/uninstall.sh - take aiagent off this machine.
#
#   sudo ./packaging/uninstall.sh            services, programs and rooms (each
#                                            room's home is archived first)
#   sudo ./packaging/uninstall.sh --purge    also the state, the archives, the
#                                            work folders, the agent CLIs and
#                                            their sign-ins and conversations
#   --yes                                    do not ask
#
# Without --purge your sign-ins, conversations, the service's state and the
# archives in /var/backups/aiagent-users stay, so installing again continues.
# Packages a package manager installed (Go, tmux, git) are left alone.
set -eu
purge=no
yes=no
for a in "$@"; do
	case "$a" in
	--purge) purge=yes ;;
	--yes) yes=yes ;;
	-h | --help)
		sed -n '2,13p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown option: $a" >&2
		exit 2
		;;
	esac
done
me=$(id -un)
home=$HOME
state="${XDG_STATE_HOME:-$home/.local/state}/aiagent"
if [ "$yes" != yes ]; then
	echo "This removes aiagent from $(uname -n) for $me."
	if [ "$purge" = yes ]; then
		echo "--purge ALSO deletes sign-ins and conversations (~/.claude ~/.codex ~/.gemini), room archives and work folders."
	fi
	printf 'Type "remove" to go on: '
	read -r ans
	[ "$ans" = remove ] || { echo "nothing was changed"; exit 1; }
fi
s=sudo
[ "$(id -u)" = 0 ] && s=""
conf=/etc/aiagent/agents.conf
[ -r "$conf" ] || conf="$home/.config/aiagent/agents.conf"
session=""
work=""
if [ -r "$conf" ]; then
	session=$(sed -n 's/^SESSION=//p' "$conf")
	work=$(sed -n 's/^WORK=//p' "$conf")
fi

case "$(uname -s)" in
Linux)
	if command -v systemctl >/dev/null 2>&1; then
		# rooms first: osuser.sh stops each one, archives its home and removes the user
		if [ "$(id -u)" = 0 ] && getent group aiagent-users >/dev/null 2>&1; then
			for u in $(getent group aiagent-users | cut -d: -f4 | tr ',' ' '); do
				echo "removing room $u"
				if [ -x /usr/local/lib/aiagent/osuser.sh ]; then
					/usr/local/lib/aiagent/osuser.sh del "$u" || echo "  could not remove $u" >&2
				fi
			done
			groupdel aiagent-users 2>/dev/null || true
		fi
		$s systemctl disable --now "aiagent-keep@$me.timer" "aiagent-quota@$me.timer" aiagent-room-share.timer \
			"aiagent-agents@$me" "aiagent@$me" "aiagent-tmux@$me" >/dev/null 2>&1 || true
		if [ -n "$session" ]; then tmux kill-session -t "=$session" 2>/dev/null || true; fi
		for t in $(tmux list-sessions -F '#S' 2>/dev/null | grep -E '^aiagent-' || true); do
			tmux kill-session -t "=$t" 2>/dev/null || true
		done
		for f in /etc/systemd/system/aiagent*.service /etc/systemd/system/aiagent*.socket \
			/etc/systemd/system/aiagent*.timer /etc/systemd/system/aiagent*.d; do
			[ -e "$f" ] && $s rm -rf "$f"
		done
		$s systemctl daemon-reload
	fi
	if [ -e /etc/apparmor.d/aiagent-agy ]; then
		$s apparmor_parser -R /etc/apparmor.d/aiagent-agy 2>/dev/null || true
		$s rm -f /etc/apparmor.d/aiagent-agy
	fi
	$s rm -rf /usr/local/lib/aiagent /usr/local/share/aiagent /etc/aiagent \
		/run/aiagent-users /run/aiagent-tmux /run/aiagent-room /run/agent-box
	$s rm -f /usr/local/sbin/aiagent-claude-token
	# the copy of claude the rooms ran: only a plain file beside this user's own
	if [ "$(id -u)" = 0 ] && [ -f /usr/local/bin/claude ] && [ ! -L /usr/local/bin/claude ] && [ -e "$home/.local/bin/claude" ]; then
		rm -f /usr/local/bin/claude
	fi
	;;
Darwin)
	p="$home/Library/LaunchAgents/com.aiagent.service.plist"
	launchctl bootout "gui/$(id -u)" "$p" 2>/dev/null || true
	rm -f "$p"
	;;
esac

for b in aiagent agcmd; do
	for d in /usr/local/bin "$home/.local/bin"; do
		[ -e "$d/$b" ] || continue
		if [ -w "$d" ]; then rm -f "$d/$b"; else $s rm -f "$d/$b"; fi
	done
done
rm -f "$home/.local/share/zsh/site-functions/_agcmd" "$home/.local/share/bash-completion/agcmd.bash"
rm -rf "$home/.config/aiagent"

if [ "$purge" = yes ]; then
	rm -rf "$state"
	$s rm -rf /var/backups/aiagent-users
	# only the folders this installer made; anything else in there is yours
	[ -n "$work" ] || work="$home/ai-work"
	case "$work" in
	*/ai-work)
		rm -rf "$work/codex" "$work/agy"
		rmdir "$work" 2>/dev/null || true
		;;
	esac
	if command -v npm >/dev/null 2>&1; then
		npm uninstall -g --prefix "$home/.local" @openai/codex >/dev/null 2>&1 || true
	fi
	rm -f "$home/.local/bin/agy" "$home/.local/bin/claude" "$home/.local/bin/node" "$home/.local/bin/npm" "$home/.local/bin/npx"
	rm -rf "$home/.local/lib/nodejs" "$home/.local/share/claude" "$home/.claude" "$home/.codex" "$home/.gemini" "$home/.cache/antigravity"
	rm -f "$home/.claude.json" "$home"/.claude.json.backup*
	rm -rf "$home/.cache/claude" "$home/.local/state/claude"
	for d in "$home/.local/lib/node_modules/@openai" "$home/.local/lib/node_modules" "$home/.local/lib" \
		"$home/.local/share/bash-completion" "$home/.local/share/zsh/site-functions" "$home/.local/share/zsh" \
		"$home/.local/share" "$home/.local/state" "$home/.local/bin" "$home/.local"; do
		rmdir "$d" 2>/dev/null || true
	done
	echo "aiagent, the agent CLIs, their sign-ins and all its data are removed."
else
	echo "aiagent is removed. Kept: sign-ins and conversations, $state, /var/backups/aiagent-users${work:+, $work}."
	echo "Add --purge to remove those too."
fi
