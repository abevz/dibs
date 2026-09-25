#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <release-tag> <archive-directory>" >&2
	exit 2
fi

version="$1"
bundle="$2"
case "$version" in
	v[0-9]*) ;;
	*) echo "release tag must begin with v and a digit: $version" >&2; exit 2 ;;
esac
case "$version" in
	*[!A-Za-z0-9._+-]*)
		echo "release tag contains an unsupported character" >&2
		exit 2
		;;
esac

for platform in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64; do
	if [ ! -s "$bundle/dibs_${platform}.tar.gz" ]; then
		echo "missing release archive: dibs_${platform}.tar.gz" >&2
		exit 1
	fi
done

(
	cd "$bundle"
	sha256sum dibs_*.tar.gz > checksums.txt
)

checksum() {
	awk -v archive="$1" '$2 == archive { print $1 }' "$bundle/checksums.txt"
}

linux_amd64_sha="$(checksum dibs_linux_amd64.tar.gz)"
linux_arm64_sha="$(checksum dibs_linux_arm64.tar.gz)"
darwin_amd64_sha="$(checksum dibs_darwin_amd64.tar.gz)"
darwin_arm64_sha="$(checksum dibs_darwin_arm64.tar.gz)"

{
	printf '#!/bin/sh\nDIBS_RELEASE_VERSION=%s\n' "$version"
	sed '1d' contrib/install/install-release.sh
} > "$bundle/install.sh"
chmod 755 "$bundle/install.sh"

cat > "$bundle/dibs.rb" <<EOF
class Dibs < Formula
  desc "Local execution ledger for AI agents"
  homepage "https://github.com/abevz/dibs"
  version "${version#v}"
  license "Apache-2.0"

  on_arm do
    on_macos do
      url "https://github.com/abevz/dibs/releases/download/$version/dibs_darwin_arm64.tar.gz"
      sha256 "$darwin_arm64_sha"
    end
    on_linux do
      url "https://github.com/abevz/dibs/releases/download/$version/dibs_linux_arm64.tar.gz"
      sha256 "$linux_arm64_sha"
    end
  end

  on_intel do
    on_macos do
      url "https://github.com/abevz/dibs/releases/download/$version/dibs_darwin_amd64.tar.gz"
      sha256 "$darwin_amd64_sha"
    end
    on_linux do
      url "https://github.com/abevz/dibs/releases/download/$version/dibs_linux_amd64.tar.gz"
      sha256 "$linux_amd64_sha"
    end
  end

  def install
    bin.install "dibs", "dibsd", "dibs-mcp"
    pkgshare.install "LICENSE"
  end

  test do
    system "#{bin}/dibs", "version"
  end
end
EOF
