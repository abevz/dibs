#!/bin/sh
set -eu

scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
bundle="$scratch/release"
mkdir -p "$bundle" "$scratch/archive" "$scratch/home/.local/share/dibs"

for binary in dibs dibsd dibs-mcp; do
	printf '#!/bin/sh\necho test-binary\n' > "$scratch/archive/$binary"
	chmod 755 "$scratch/archive/$binary"
done
cp LICENSE "$scratch/archive/LICENSE"

for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
	tar -C "$scratch/archive" -czf "$bundle/dibs_${platform}.tar.gz" dibs dibsd dibs-mcp LICENSE
done
sh contrib/install/package-release.sh v0.1.0-rc.1 "$bundle"
grep -q 'license "Apache-2.0"' "$bundle/dibs.rb"
grep -q 'pkgshare.install "LICENSE"' "$bundle/dibs.rb"
if command -v ruby >/dev/null 2>&1; then
	ruby -c "$bundle/dibs.rb" >/dev/null
fi

printf 'user data\n' > "$scratch/home/.local/share/dibs/keep.txt"
HOME="$scratch/home" BINDIR="$scratch/home/.local/bin" \
	DIBS_RELEASE_BASE_URL="file://$bundle" sh "$bundle/install.sh" > "$scratch/install.log"
test "$("$scratch/home/.local/bin/dibs" version)" = test-binary
test "$("$scratch/home/.local/bin/dibsd" version)" = test-binary
test "$("$scratch/home/.local/bin/dibs-mcp" version)" = test-binary
cmp LICENSE "$scratch/home/.local/share/licenses/dibs/LICENSE"
HOME="$scratch/home" BINDIR="$scratch/home/.local/bin" \
	DIBS_RELEASE_BASE_URL="file://$bundle" sh "$bundle/install.sh" > "$scratch/reinstall.log"
test "$(cat "$scratch/home/.local/share/dibs/keep.txt")" = 'user data'
cmp LICENSE "$scratch/home/.local/share/licenses/dibs/LICENSE"

# The release asset must resolve its archive to its own tag, even if a newer
# release appears after the user fetched install.sh.
mkdir -p "$scratch/mock-bin"
cat > "$scratch/mock-bin/curl" <<'EOF'
#!/bin/sh
case "$2" in
  https://github.com/abevz/dibs/releases/download/v0.1.0-rc.1/*) ;;
  *) echo "unexpected release URL: $2" >&2; exit 1 ;;
esac
asset=${2##*/}
cp "$DIBS_TEST_BUNDLE/$asset" "$4"
EOF
chmod 755 "$scratch/mock-bin/curl"
PATH="$scratch/mock-bin:$PATH" DIBS_TEST_BUNDLE="$bundle" VERSION=latest HOME="$scratch/home" \
	BINDIR="$scratch/home/.local/bin" sh "$bundle/install.sh" > "$scratch/pinned.log"

printf 'tamper\n' >> "$bundle/dibs_linux_amd64.tar.gz"
if HOME="$scratch/home" BINDIR="$scratch/home/.local/bin" \
	DIBS_RELEASE_BASE_URL="file://$bundle" sh "$bundle/install.sh" > "$scratch/tampered.log" 2>&1; then
	echo 'tampered release archive was accepted' >&2
	exit 1
fi
test "$("$scratch/home/.local/bin/dibs" version)" = test-binary

echo 'release installer fixture checks passed'
