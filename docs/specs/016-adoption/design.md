# 016 Adoption Design

Status: draft pending owner approval. This packet specifies external behavior
and gates; implementation details remain with the corresponding child slice.

## Boundaries and flow

```text
newcomer: release install -> dibs init -> dibs issue claim
                                  |             |
                                  v             v
                            local dibsd -> Unix socket API -> SQLite

agent: ready view -> selected issue -> issue run -> fenced lease lifecycle
swarm: N isolated worktrees -> N issue-run children -> distinct branches
watch: daemon API -> read-only terminal view
```

`dibs` is the public CLI name, `dibsd` the daemon name. Keep the local daemon
as the sole writer; first-use convenience may launch it but must use the same
DB singleton lock and startup checks as explicit start. The CLI waits for a
healthy socket, handles concurrent first calls without two writers, and
reports startup errors. Existing `AF_*` environment variables and old socket
and DB locations are deprecated aliases. Migration must select one canonical
database and avoid splitting live state; exact precedence and migration steps
belong to the rename slice and require an owner-visible compatibility test.

`init` derives identifiers from Git metadata, including the actual worktree,
then shows the detected mapping before creating coordinator records. Ambiguous
or missing Git context must produce an actionable error, not a guessed global
project. The clean-machine measurement includes the tested release install and
this setup, then a successful claim against the started daemon.

`watch` uses existing list/ready/event APIs where sufficient; if lease/TTL or
blocked data are unavailable, add a bounded read-only API surface in its own
slice. Existing Charmbracelet dependencies may be reused. Keep display state
out of the authoritative database and never call mutation endpoints while
refreshing. A disconnected view labels its data stale.

Hooks are thin client-side integrations over `issue run`. SessionStart presents
ready items and does not claim by default. The selected issue enters the
normal claim/heartbeat/cancel/close-or-handoff path. The Stop-hook's unfinished
work behavior is still an owner decision below. Any hook unable to prove lease
ownership must stop work; it must not assume success from an absent response.

`swarm` takes `-n N -- <cmd>` and launches at most N workers. Each worker gets
one coordinator-selected, atomically claimed issue, one sibling Git worktree,
and the `issue run` environment (`AF_ISSUE_ID`, `AF_LEASE_TOKEN`,
`AF_LEASE_GENERATION`, `AF_ATTEMPT_ID`, `AF_EXPECTED_VERSION`, with compatible
new aliases if introduced). These variables are process-private: never place
lease tokens in logs, branches, PRs, or shared worktree metadata. The claim
remains the authority; a directory or process existing is not ownership.
Workers return issue/branch/result records. A Claude Code preset is an adapter
to this generic command contract. A worker failure hands off according to the
resolved lifecycle policy and does not close its issue or cancel unrelated
workers. Worktree cleanup policy is open below.

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

## Owner decisions recorded from `afc-127` notes (2026-09-23)

1. **Launch identity:** product/CLI `dibs`, daemon `dibsd`, intended repo
   `abevz/dibs`. Rename is a distinct wave-A task before the public tag.
   Retain `AF_*` variables and old socket/DB paths as deprecated aliases.
   GitHub topics: `ai-agents`, `claude-code`, `worktree`, `coordination`.
2. **Plugin claim policy:** SessionStart shows ready work for agent/user choice.
   Auto-claim of top ready item requires an explicit flag for unattended use.
3. **Swarm command/result:** arbitrary command after `--`, issue-run context in
   environment, built-in Claude Code preset. Branch per issue by default; PR
   creation is later opt-in `--pr`. Document each harness recipe and verify at
   least three end to end.
4. **Daemon start:** `dibs` forks `dibsd` on demand if socket is absent;
   explicit `dibs daemon start/stop` remains. Service-manager units are
   optional. Concurrent first calls must pass a lock-file singleton test.

## Open owner decisions

These are questions, with options and a recommendation, not implementation
authorization. The first four prompts from the original note are resolved
above and must not be silently reopened.

| Question | Options and trade-offs | Recommendation |
| --- | --- | --- |
| Stop-hook when work is unfinished | Handoff records context and frees the lease; bare release loses structured context; letting lease expire delays ready work and obscures intent. | Handoff with a concise `HANDOFF:` note through the atomic lifecycle path, after verifying ownership. Owner to confirm. |
| Worktree cleanup after swarm | Keep all for inspection but consume disk; remove on success but risk losing uncommitted evidence; explicit cleanup command makes lifecycle visible. | Keep failed/incomplete worktrees; clean only verified, merged, unreferenced worktrees through explicit cleanup. Owner to confirm. |
| Distribution beyond GitHub release | Homebrew tap and `go install` serve macOS/Go users; AUR broadens Arch reach but adds packaging maintenance; other channels raise ongoing support cost. | Prove Homebrew and `go install` in wave A; defer AUR and further channels until demand is measured. Owner to confirm channel set. |
| Public tagline | A short outcome-oriented line improves newcomer comprehension; technical wording may be precise but less inviting. | Use a factual line about preventing duplicate work by parallel coding agents; owner chooses final copy. |
| Exact Git/record naming and legacy-path precedence | Git remote name is convenient but may collide; directory name is local but unstable. Prefer explicit overrides for ambiguity. Old and new DB paths must never silently diverge. | Show inferred values and require confirmation on ambiguous repositories; preserve old live DB as canonical until an explicit migration. Owner to approve exact rules. |
| First ready issue within three commands | Install, init, create, claim is four operations; a fresh coordinator cannot claim without an existing issue. Options: measure a newcomer joining a pre-populated project (honest but narrower), seed an explicit demo issue during opt-in init (extra behavior), or add an onboarding command that creates and claims atomically (larger product change). | Measure a new user joining a prepared demo project first; label that scenario explicitly. Owner must decide whether the broader fresh-project claim target needs an additional product slice before README promises it. |

The public repository rename and topics are owner-side publication actions,
not actions in `afc-127`.
