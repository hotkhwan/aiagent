#!/bin/sh
# packaging/agents-up.sh - bring the site's agents back after a start: the Codex
# and Antigravity windows (each continues its last conversation), then the
# Claude background sessions that were running before.
conf=/etc/aiagent/agents.conf
[ -r "$conf" ] || conf="$HOME/.config/aiagent/agents.conf"
SESSION=$(sed -n 's/^SESSION=//p' "$conf" 2>/dev/null)
SESSION=${SESSION:-aiagent}
lib=/usr/local/lib/aiagent
tmux has-session -t "=$SESSION" 2>/dev/null || tmux new-session -d -s "$SESSION" -n host "$lib/host-menu.py || sleep infinity"
for w in codex agy; do
	command -v "$w" >/dev/null 2>&1 || continue
	tmux list-windows -t "=$SESSION" -F '#W' | grep -qx "$w" && continue
	tmux new-window -d -t "=$SESSION:" -n "$w" "$lib/agent-box.sh $w"
	tmux set-option -w -t "=$SESSION:=$w" remain-on-exit on
done
exec "$lib/claude-keep.py" restore
