# bash completion for agcmd and aiagent.
#
# Source it from ~/.bashrc, or drop it in your bash-completion directory:
#   source /path/to/packaging/completions/agcmd.bash
#
# Workspace names and run ids come from the live configuration, so tab shows
# what this machine can actually reach. Both lookups are read-only and fail
# silently — a missing config or a stopped tmux server must leave you with
# plain completion, not an error printed over your prompt.

_agcmd_ws_names() {
	agcmd ws list 2>/dev/null | awk 'NR>1 && $1 != "" {print $1}'
}

_agcmd_run_ids() {
	agcmd eng status 2>/dev/null | awk 'NR>1 && $1 ~ /^ENG-/ {print $1}'
}

_agcmd_complete() {
	local cur prev words cword
	cur="${COMP_WORDS[COMP_CWORD]}"
	words=("${COMP_WORDS[@]}")
	cword=$COMP_CWORD
	COMPREPLY=()

	if ((cword == 1)); then
		COMPREPLY=($(compgen -W "ws dispatch eng restart help" -- "$cur"))
		return
	fi

	case "${words[1]}" in
	ws)
		((cword == 2)) && COMPREPLY=($(compgen -W "list" -- "$cur"))
		;;
	dispatch | restart)
		((cword == 2)) && COMPREPLY=($(compgen -W "$(_agcmd_ws_names)" -- "$cur"))
		;;
	eng)
		case $cword in
		2)
			# This position is shared with `agcmd eng <task> <workspace>`, so the
			# subcommands are a suggestion, not the only valid input.
			COMPREPLY=($(compgen -W "status answer approve abort archive note" -- "$cur"))
			;;
		3)
			case "${words[2]}" in
			status | answer | approve | abort | archive | note)
				COMPREPLY=($(compgen -W "$(_agcmd_run_ids)" -- "$cur"))
				;;
			*)
				COMPREPLY=($(compgen -W "$(_agcmd_ws_names)" -- "$cur"))
				;;
			esac
			;;
		esac
		;;
	esac
}
complete -F _agcmd_complete agcmd

_aiagent_complete() {
	local cur words cword
	cur="${COMP_WORDS[COMP_CWORD]}"
	words=("${COMP_WORDS[@]}")
	cword=$COMP_CWORD
	COMPREPLY=()

	if ((cword == 1)); then
		COMPREPLY=($(compgen -W "serve enable disable rotate-token status statusline review help" -- "$cur"))
		return
	fi

	case "${words[1]}" in
	serve)
		COMPREPLY=($(compgen -W "--listen --state-dir --homes --workspaces --tls-cert --tls-key" -- "$cur"))
		;;
	enable | disable | rotate-token | status)
		COMPREPLY=($(compgen -W "--state-dir" -- "$cur"))
		;;
	review)
		case "$cur" in
		-*) COMPREPLY=($(compgen -W "--title --summary --session" -- "$cur")) ;;
		*) COMPREPLY=($(compgen -f -X '!*.html' -- "$cur")) ;;
		esac
		;;
	esac
}
complete -F _aiagent_complete aiagent
