#!/bin/sh
set -eu

if [ "${DIBS_REPO+x}" ]; then
	repo="$DIBS_REPO"
else
	repo="${AF_COORDINATOR_REPO:-abevz/dibs}"
fi
if [ -z "$repo" ]; then
	echo "DIBS_REPO must not be empty" >&2
	exit 1
fi
version="${VERSION:-latest}"
bindir="${BINDIR:-$HOME/.local/bin}"

case "$(uname -s)" in
	Linux) os="linux" ;;
	Darwin) os="darwin" ;;
	*)
		echo "unsupported OS: $(uname -s)" >&2
		exit 1
		;;
esac

case "$(uname -m)" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*)
		echo "unsupported architecture: $(uname -m)" >&2
		exit 1
		;;
esac

if command -v curl >/dev/null 2>&1; then
	download() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
	download() { wget -q "$1" -O "$2"; }
else
	echo "curl or wget is required" >&2
	exit 1
fi

asset="dibs_${os}_${arch}.tar.gz"
if [ "$version" = "latest" ]; then
	base_url="https://github.com/$repo/releases/latest/download"
else
	base_url="https://github.com/$repo/releases/download/$version"
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

download "$base_url/$asset" "$tmpdir/$asset"
download "$base_url/checksums.txt" "$tmpdir/checksums.txt"

grep "  $asset\$" "$tmpdir/checksums.txt" > "$tmpdir/$asset.sha256"
(
	cd "$tmpdir"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c "$asset.sha256"
	else
		shasum -a 256 -c "$asset.sha256"
	fi
)

tar -xzf "$tmpdir/$asset" -C "$tmpdir"
mkdir -p "$bindir"
install -m 755 "$tmpdir/dibs" "$bindir/dibs"
install -m 755 "$tmpdir/dibsd" "$bindir/dibsd"
install -m 755 "$tmpdir/dibs-mcp" "$bindir/dibs-mcp"
ln -sfn dibs "$bindir/afctl"
ln -sfn dibsd "$bindir/af-coordinatord"
ln -sfn dibs-mcp "$bindir/afc-mcp"

echo "Installed dibs binaries into $bindir"
