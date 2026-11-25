#!/bin/sh
# packaging/install-clis.sh [install|update] — put Claude Code, Codex and Antigravity
# (agy) on this machine, for this user. A CLI already present is skipped
# ("install") or updated ("update"). It never signs anything in or out: an
# existing sign-in is used as it is, a new one is made on the web (Accounts).
set -u

mode="${1:-install}"
bindir="$HOME/.local/bin"
PATH="$bindir:$PATH"
export PATH
failed=0

ensure_npm() {
	command -v npm >/dev/null 2>&1 && return 0
	if [ "$(id -u)" = 0 ]; then s=""; else s="sudo"; fi
	if command -v apt-get >/dev/null 2>&1; then
		$s apt-get update && $s apt-get install -y nodejs npm
	elif command -v dnf >/dev/null 2>&1; then
		$s dnf install -y nodejs npm
	elif command -v brew >/dev/null 2>&1; then
		brew install node
	fi
	command -v npm >/dev/null 2>&1
}

install_claude() {
	f=$(mktemp) || return 1
	curl -fsSL https://claude.ai/install.sh -o "$f" && bash "$f"
	rc=$?
	rm -f "$f"
	return $rc
}

# Only the binary, checked against the vendor's manifest; its own installer also
# edits shell profiles.
install_agy() {
	command -v python3 >/dev/null 2>&1 || { echo "python3 is needed to install agy" >&2; return 1; }
	mkdir -p "$bindir" && python3 - "$bindir/agy" <<'EOF_AGY'
import hashlib, io, json, os, platform, sys, tarfile, urllib.request
plat = {"Linux": "linux", "Darwin": "darwin"}[platform.system()]
arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[platform.machine()]
base = "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/"
m = json.load(urllib.request.urlopen(base + plat + "_" + arch + ".json", timeout=30))
b = urllib.request.urlopen(m["url"], timeout=600).read()
if hashlib.sha512(b).hexdigest() != m["sha512"]:
    sys.exit("agy: checksum does not match")
if ".tar.gz" in m["url"]:
    b = tarfile.open(fileobj=io.BytesIO(b)).extractfile("antigravity").read()
with open(sys.argv[1] + ".new", "wb") as f:
    f.write(b)
os.chmod(sys.argv[1] + ".new", 0o755)
os.replace(sys.argv[1] + ".new", sys.argv[1])
EOF_AGY
}

# npm_cli PACKAGE: per-user prefix, so nothing needs root
npm_cli() {
	ensure_npm || { echo "npm is not available; install Node.js 20+ first" >&2; return 1; }
	npm install -g --prefix "$HOME/.local" "$1@latest"
}

# update_npm_cli NAME PACKAGE: stay in the prefix the copy on PATH came from
update_npm_cli() {
	real=$(readlink -f "$(command -v "$1")")
	case "$real" in
	*/lib/node_modules/*) npm install -g --prefix "${real%%/lib/node_modules/*}" "$2@latest" ;;
	*) echo "$1 was not installed with npm; update it the way it was installed" >&2; return 1 ;;
	esac
}

for cli in claude codex agy; do
	case "$cli" in
	codex) pkg=@openai/codex ;;
	*) pkg="" ;;
	esac
	if command -v "$cli" >/dev/null 2>&1; then
		if [ "$mode" != update ]; then
			echo "$cli: already installed, skipped"
			continue
		fi
		echo "$cli: updating"
		if [ "$cli" = claude ] || [ "$cli" = agy ]; then "$cli" update; else update_npm_cli "$cli" "$pkg"; fi
	else
		echo "$cli: installing"
		if [ "$cli" = claude ]; then install_claude; elif [ "$cli" = agy ]; then install_agy; else npm_cli "$pkg"; fi
	fi || { echo "$cli: FAILED" >&2; failed=1; }
done
exit $failed
