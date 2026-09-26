# README demo: two simulated agents race for one dibs ready queue.
# Re-record from the repository root with: contrib/demo/record.sh
# Requires vhs (with ttyd and ffmpeg), tmux, jq, git, and Go.

# Frames only: record.sh assembles the GIF with ffmpeg, because vhs 0.12
# produced no GIF with ffmpeg 9 here.
Output .demo-frames/

Require tmux
Require jq
Require go

Set Shell bash
Set FontSize 15
Set Width 1240
Set Height 720
Set Padding 12
Set TypingSpeed 0

Hide
Type "clear && contrib/demo/race.sh" Enter
Wait+Screen@90s /waiting for the queue/
Show

# ~2.5s of the idle board, ~12s of agents working, then the final state.
Sleep 19s

# Quit the board and let race.sh stop its daemon and remove temporary state.
Hide
Type "q"
Wait+Screen@60s /dibs demo cleaned up/
