#!/bin/bash
# packaging/agent-box.sh codex|agy [login] - run an agent CLI in its own work
# folder, continuing its last conversation. Started by root it is boxed first:
# an unprivileged id that sees only the system, the code (read-only), its work
# folder and its own sign-in. Any other user runs it as themselves.
set -eu
conf=/etc/aiagent/agents.conf
[ "${BOXED:-}" = run ] && conf=/run/aiagent/conf
[ -r "$conf" ] || conf="$HOME/.config/aiagent/agents.conf"
CODE="" WORK="$HOME/ai-work" HIDE=""
if [ -r "$conf" ]; then
	while IFS='=' read -r k v; do
		case "$k" in CODE) CODE=$v ;; WORK) WORK=$v ;; HIDE) HIDE=$v ;; esac
	done < "$conf"
fi
cli=${1:?}
mode=${2:-}
# Commands run in agy's own sandbox, which has no network, without asking; one
# that needs to leave it asks at the terminal, where only the admin answers.
agyset='{"toolPermission":"proceed-in-sandbox"}'
run() {
	case "$cli:$mode" in
	codex:*)
		set -- --sandbox workspace-write --ask-for-approval never
		# exec: the window must show the agent itself, not a shell
		if [ -n "$(find "$HOME/.codex/sessions" -name '*.jsonl' -print -quit 2>/dev/null)" ]; then exec codex resume --last --no-daemon "$@"; fi
		exec codex --no-daemon "$@" ;;
	agy:usage) exec agy -p /usage --output-format json ;;
	agy:login) exec agy --sandbox --settings "$agyset" ;;
	agy:*)
		if ls "$HOME"/.gemini/antigravity-cli/conversations/* >/dev/null 2>&1; then exec agy --sandbox --settings "$agyset" --continue; fi
		exec agy --sandbox --settings "$agyset" ;;
	esac
}
case "$cli" in codex) own=.codex ;; agy) own=.gemini ;; *) exit 2 ;; esac
work=$WORK/$cli
if [ "${BOXED:-}" = run ]; then cd "$work"; run; exit; fi
mkdir -p "$work" "$HOME/$own"
[ -d "$work/.git" ] || git -C "$work" init -q
if [ "$(id -u)" != 0 ]; then cd "$work"; PATH="$HOME/.local/bin:$PATH" run; exit; fi
# Outside the rooms' id range, whose firewall is for rooms. Files stay root's on
# disk and are shown to this id by an idmapped mount.
id=800001
[ "$cli" = agy ] && id=800002
if [ -z "${BOXED:-}" ]; then
	BOXED=1 exec unshare --mount --ipc --propagation private "$0" "$@"
fi
b=/run/agent-box
mkdir -p $b; mount -t tmpfs -o mode=755 none $b
mkdir -p $b/{usr,etc,root/.local,proc,dev,tmp,run,var/tmp}
chmod 1777 $b/tmp $b/var/tmp
for l in bin sbin lib lib64; do [ -L /$l ] && cp -P /$l $b/ || true; done
ro() { [ -e "$1" ] || return 0; if [ -d "$1" ]; then mkdir -p "$b$1"; else mkdir -p "$(dirname "$b$1")"; : > "$b$1"; fi; mount --rbind "$1" "$b$1"; mount -o remount,ro,bind,nosuid "$b$1"; }
ro /usr;  ro /root/.local/bin; ro /root/.local/lib
for f in ssl alternatives ca-certificates passwd group hosts nsswitch.conf localtime ld.so.cache gitconfig; do ro /etc/$f; done
cp -L /etc/resolv.conf $b/etc/resolv.conf
if [ -n "$CODE" ]; then ro "$CODE"; fi
for h in $HIDE; do [ -d "$b$h" ] && mount -t tmpfs -o mode=755,ro none "$b$h" || true; done
mkdir -p "$b$work"; mount --bind -o X-mount.idmap="u:0:$id:1 g:0:$id:1" "$work" "$b$work"
mkdir -p "$b/root/$own"; mount --bind -o X-mount.idmap="u:0:$id:1 g:0:$id:1" "/root/$own" "$b/root/$own"
mount --rbind /dev $b/dev; mount --make-rslave $b/dev
mount -t proc -o hidepid=2 none $b/proc 2>/dev/null || mount --rbind /proc $b/proc
chown $id:$id $b/root
mkdir -p "$b/run/aiagent"; install -m 0755 "$0" $b/run/aiagent/box; printf "CODE=%s\nWORK=%s\n" "$CODE" "$WORK" > $b/run/aiagent/conf
# pivot_root, not chroot: the CLI's own sandbox needs a user namespace
mkdir -p $b/old; cd $b; pivot_root . old; umount -l /old; rmdir /old;
exec env -i HOME=/root USER=root BOXED=run TERM="${TERM:-screen-256color}" LANG=C.UTF-8 PATH=/root/.local/bin:/usr/local/bin:/usr/bin:/bin \
	/usr/bin/setpriv --reuid $id --regid $id --clear-groups --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs /run/aiagent/box "$cli" "$mode"
