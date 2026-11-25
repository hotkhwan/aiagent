#!/bin/bash
# packaging/claude-token.sh — let every room on this machine use one Claude
# sign-in: the operator's own, made with `claude setup-token`. Only for rooms
# the operator uses themselves; other people sign in with their own account.
# The token is typed hidden, kept in a root-only file and handed to the rooms
# by systemd. A room's agent can read it.
set -euo pipefail
env=/etc/aiagent/claude.env
rooms() { systemctl list-units --plain --no-legend --state=active 'aiagent-user-tmux@*' 'aiagent-user@*.service' | awk '{print $1}'; }
case "${1:-set}" in
set)
	read -rsp 'Paste the token from `claude setup-token` (hidden): ' t; echo
	t="${t//[[:space:]]/}"
	[[ "$t" =~ ^sk-ant-oat[A-Za-z0-9_-]{20,}$ ]] || { echo "that is not a setup-token token; nothing changed" >&2; exit 1; }
	mkdir -p -m 0700 /etc/aiagent
	(umask 077; printf 'CLAUDE_CODE_OAUTH_TOKEN=%s\n' "$t" > "$env.new"); mv "$env.new" "$env"
	;;
remove) rm -f "$env" ;;
status) [ -s "$env" ] && echo "set" || echo "not set"; exit ;;
*) echo "usage: claude-token.sh [set|remove|status]" >&2; exit 2 ;;
esac
r=$(rooms); [ -z "$r" ] || systemctl restart $r
echo "done; rooms restarted: ${r:-none}"
