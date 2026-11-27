#!/bin/sh
# check-changelog-section.sh <tag> [changelog]
#
# Fail unless the changelog has a "## <tag>" release heading. `dibs update`
# reads CHANGELOG.md at the release tag (internal/update/release.go) and
# refuses to upgrade when the section is missing, so a tag without one ships
# a release that existing installs cannot update to.
set -eu

tag=${1:?usage: check-changelog-section.sh <tag> [changelog]}
changelog=${2:-CHANGELOG.md}

if [ ! -f "$changelog" ]; then
	echo "check-changelog-section: $changelog not found" >&2
	exit 1
fi

if ! awk -v tag="$tag" '$1 == "##" && $2 == tag { found = 1 } END { exit !found }' "$changelog"; then
	echo "check-changelog-section: $changelog has no \"## $tag\" section; add release notes before tagging" >&2
	exit 1
fi

echo "check-changelog-section: found \"## $tag\" in $changelog"
