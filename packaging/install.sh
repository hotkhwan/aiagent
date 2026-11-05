#!/bin/sh
# packaging/install.sh — build aiagent and install it to /usr/local/bin.
#
#   ./packaging/install.sh                 build + install the binary
#   ./packaging/install.sh --with-systemd  also install the template unit
#
# This script does NOT enable the service and does NOT create a token: turning
# AI Agent World on is a deliberate act, so you run `aiagent enable` yourself
# and keep the token it prints once.
set -eu

prefix="${PREFIX:-/usr/local}"
bindir="$prefix/bin"
unitdir="${UNITDIR:-/etc/systemd/system}"
with_systemd=no

for arg in "$@"; do
	case "$arg" in
	--with-systemd) with_systemd=yes ;;
	-h | --help)
		sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "unknown option: $arg" >&2
		exit 2
		;;
	esac
done

command -v go >/dev/null 2>&1 || {
	echo "go is required to build aiagent" >&2
	exit 1
}

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "building aiagent..."
(cd "$root" && go build -trimpath -o "$tmp/aiagent" ./cmd/aiagent)

# A plain `install` keeps ownership and mode explicit rather than inheriting
# whatever umask the caller happened to have.
echo "installing to $bindir/aiagent"
if [ -w "$bindir" ]; then
	install -m 0755 "$tmp/aiagent" "$bindir/aiagent"
else
	sudo install -m 0755 "$tmp/aiagent" "$bindir/aiagent"
fi

if [ "$with_systemd" = yes ]; then
	echo "installing $unitdir/aiagent@.service"
	if [ -w "$unitdir" ]; then
		install -m 0644 "$root/packaging/aiagent@.service" "$unitdir/aiagent@.service"
	else
		sudo install -m 0644 "$root/packaging/aiagent@.service" "$unitdir/aiagent@.service"
	fi
	echo "  systemctl daemon-reload && systemctl enable --now aiagent@\$USER"
fi

cat <<'EOF'

Installed. Next:

  aiagent enable          turn it on; keep the token it prints (shown once)
  aiagent serve           run it on 127.0.0.1:8890

Reach it from another device over a private network (Tailscale Serve, an SSH
tunnel, or a reverse proxy that terminates TLS). Do not publish the port.
EOF
