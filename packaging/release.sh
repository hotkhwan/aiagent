#!/bin/sh
# packaging/release.sh — cut an aiagent release (the owner, on the release Mac).
#
#   ./packaging/release.sh 0.36.0
#
# Refuses unless the tree is clean, on main, and 0.36.0 is the Version in
# internal/world/version.go. Builds aiagent and agcmd for darwin/arm64,
# darwin/amd64, linux/amd64 and linux/arm64 into
# dist/aiagent_<ver>_<os>_<arch>.tar.gz, writes SHA256SUMS over the archives and
# SHA256SUMS.sig (ed25519, base64) with the release key, checks every archive
# the way an installed aiagent will, then tags v<ver>, pushes the tag and
# publishes the GitHub release with the commits since the previous tag as notes.
#
# The private key: $AIAGENT_RELEASE_KEY, default
# ~/littledays-keys/aiagent-release-ed25519.pem. Its public half is compiled into
# aiagent (internal/world/release_pubkey.go); a release signed with any other key
# is refused by every installed machine, and by this script before publishing.
set -eu

ver="${1:-}"
case "$ver" in
v*) ver="${ver#v}" ;;
esac
echo "$ver" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || { echo "usage: $0 MAJOR.MINOR.PATCH" >&2; exit 2; }

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
key="${AIAGENT_RELEASE_KEY:-$HOME/littledays-keys/aiagent-release-ed25519.pem}"
tag="v$ver"

[ "$(git -C "$root" rev-parse --abbrev-ref HEAD)" = main ] || { echo "not on main" >&2; exit 1; }
[ -z "$(git -C "$root" status --porcelain)" ] || { echo "the tree is not clean" >&2; exit 1; }
code_ver=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$root/internal/world/version.go")
[ "$code_ver" = "$ver" ] || { echo "internal/world/version.go says $code_ver, not $ver" >&2; exit 1; }
if git -C "$root" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	echo "tag $tag already exists" >&2
	exit 1
fi
command -v gh >/dev/null 2>&1 || { echo "gh (GitHub CLI) is needed" >&2; exit 1; }

if [ ! -e "$key" ]; then
	mkdir -p "$(dirname "$key")"
	go -C "$root" run ./tools/releasesign keygen -key "$key"
	chmod 600 "$key"
	echo "made a NEW release key at $key. Its public half:" >&2
	go -C "$root" run ./tools/releasesign pubkey -key "$key" >&2
	echo "put it in internal/world/release_pubkey.go (and packaging/release-pubkey.pem), release that by building, then run this again" >&2
	exit 1
fi
[ "$(stat -f %Lp "$key" 2>/dev/null || stat -c %a "$key")" = 600 ] || { echo "$key must be mode 600" >&2; exit 1; }

dist="$root/dist"
rm -rf "$dist"
mkdir -p "$dist"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for p in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64; do
	os=${p%/*}
	arch=${p#*/}
	d="$stage/$os-$arch"
	mkdir -p "$d"
	echo "building $os/$arch"
	for b in aiagent agcmd; do
		CGO_ENABLED=0 GOOS=$os GOARCH=$arch go -C "$root" build -trimpath -ldflags "-s -w" -o "$d/$b" "./cmd/$b"
	done
	# exactly two plain files: no AppleDouble ._ entries, no extended attributes
	COPYFILE_DISABLE=1 tar --format ustar --no-xattrs --no-mac-metadata -czf "$dist/aiagent_${ver}_${os}_${arch}.tar.gz" -C "$d" aiagent agcmd
done

(cd "$dist" && shasum -a 256 aiagent_*.tar.gz > SHA256SUMS)
go -C "$root" run ./tools/releasesign sign -key "$key" -in "$dist/SHA256SUMS" -out "$dist/SHA256SUMS.sig"
go -C "$root" run ./tools/releasesign check -in "$dist"
cat "$dist/SHA256SUMS"

prev=$(git -C "$root" describe --tags --abbrev=0 2>/dev/null || true)
notes="$stage/notes.md"
if [ -n "$prev" ]; then
	git -C "$root" log --no-merges --format='- %s' "$prev..HEAD" > "$notes"
else
	git -C "$root" log --no-merges --format='- %s' -n 30 > "$notes"
fi
cat >> "$notes" <<'EOF_NOTES'

Installed machines update from the 🔔 on the page; it verifies the signature itself.
To check a download by hand, see "Install from a release" in the README.
EOF_NOTES

git -C "$root" tag -a "$tag" -m "aiagent $ver"
git -C "$root" push origin "$tag"
gh release create "$tag" --repo hotkhwan/aiagent --title "aiagent $ver" --notes-file "$notes" "$dist"/aiagent_*.tar.gz "$dist/SHA256SUMS" "$dist/SHA256SUMS.sig"
echo "released $tag"
