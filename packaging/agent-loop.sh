#!/bin/bash
# packaging/agent-loop.sh NAME — keep one Claude session open in this tmux
# window. It starts signed out too, so the user sees their session at once; when
# a sign-in lands (made from the web) it is restarted to pick it up. Later runs
# continue the same conversation, so a name the user chose is kept.
# Job control (-m) puts claude in the foreground group, which is how the
# service knows the pane runs an agent.
set -m
trap "exit 0" TERM HUP
name="${1:?}"
# What every room knows: skills and a CLAUDE.md the admin keeps in one
# root-owned place. Links, so an update there reaches every room; a file the
# user made under the same name is left alone.
room=/usr/local/share/aiagent/room
mkdir -p "$HOME/.claude/skills"
for d in "$room"/skills/*/; do
	[ -f "$d/SKILL.md" ] || continue
	t="$HOME/.claude/skills/$(basename "$d")"
	[ -e "$t" ] && [ ! -L "$t" ] || ln -sfn "${d%/}" "$t"
done
if [ -f "$room/CLAUDE.md" ] && { [ ! -e "$HOME/.claude/CLAUDE.md" ] || [ -L "$HOME/.claude/CLAUDE.md" ]; }; then
	ln -sfn "$room/CLAUDE.md" "$HOME/.claude/CLAUDE.md"
fi

# Commands the agent runs do not get the sign-in token in their environment,
# and it is told not to read where secrets live. The account's own files and
# the firewall are what really bound it; this stops an agent being talked into it.
export CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1
set --
[ -f "$room/settings.json" ] && set -- --settings "$room/settings.json"

# Nobody sits at a room's terminal to answer a permission prompt, so the CLI
# decides itself (auto mode); what the room may touch is bounded by its OS
# user, the settings above and the firewall.
set -- "$@" --permission-mode auto

# The admin's model; a user cannot choose one.
model() {
	local m
	m=$(head -c 60 /run/aiagent-room/model 2>/dev/null)
	[[ "$m" =~ ^[a-z0-9-]+$ ]] || m=opus
	printf %s "$m"
}
signed_in() { claude auth status >/dev/null 2>&1; }

# settle: once the CLI is up, put it where a room must be. The CLI does not
# always honour --permission-mode when it continues a conversation, and a
# conversation it moved to a background job comes back under a derived name.
# Both are fixed the way a person would: Shift+Tab until it says auto mode,
# and /rename. Only at start, only at the idle prompt.
settle() {
	[ -n "${TMUX_PANE:-}" ] || return 0
	local i screen pid
	for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
		sleep 3
		screen=$(tmux capture-pane -p -t "$TMUX_PANE" 2>/dev/null) || return 0
		case "$screen" in
		*"auto mode on"*) break ;;
		*"Esc to cancel"* | *"Enter to confirm"* | *"esc to interrupt"*) ;; # a question or a turn: leave it
		*"manual mode on"* | *"accept edits on"* | *"plan mode on"*) tmux send-keys -t "$TMUX_PANE" BTab ;;
		esac
	done
	pid=$(pgrep -P "$1" -x claude | head -1)
	[ -n "$pid" ] && [ -f "$HOME/.claude/sessions/$pid.json" ] || return 0
	grep -q "\"name\": *\"$name\"" "$HOME/.claude/sessions/$pid.json" && return 0
	case "$(tmux capture-pane -p -t "$TMUX_PANE" 2>/dev/null)" in
	*"esc to interrupt"* | *"Esc to cancel"*) return 0 ;;
	esac
	# A message may be in the box, pasted but not submitted yet: typing after it
	# would send the two as one.
	[ -z "$(boxtext)" ] || return 0
	tmux send-keys -t "$TMUX_PANE" -l -- "/rename $name"
	sleep 0.5
	[ "$(boxtext)" = "/rename$name" ] && tmux send-keys -t "$TMUX_PANE" Enter
}

# boxtext: what the input box holds, without spaces and without the dim
# suggestion an empty box shows.
boxtext() {
	local esc=$'\033'
	tmux capture-pane -e -p -t "$TMUX_PANE" 2>/dev/null |
		sed -e "s/$esc\[2m\($esc\[[0-9;]*m\)*[^$esc]*//g" -e "s/$esc\[[0-9;]*m//g" |
		sed -n 's/^❯//p' | tail -n 1 | sed 's/\xc2\xa0//g; s/[[:space:]]//g'
}
while :; do
	if signed_in; then
		settle $$ </dev/null >/dev/null 2>&1 &
		if compgen -G "$HOME/.claude/projects/*/*.jsonl" >/dev/null; then
			claude "$@" --model "$(model)" --continue || claude "$@" --model "$(model)" -n "$name"
		else
			claude "$@" --model "$(model)" -n "$name"
		fi
	else
		(until signed_in; do sleep 3; done; sleep 1; pkill -TERM -x -P $$ claude) </dev/null >/dev/null 2>&1 &
		watcher=$!
		claude "$@" --model "$(model)" -n "$name"
		kill -- -"$watcher" 2>/dev/null || kill "$watcher" 2>/dev/null
	fi
	sleep 2
done
