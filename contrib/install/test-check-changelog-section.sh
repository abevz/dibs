#!/bin/sh
set -eu

repo_root=$(pwd)
script="$repo_root/contrib/install/check-changelog-section.sh"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

cat > "$scratch/CHANGELOG.md" <<'EOF'
# Changelog

## Unreleased

## v1.2.0 — 2026-01-02

### Added

- Something. Mentions ## v9.9.9 inline, which is not a heading.

## v1.1.0 — 2026-01-01
EOF

sh "$script" v1.2.0 "$scratch/CHANGELOG.md" >/dev/null
sh "$script" v1.1.0 "$scratch/CHANGELOG.md" >/dev/null

for missing in v1.3.0 v1.2 v9.9.9 Unreleased-x; do
	if sh "$script" "$missing" "$scratch/CHANGELOG.md" >/dev/null 2>&1; then
		echo "expected failure for $missing" >&2
		exit 1
	fi
done

if sh "$script" v1.2.0 "$scratch/absent.md" >/dev/null 2>&1; then
	echo "expected failure for a missing changelog file" >&2
	exit 1
fi

# The repository changelog must keep a section for every published tag
# reachable from HEAD, starting with v0.3.0 (the first tag cut without one).
for tag in v0.3.0 v0.3.1; do
	sh "$script" "$tag" "$repo_root/CHANGELOG.md" >/dev/null
done

echo "check-changelog-section tests passed"
