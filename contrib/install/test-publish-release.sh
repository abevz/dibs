#!/bin/sh
set -eu

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/bin" "$scratch/bundle"
for asset in dibs_linux_amd64.tar.gz checksums.txt install.sh dibs.rb; do
	: > "$scratch/bundle/$asset"
done
cat > "$scratch/bin/gh" <<'EOF'
#!/bin/sh
if [ "$1" = release ] && [ "$2" = view ]; then
	if [ "$GH_TEST_STATE" = missing ]; then
		exit 1
	fi
	printf '%s\n' "$GH_TEST_STATE"
	exit 0
fi
printf '%s\n' "$@" > "$GH_TEST_ARGS"
EOF
chmod +x "$scratch/bin/gh"

GH_TEST_STATE=missing GH_TEST_ARGS="$scratch/rc-args" PATH="$scratch/bin:$PATH" \
	sh contrib/install/publish-release.sh v0.1.0-rc.1 "$scratch/bundle"
grep -qx -- '--prerelease' "$scratch/rc-args"
grep -qx -- '--latest=false' "$scratch/rc-args"
grep -qx -- '--verify-tag' "$scratch/rc-args"
grep -qx -- '--notes-from-tag' "$scratch/rc-args"

GH_TEST_STATE=missing GH_TEST_ARGS="$scratch/stable-args" PATH="$scratch/bin:$PATH" \
	sh contrib/install/publish-release.sh v0.1.0 "$scratch/bundle"
if grep -Eq -- '^--(prerelease|latest=false)$' "$scratch/stable-args"; then
	echo 'stable release was marked as prerelease' >&2
	exit 1
fi
grep -qx -- '--verify-tag' "$scratch/stable-args"
grep -qx -- '--notes-from-tag' "$scratch/stable-args"

GH_TEST_STATE=true GH_TEST_ARGS="$scratch/draft-args" PATH="$scratch/bin:$PATH" \
	sh contrib/install/publish-release.sh v0.1.0-rc.1 "$scratch/bundle"
grep -qx -- 'edit' "$scratch/draft-args"
grep -qx -- '--draft=false' "$scratch/draft-args"
grep -qx -- '--prerelease' "$scratch/draft-args"
grep -qx -- '--latest=false' "$scratch/draft-args"

echo 'release publication fixture checks passed'
