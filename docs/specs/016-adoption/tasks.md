# 016 Adoption Tasks

The live coordinator issues determine claim and execution status. This file is
the canonical packet slice map. `afc-128`–`afc-134` already exist; do not
recreate them. Every child gets one worktree and one PR. None is implemented by
this packet-authoring task. Completion requires its acceptance evidence in
`review.md`, not merely a file or a passing unrelated suite.

| Slice | Issue | Wave | Depends on | Acceptance evidence |
| --- | --- | --- | --- | --- |
| Rename product, CLI, daemon, and compatibility aliases | Owner-requested distinct task; coordinator issue to be assigned before implementation | A, before public tag | Packet approval | `dibs`/`dibsd` names and intended repo path in release assets/docs; old `AF_*` and socket/DB paths still work against one live state; migration test and installed-binary check; topics supplied to owner. |
| Release path | `afc-128` | A | Rename before public tag | Dry-run release workflow green on a pre-release tag in a fork or via dispatch; real artifact accepted by checksum installer; `go install` succeeds; Homebrew formula ready. Owner alone pushes v0.1.0 tag. |
| Init and first-use daemon | `afc-129` | A | Packet approval; rename-compatible command path | Fresh Git repo infers project/repo/worktree; install, init, create, claim works without `make`/service manager; concurrent first-call singleton lock test and failure-path tests. Time the first-claim path against R-01 after the owner resolves the first-ready-issue scenario. |
| Live watch | `afc-130` | A | Packet approval | Ready, active lease/TTL, blocked, and recent events refresh from daemon API only; disconnection clear; GIF-ready view. |
| Newcomer README | `afc-131` | A | `afc-128`, `afc-129`; GIF from `afc-130` when available | First screen: pain, truthful GIF, three verified commands, fair Beads/Claude Code tasks/GitHub Issues comparison; architecture/SDD history in `docs/`; release and timing claims match evidence. |
| Claude Code/Codex hooks | `afc-132` | B | `afc-102`, `afc-120`–`afc-122`, packet approval | Ready list shown without default claim; selected work uses `issue run`; explicit auto-claim flag; two parallel sessions cannot own same issue; lost session is reclaimable; unfinished Stop behavior follows owner decision; install documented. |
| Swarm launcher | `afc-133` | B | `afc-102`, `afc-120`–`afc-122`, packet approval | `dibs swarm -n 3 -- <cmd>` uses distinct claims/worktrees and branches; built-in Claude preset; worker failure isolated; no default PR; short recipes for candidate harnesses, at least three verified end to end; recorded demo. |
| Launch | `afc-134` | C | `afc-130`, `afc-132`, `afc-133`; public release and owner sign-off | Demo GIF shows distinct issues with no double claim; owner writes/publishes final post, including honest single-writer/Beads plus shared Dolt framing. |

## Order and handoffs

1. Owner reviews this packet and resolves launch-critical open decisions.
   Record approval in `review.md` and on `afc-127` before closing it. This PR
   is the packet draft and does not claim that approval.
2. Assign a separate coordinator issue for the owner-requested rename before
   starting that task. Do not smuggle it into `afc-128`–`afc-134` or create
   a new issue as part of `afc-127`.
3. Wave A may progress alongside packet 015, but public claims and the tag
   wait for tested install and first-use evidence.
4. Wave B starts only after all named safety blockers are closed with reviewed
   evidence. Wave C follows its verified demo and owner publication approval.

The epic's end-to-end measure is at most three commands and two minutes from
install to successful first claim on a clean machine. Record the exact command
transcript and elapsed time in the implementation review. The current
install/init/create/claim sequence has four operations, so do not advertise it
as the three-command path until the owner decides the first-ready-issue source
and that scenario is measured.
