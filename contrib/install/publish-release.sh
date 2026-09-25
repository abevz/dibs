#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <release-tag> <bundle-directory>" >&2
	exit 2
fi

version=$1
bundle=$2
cd "$bundle"

if gh release view "$version" >/dev/null 2>&1; then
	gh release upload "$version" ./*.tar.gz checksums.txt install.sh dibs.rb --clobber
else
	set --
	case "$version" in
		*-*) set -- --prerelease --latest=false ;;
	esac
	gh release create "$version" ./*.tar.gz checksums.txt install.sh dibs.rb \
		--verify-tag --title "$version" --notes-from-tag "$@"
fi
