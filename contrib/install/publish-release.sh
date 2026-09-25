#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <release-tag> <bundle-directory>" >&2
	exit 2
fi

version=$1
bundle=$2
cd "$bundle"

set --
case "$version" in
	*-*) set -- --prerelease --latest=false ;;
esac

state=$(gh release view "$version" --json isDraft --jq .isDraft 2>/dev/null) || state=missing
case "$state" in
	true)
		gh release upload "$version" ./*.tar.gz checksums.txt install.sh dibs.rb --clobber
		gh release edit "$version" --draft=false "$@"
		;;
	false)
		gh release upload "$version" ./*.tar.gz checksums.txt install.sh dibs.rb --clobber
		;;
	missing)
	gh release create "$version" ./*.tar.gz checksums.txt install.sh dibs.rb \
		--verify-tag --title "$version" --notes-from-tag "$@"
		;;
	*)
		echo "unexpected release state for $version: $state" >&2
		exit 1
		;;
esac
