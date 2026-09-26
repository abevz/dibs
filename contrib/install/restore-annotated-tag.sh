#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <release-tag> <expected-commit>" >&2
	exit 2
fi

version=$1
expected_commit=$2
git check-ref-format "refs/tags/$version"

# actions/checkout can fetch an annotated tag, then replace its local ref with
# the peeled commit when the workflow itself was triggered by that tag.
git fetch --force origin "+refs/tags/$version:refs/tags/$version"

if [ "$(git cat-file -t "refs/tags/$version")" != tag ]; then
	echo "release tag is not annotated: $version" >&2
	exit 1
fi
if [ "$(git rev-list -n 1 "refs/tags/$version")" != "$expected_commit" ]; then
	echo "release tag points to an unexpected commit: $version" >&2
	exit 1
fi
