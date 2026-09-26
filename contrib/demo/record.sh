#!/usr/bin/env bash
# Record the README demo: run race.tape with vhs, then assemble the GIF.
# Run from anywhere; requires vhs (with ttyd), ffmpeg, tmux, jq, git, and Go.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
out="${1:-$root/docs/assets/dibs-race-demo.gif}"
frames="$root/.demo-frames"
tmp="$root/.demo-tmp"

cd "$root"
rm -rf "$frames" "$tmp"
mkdir -p "$tmp"
# vhs moves its frame directory out of TMPDIR with a rename, which fails
# silently across filesystems (for example tmpfs /tmp), so keep it local.
TMPDIR="$tmp" vhs contrib/demo/race.tape

# vhs stores terminal text and cursor as separate layers at 50 fps. Merge
# them, pad like a terminal window, drop to 10 fps, and use one palette
# without dithering so text stays sharp.
ffmpeg -v error -y \
	-framerate 50 -i "$frames/frame-text-%05d.png" \
	-framerate 50 -i "$frames/frame-cursor-%05d.png" \
	-filter_complex "[0][1]overlay,fps=10,pad=iw+32:ih+32:16:16:color=#171717,split[a][b];[a]palettegen=max_colors=64:stats_mode=full[p];[b][p]paletteuse=dither=none" \
	"$out"
rm -rf "$frames" "$tmp"
echo "wrote $out"
