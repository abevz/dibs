# 016 Adoption Design

Status: draft pending owner approval. This packet specifies external behavior
and gates; implementation details remain with the corresponding child slice.

## Boundaries and flow

```text
newcomer: release install -> dibs init -> dibs issue create -> dibs issue claim
                                  |                   |                    |
                                  v                   v                    v
                            local dibsd --------> Unix socket API ------> SQLite

agent: ready view -> selected issue -> issue run -> fenced lease lifecycle
swarm: N isolated worktrees -> N issue-run children -> distinct branches
watch: daemon API -> read-only terminal view
```

`dibs` is the public CLI name, `dibsd` the daemon name. Keep the local daemon
as the sole writer; first-use convenience may launch it but must use the same
DB singleton lock and startup checks as explicit start. The CLI waits for a
healthy socket, handles concurrent first calls without two writers, and
reports startup errors. New `DIBS_*` environment variables are introduced;
existing `AF_*` variables and old socket and DB locations are deprecated
aliases. The old live DB remains canonical
until an explicit migration; the rename slice must prove no second DB is
silently created. Its compatibility test covers legacy and new names.

`init` derives identifiers from Git metadata, including the actual worktree,
then shows the detected mapping before creating coordinator records. Ambiguous
Git context must ask the user; missing Git context must produce an actionable
error, never a guessed global project. The clean-machine measurement includes
the tested release install, init, issue creation, and successful claim against
the started daemon.

`watch` uses existing list/ready/event APIs where sufficient; if lease/TTL or
blocked data are unavailable, add a bounded read-only API surface in its own
slice. Existing Charmbracelet dependencies may be reused. Keep display state
out of the authoritative database and never call mutation endpoints while
refreshing. A disconnected view labels its data stale.

Hooks are thin client-side integrations over `issue run`. SessionStart presents
ready items and does not claim. The selected issue enters the normal
claim/heartbeat/cancel/close-or-handoff path. The one-shot agent command uses
`issue run --require-complete`; its completion
marker permits closing only after `dibs hooks complete`. An unfinished command
exits without that marker, so `issue run` writes a `HANDOFF:` note through the atomic
handoff path. Any hook unable to prove lease ownership must stop work; it must
not assume success from an absent response.

`swarm` takes `-n N -- <cmd>` and launches at most N workers. Each worker gets
one coordinator-selected, atomically claimed issue, one sibling Git worktree,
and the `issue run` environment (`AF_ISSUE_ID`, `AF_LEASE_TOKEN`,
`AF_LEASE_GENERATION`, `AF_ATTEMPT_ID`, `AF_EXPECTED_VERSION`, with compatible
`DIBS_*` aliases). These variables are process-private: never place
lease tokens in logs, branches, PRs, or shared worktree metadata. The claim
remains the authority; a directory or process existing is not ownership.
Workers return issue/branch/result records. A Claude Code preset is an adapter
to this generic command contract. A worker failure hands off according to the
handoff lifecycle policy and does not close its issue or cancel unrelated
workers. Failed and incomplete worktrees remain for inspection. An explicit
cleanup command may remove only verified, merged, unreferenced worktrees.

## Wave B dependency contract

Both `afc-132` and `afc-133` are blocked on all of `afc-102` and
`afc-120`–`afc-122`, not merely on their docs. The concrete packet 015
guarantees they consume are:

| Guarantee | Hook reliance | Swarm reliance |
| --- | --- | --- |
| R-02 single mutation authority and DB singleton | Hooks use daemon API only | Concurrent first use and all workers share one daemon |
| R-03 atomic ready-qualified claim and R-07 computed ready graph | Two sessions cannot both acquire one item | N workers drain distinct eligible issues |
| R-04–R-08 token, generation, expiry, lifecycle and daemon time | Stop/close cannot act after ownership loss | Failed or stale worker cannot close a successor's issue |
| R-09–R-10 idempotent retry and crash recovery | Ambiguous hook outcome can be reconciled | Restart or lost response cannot duplicate a claim/result |
| R-11 `issue run` cancellation and typed errors | Lost lease stops agent work | Lost lease stops child command in its process group |
| R-12–R-13 audit and production-like race proof | Session behavior can be checked | Demo claim uniqueness has evidence beyond UI |

