#!/bin/sh
# packaging/install-skills.sh DIR — copy each skill (a folder holding SKILL.md)
# from DIR to ~/.claude/skills. Skills only: no memory, settings, credentials or
# transcripts, so one project's context never reaches another machine. A skill
# already there is kept. Only DIR's direct children are considered, which leaves
# out skills/synced, the ones Claude Code syncs per account.
set -eu
src="${1:?usage: install-skills.sh DIR}"
dst="$HOME/.claude/skills"
mkdir -p "$dst"
for d in "$src"/*/; do
	[ -f "$d/SKILL.md" ] || continue
	n=$(basename "$d")
	if [ -e "$dst/$n" ]; then
		echo "skill $n: already there, kept"
	else
		cp -R "$d" "$dst/$n"
		echo "skill $n: installed"
	fi
done
