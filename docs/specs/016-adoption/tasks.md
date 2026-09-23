# 016 Adoption Tasks

The live coordinator issues determine claim and execution status. This file is
the canonical packet slice map. `afc-128`–`afc-134` and `afc-137` already exist; do not
recreate them. Every child gets one worktree and one PR. None is implemented by
this packet-authoring task. Completion requires its acceptance evidence in
`review.md`, not merely a file or a passing unrelated suite.

| Slice | Issue | Wave | Depends on | Acceptance evidence |
| --- | --- | --- | --- | --- |
| Rename product, CLI, daemon, and compatibility aliases | `afc-137` | A, before public tag | Packet approval | `dibs`/`dibsd` names and intended repo path in release assets/docs; new `DIBS_*` variables plus old `AF_*` and socket/DB paths work against the old live canonical DB until explicit migration; no second DB silently created; migration test and installed-binary check; topics supplied to owner. |
| Release path | `afc-128` | A | `afc-137` | Dry-run release workflow green on a pre-release tag in a fork or via dispatch; release-pinned `install.sh` asset supports `curl -fsSL <release URL>/install.sh \| sh`, reuses checksum-verifying `contrib/install/install-release.sh`, installs to `~/.local/bin` without sudo and prints PATH hint; real artifact verified; README documents download-inspect-run alternative; Homebrew tap, `go install`, and AUR package ready and tested before advertised; other channels deferred. Owner alone pushes v0.1.0 tag. |
| Init and first-use daemon | `afc-129` | A | `afc-137` | Fresh Git repo infers project/repo/worktree; ambiguous Git context asks; install, init, create, claim works in four commands and at most two minutes on a clean supported machine without `make`/service manager; concurrent first-call singleton lock test and failure-path tests. |
| Live watch | `afc-130` | A | Packet approval | Ready, active lease/TTL, blocked, and recent events refresh from daemon API only; disconnection clear; GIF-ready view. |
| Newcomer README | `afc-131` | A | `afc-128`, `afc-129`; GIF from `afc-130` when available | First screen: pain, truthful GIF, tagline, four verified commands with release-pinned install plus download-inspect-run alternative, fair Beads/Claude Code tasks/GitHub Issues comparison; architecture/SDD history in `docs/`; release and timing claims match evidence. |
| Claude Code/Codex hooks | `afc-132` | B | `afc-102`, `afc-120`–`afc-122`, packet approval | Ready list shown without default claim; selected work uses `issue run`; explicit auto-claim flag; two parallel sessions cannot own same issue; lost session is reclaimable; unfinished Stop writes atomic `HANDOFF:` after ownership check; install documented. |
| Swarm launcher | `afc-133` | B | `afc-102`, `afc-120`–`afc-122`, packet approval | `dibs swarm -n 3 -- <cmd>` uses distinct claims/worktrees and branches; built-in Claude preset; worker failure isolated; failed/incomplete worktrees kept and explicit cleanup removes only verified merged unreferenced ones; no default PR; short recipes for candidate harnesses, at least three verified end to end; recorded demo. |
| Launch | `afc-134` | C | `afc-130`, `afc-132`, `afc-133`; public release and owner sign-off | Demo GIF shows distinct issues with no double claim; owner writes/publishes final post, including honest single-writer/Beads plus shared Dolt framing. |

## Order and handoffs

1. Owner reviews the updated packet and records approval in `review.md` and
   on `afc-127` before closing it. This PR does not claim that approval.
2. `afc-137` owns the distinct rename and blocks `afc-128` and `afc-129`.
3. Wave A may progress alongside packet 015, but public claims and the tag
   wait for tested install and first-use evidence.
4. Wave B starts only after all named safety blockers are closed with reviewed
   evidence. Wave C follows its verified demo and owner publication approval.

The epic's end-to-end measure is install, init, create, claim in four commands
and at most two minutes on a clean supported machine without `make` or a
service manager. Record the exact command transcript and elapsed time in the
implementation review.
