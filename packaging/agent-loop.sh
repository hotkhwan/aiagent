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

# The admin's model; a user cannot choose one.
model() {
	local m
	m=$(head -c 60 /run/aiagent-room/model 2>/dev/null)
	[[ "$m" =~ ^[a-z0-9-]+$ ]] || m=opus
	printf %s "$m"
}
signed_in() { claude auth status >/dev/null 2>&1; }
while :; do
	if signed_in; then
		if compgen -G "$HOME/.claude/projects/*/*.jsonl" >/dev/null; then
			claude --model "$(model)" --continue || claude --model "$(model)" -n "$name"
		else
			claude --model "$(model)" -n "$name"
		fi
	else
		(until signed_in; do sleep 3; done; sleep 1; pkill -TERM -x -P $$ claude) </dev/null >/dev/null 2>&1 &
		watcher=$!
		claude --model "$(model)" -n "$name"
		kill -- -"$watcher" 2>/dev/null || kill "$watcher" 2>/dev/null
	fi
	sleep 2
done
