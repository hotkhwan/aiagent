#!/bin/bash
# packaging/capture-box.sh ID - take the screenshots of one preview capture.
# Started by root through aiagent-capture@ID.service. The browser renders what
# a room wrote, so it runs boxed: an unprivileged id of its own, no home, no
# configuration, a network of its own with nothing in it, and one unix socket
# on which the service answers that one room's content. What comes out is two
# files, copied with a size limit and checked by the service.
set -eu
id=${1:?}
[[ "$id" =~ ^[0-9a-f]{32}$ ]] || { echo "bad id"; exit 2; }
job=${AIAGENT_CAPTURE_DIR:-/run/aiagent-capture}/$id
conf=${AIAGENT_PREVIEW_CONF:-/etc/aiagent/preview.conf}
aiagent=${AIAGENT_BIN:-/usr/local/bin/aiagent}
# Outside the rooms' id range and different from agent-box's ids.
uid=800003
max=1048576
if [ -z "${BOXED:-}" ]; then
	[ "$(id -u)" = 0 ] || { echo "needs root"; exit 2; }
	[ -d "$job" ] && [ -S "$job/s" ] || { echo "no such capture"; exit 2; }
	BOXED=1 exec unshare --mount --net --ipc --uts --pid --fork --kill-child --propagation private "$0" "$id"
fi
chromium=$(sed -n 's/^CHROMIUM=//p' "$conf" | head -n 1)
[ -x "$chromium" ] || { echo "no browser configured"; exit 3; }
chromium=$(readlink -f "$chromium")
case "$chromium" in /home/* | /root/*) echo "the browser must not be in a home"; exit 3 ;; esac
read -r path < "$job/path" || [ -n "$path" ]
ip link set lo up
b=/run/aiagent-capture-box
[ -d "$b" ] || mkdir -p "$b"
mount -t tmpfs -o mode=755,size=256m,nosuid,nodev none "$b" # seen by this box only
mkdir -p "$b"/{usr,etc,proc,dev/shm,tmp,run,out,job,chromium}
chmod 1777 "$b/tmp" "$b/dev/shm"
for l in bin sbin lib lib64; do if [ -L "/$l" ]; then cp -P "/$l" "$b/"; fi; done
ro() { if [ -d "$1" ]; then mkdir -p "$2"; else : > "$2"; fi; mount --bind "$1" "$2"; mount -o remount,ro,bind,nosuid,nodev "$2"; }
ro /usr "$b/usr"
# what this machine keeps in /usr/local is not the browser's business
mount -t tmpfs -o mode=755,size=1m,nosuid none "$b/usr/local"
ro "$aiagent" "$b/run/aiagent"
case "$chromium" in
/usr/local/*) own=yes ;;
/usr/*) own=no ;;
*) own=yes ;;
esac
if [ $own = yes ]; then ro "$(dirname "$chromium")" "$b/chromium"; chromium=/chromium/$(basename "$chromium"); fi
for f in fonts ld.so.cache alternatives localtime; do if [ -e "/etc/$f" ]; then ro "/etc/$f" "$b/etc/$f"; fi; done
printf 'capture:x:%s:%s::/tmp:/bin/false\n' $uid $uid > "$b/etc/passwd"
for d in null zero full random urandom; do : > "$b/dev/$d"; mount --bind "/dev/$d" "$b/dev/$d"; done
mount -t proc -o nosuid,nodev,noexec none "$b/proc"
mount -t tmpfs -o "mode=700,uid=$uid,gid=$uid,size=4m,nosuid,nodev,noexec" none "$b/out"
: > "$b/run/s"; mount --bind "$job/s" "$b/run/s"; chown $uid:$uid "$b/run/s"; chmod 0600 "$b/run/s"
mount --bind "$job" "$b/job" # root only: where the files are copied to
chmod 0755 "$b"
mkdir "$b/old"; cd "$b"; pivot_root . old; umount -l /old; rmdir /old
rc=0
env -i HOME=/tmp TMPDIR=/tmp LANG=C.UTF-8 PATH=/usr/bin:/bin \
	/usr/bin/timeout -s KILL 55 /usr/bin/prlimit --core=0 --nofile=4096 --nproc=256 --fsize=67108864 \
	/usr/bin/setpriv --reuid $uid --regid $uid --clear-groups --bounding-set=-all --inh-caps=-all --ambient-caps=-all --no-new-privs \
	/run/aiagent capture-box "$chromium" /run/s "$path" /out > /tmp/log 2>&1 || rc=$?
kill -KILL -1 2>/dev/null || true # every process of the box but this one
if [ $rc -ne 0 ]; then tail -c 400 /tmp/log | tr -cd '[:print:]\n' | tail -n 2; exit 4; fi
for w in 390 1440; do
	f=/out/$w.jpg
	[ -f "$f" ] && [ ! -L "$f" ] && [ "$(stat -c %s "$f")" -le $max ] || { echo "no picture"; exit 5; }
	(umask 077; head -c $max "$f" > "/job/$w.jpg")
done
