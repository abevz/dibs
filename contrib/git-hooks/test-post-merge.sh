#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
hook_path=${HOOK_UNDER_TEST:-$repo_root/contrib/git-hooks/post-merge}
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
mkdir "$tmpdir/bin"

cat > "$tmpdir/bin/git" <<'EOF'
#!/bin/sh
if [ "$1" = "rev-parse" ] && [ "$2" = "--show-toplevel" ]; then
	printf '%s\n' "$HOOK_TEST_ROOT"
elif [ "$1" = "-C" ] && [ "$3" = "rev-parse" ] && [ "$4" = "--abbrev-ref" ]; then
	printf '%s\n' "$HOOK_TEST_BRANCH"
else
	exit 1
fi
EOF
cat > "$tmpdir/bin/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$HOOK_TEST_LOG"
case "$*" in
	'--user is-active --quiet dibsd') [ "$HOOK_TEST_ACTIVE" = "dibsd" ] ;;
	'--user is-active --quiet af-coordinatord') [ "$HOOK_TEST_ACTIVE" = "old" ] ;;
	'--user try-restart dibsd') [ "$HOOK_TEST_RESTART_FAIL" != "1" ] ;;
	*) exit 1 ;;
esac
EOF
cat > "$tmpdir/bin/make" <<'EOF'
#!/bin/sh
printf 'make %s\n' "$*" >> "$HOOK_TEST_LOG"
[ "$HOOK_TEST_BUILD_FAIL" != "1" ]
EOF
chmod +x "$tmpdir/bin/git" "$tmpdir/bin/systemctl" "$tmpdir/bin/make"

export HOOK_TEST_ROOT="$repo_root" HOOK_TEST_LOG="$tmpdir/calls"
export PATH="$tmpdir/bin:$PATH"
run_case() {
	name=$1 HOOK_TEST_BRANCH=$2 HOOK_TEST_ACTIVE=$3
	HOOK_TEST_BUILD_FAIL=$4 HOOK_TEST_RESTART_FAIL=$5
	export HOOK_TEST_BRANCH HOOK_TEST_ACTIVE HOOK_TEST_BUILD_FAIL HOOK_TEST_RESTART_FAIL
	: > "$HOOK_TEST_LOG"
	sh "$hook_path" > "$tmpdir/output" 2>&1 || {
		printf '%s: hook must exit 0\n' "$name" >&2
		exit 1
	}
}

run_case feature-branch feature dibsd 0 0
! grep -Eq '^make|try-restart' "$HOOK_TEST_LOG"

run_case active-new main dibsd 0 0
grep -Fqx "make -C $repo_root build-install" "$HOOK_TEST_LOG"
grep -Fqx 'systemctl --user try-restart dibsd' "$HOOK_TEST_LOG"

run_case active-old main old 0 0
! grep -Eq '^make|try-restart' "$HOOK_TEST_LOG"
grep -q 'switch manually' "$tmpdir/output"

run_case inactive main none 0 0
! grep -Eq '^make|try-restart' "$HOOK_TEST_LOG"

run_case build-failure main dibsd 1 0
! grep -q 'try-restart' "$HOOK_TEST_LOG"

run_case restart-failure main dibsd 0 1
grep -q 'try-restart failed' "$tmpdir/output"

printf 'post-merge unit selection: PASS\n'