`afc-120` repairs MCP lease-generation schemas and propagation; `afc-121`
propagates invocation mode into MCP audit events; `afc-122` makes CLI parsing
reject malformed machine arguments before mutation. A hook/swarm release must
verify these actual surfaces, not infer safety from packet 015 text. Wave A's
first-use daemon also relies on R-02, but it can be built concurrently and
must prove the singleton race before release.

## Dependencies

Reuse Go stdlib, the current SQLite driver, Git CLI, and existing terminal UI
packages. No new dependency is approved by this packet. A child task proposing
one must document the required capability, why stdlib/current packages do not
suffice, version/license, and maintenance cost before adoption. No framework,
DI container, daemon plugin system, or network service is introduced.

## Distribution

The primary quickstart install is
`curl -fsSL <release URL>/install.sh | sh /dev/stdin`. The `install.sh` release
asset embeds the selected tag, not a script fetched from `main`. It reuses the
checksum-verifying `contrib/install/install-release.sh` and selects the archive
and manifest from that same release. It installs to `~/.local/bin` without sudo
and prints a PATH hint. Document download-inspect-run, version selection,
repeat install, and binary removal without deleting runtime data.
Release verification exercises the actual asset and checksum manifest on each
advertised Linux and macOS architecture before publication. The release
package generates a checksum-pinned Homebrew formula; advertise a tap,
`go install`, or AUR only after that channel is verified. The owner pushes the
public tag.

## Owner decisions recorded from `afc-127` notes (2026-09-23)

1. **Launch identity:** product/CLI `dibs`, daemon `dibsd`, intended repo
   `abevz/dibs`. Rename is a distinct wave-A task before the public tag.
   Retain `AF_*` variables and old socket/DB paths as deprecated aliases.
   GitHub topics: `ai-agents`, `claude-code`, `worktree`, `coordination`.
2. **Plugin claim policy:** SessionStart shows ready work for agent/user choice.
   RC2 requires explicit issue selection; auto-selection is deferred.
3. **Swarm command/result:** arbitrary command after `--`, issue-run context in
   environment, built-in Claude Code preset. Branch per issue by default; PR
   creation is later opt-in `--pr`. Document each harness recipe and verify at
   least three end to end.
4. **Daemon start:** `dibs` forks `dibsd` on demand if socket is absent;
   explicit `dibs daemon start/stop` remains. Service-manager units are
   optional. Concurrent first calls must pass a lock-file singleton test.
5. **First claim target:** install, init, create, claim in four user commands
   and at most two minutes; no `make`, service manager, demo issue, or seed
   issue is needed.
6. **One-shot lifecycle:** Claude Code and Codex `Stop` is turn-scoped. The
   explicit one-command `issue run --require-complete` wrapper handles unfinished
   work through atomic `HANDOFF:` after the child exits, with ownership fencing.
7. **Swarm cleanup:** retain failed/incomplete worktrees; remove only
   verified, merged, unreferenced worktrees through explicit cleanup.
8. **Distribution:** the release-pinned `install.sh` asset is the primary
   one-line install; also provide the inspect-before-run alternative, Homebrew
   tap, `go install`, and AUR package. Defer other channels.
9. **Public tagline:** "Your AI agents call dibs on work. Exactly one wins."
10. **Legacy paths and Git context:** the old live DB remains canonical until
    explicit migration. Ask on ambiguous Git context; never guess.

The public repository rename and topics are owner-side publication actions,
not actions in `afc-127`.

## Later owner direction for newcomer wording

The saved [delivery plan](implementation-plan.md) records the owner's later
decision to explain dibs through its own use cases without a Beads comparison.
This supersedes the comparison clause in the original packet approval for the
README and launch copy. The development `watch` demo must also identify its
build until a published release includes it.
