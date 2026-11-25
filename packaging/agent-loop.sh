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
signed_in() { claude auth status >/dev/null 2>&1; }
while :; do
	if signed_in; then
		if compgen -G "$HOME/.claude/projects/*/*.jsonl" >/dev/null; then
			claude --continue || claude -n "$name"
		else
			claude -n "$name"
		fi
	else
		(until signed_in; do sleep 3; done; sleep 1; pkill -TERM -x -P $$ claude) </dev/null >/dev/null 2>&1 &
		watcher=$!
		claude -n "$name"
		kill -- -"$watcher" 2>/dev/null || kill "$watcher" 2>/dev/null
	fi
	sleep 2
done
