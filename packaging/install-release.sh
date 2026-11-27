#!/bin/sh
# packaging/install-release.sh — install aiagent and agcmd from a published
# release, without Go or a checkout.
#
#   sh install-release.sh            the latest release
#   sh install-release.sh v0.36.0    that release
#   PREFIX=/usr/local sh install-release.sh   install to /usr/local/bin (default ~/.local/bin)
#
# It downloads this machine's archive, SHA256SUMS and SHA256SUMS.sig, checks the
# signature against the release key below (with OpenSSL 3, or with an aiagent
# already installed here), checks the archive's sha256, and only then installs.
# Anything that does not verify is refused. It installs the binaries only; the
# service unit and the agent CLIs come from packaging/install.sh (see README).
set -eu

# The aiagent release key (packaging/release-pubkey.pem, internal/world/release_pubkey.go).
pubkey='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA79kK0JDtBtjsXN7QBFJkwYZ43q8dzM70HJDD54eqpj4=
-----END PUBLIC KEY-----'

repo=hotkhwan/aiagent
tag="${1:-}"
case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) echo "no release for $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
arm64 | aarch64) arch=arm64 ;;
x86_64 | amd64) arch=amd64 ;;
*) echo "no release for $(uname -m)" >&2; exit 1 ;;
esac

get() { curl -fsSL --proto '=https' --tlsv1.2 "$@"; }

if [ -z "$tag" ]; then
	tag=$(get "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
fi
echo "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "not a release tag: '$tag'" >&2; exit 1; }
ver=${tag#v}
name="aiagent_${ver}_${os}_${arch}.tar.gz"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
base="https://github.com/$repo/releases/download/$tag"
echo "downloading aiagent $ver for $os/$arch"
for f in "$name" SHA256SUMS SHA256SUMS.sig; do
	get -o "$tmp/$f" "$base/$f"
done

# 1. the signature over SHA256SUMS
if openssl version 2>/dev/null | grep -Eq '^OpenSSL ([3-9]|[1-9][0-9])\.'; then
	printf '%s\n' "$pubkey" > "$tmp/pub.pem"
	base64 -d < "$tmp/SHA256SUMS.sig" > "$tmp/sig.bin" 2>/dev/null || base64 -D < "$tmp/SHA256SUMS.sig" > "$tmp/sig.bin"
	openssl pkeyutl -verify -pubin -inkey "$tmp/pub.pem" -rawin -in "$tmp/SHA256SUMS" -sigfile "$tmp/sig.bin" >/dev/null ||
		{ echo "SHA256SUMS is NOT signed by the aiagent release key; nothing installed" >&2; exit 1; }
	echo "signature ok (openssl)"
elif command -v aiagent >/dev/null 2>&1 && aiagent help 2>/dev/null | grep -q verify-release; then
	aiagent verify-release "$tmp" || { echo "the release does not verify; nothing installed" >&2; exit 1; }
else
	echo "cannot check the signature here: install OpenSSL 3 (brew install openssl@3 / apt install openssl)," >&2
	echo "or check it by hand (README, \"Install from a release\"). Nothing installed." >&2
	exit 1
fi

# 2. the archive is the one SHA256SUMS lists
want=$(awk -v n="$name" '$2 == n { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || { echo "SHA256SUMS does not list $name" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$name" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$name" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || { echo "$name does not match SHA256SUMS; nothing installed" >&2; exit 1; }
echo "checksum ok"

# 3. exactly the two binaries
[ "$(tar -tzf "$tmp/$name" | sort | tr '\n' ' ')" = "agcmd aiagent " ] || { echo "$name holds unexpected files" >&2; exit 1; }
mkdir "$tmp/x"
tar -xzf "$tmp/$name" -C "$tmp/x" aiagent agcmd

bindir="${PREFIX:-$HOME/.local}/bin"
mkdir -p "$bindir"
for b in aiagent agcmd; do
	if [ -w "$bindir" ]; then
		install -m 0755 "$tmp/x/$b" "$bindir/$b"
	else
		sudo install -m 0755 "$tmp/x/$b" "$bindir/$b"
	fi
	echo "installed $bindir/$b"
done
echo "aiagent $ver installed. Next: aiagent enable, then aiagent serve (or the service, see README)."
