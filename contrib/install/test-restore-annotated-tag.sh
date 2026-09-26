#!/bin/sh
set -eu

repo_root=$(pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT

git init --bare "$scratch/origin.git" >/dev/null
git init -b main "$scratch/source" >/dev/null
git -C "$scratch/source" config user.name fixture
git -C "$scratch/source" config user.email fixture@example.invalid
printf 'release fixture\n' > "$scratch/source/content.txt"
git -C "$scratch/source" add content.txt
git -C "$scratch/source" commit -m fixture >/dev/null
commit=$(git -C "$scratch/source" rev-parse HEAD)
git -C "$scratch/source" tag -a v0.1.0-fixture -m 'release fixture'
git -C "$scratch/source" remote add origin "$scratch/origin.git"
git -C "$scratch/source" push origin main refs/tags/v0.1.0-fixture >/dev/null
git -C "$scratch/origin.git" symbolic-ref HEAD refs/heads/main
git clone -q "$scratch/origin.git" "$scratch/checkout"
git -C "$scratch/checkout" fetch origin '+refs/tags/*:refs/tags/*' >/dev/null

# Reproduce checkout's tag-event ref update: the annotated object is fetched,
# but the local tag ref is then overwritten with the event's peeled commit.
git -C "$scratch/checkout" update-ref refs/tags/v0.1.0-fixture "$commit"
test "$(git -C "$scratch/checkout" cat-file -t refs/tags/v0.1.0-fixture)" = commit

(cd "$scratch/checkout" && sh "$repo_root/contrib/install/restore-annotated-tag.sh" v0.1.0-fixture "$commit")
test "$(git -C "$scratch/checkout" cat-file -t refs/tags/v0.1.0-fixture)" = tag

if (cd "$scratch/checkout" && sh "$repo_root/contrib/install/restore-annotated-tag.sh" v0.1.0-fixture 0000000000000000000000000000000000000000 >/dev/null 2>&1); then
	echo 'wrong tag target was accepted' >&2
	exit 1
fi

echo 'annotated tag recovery fixture checks passed'
