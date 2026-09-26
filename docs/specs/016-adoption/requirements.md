# 016 Adoption Requirements

## Outcome and measurement

- **R-01 First claim.** On a clean supported machine, a newcomer can install,
  run `dibs init`, create an issue, and successfully run `dibs issue claim` in
  four user commands and at most two minutes, without a source checkout,
  `make`, or mandatory service-manager setup. Record machine/OS, release
  artifact, exact commands, elapsed time, and claim result. The commands must
  work as documented; do not count shell setup hidden inside a script as proof
  of the user path. No demo or seeded issue is required.
- **R-02 Truthful release.** Published install instructions must resolve to a
  real verified artifact. The primary install is
  `curl -fsSL <release URL>/install.sh | sh /dev/stdin`. The release asset
  embeds its own tag and reuses the checksum-verifying
  `contrib/install/install-release.sh` so a newer release cannot change the
  binary downloaded after the script was fetched. It installs to
  `~/.local/bin` without sudo and prints a PATH hint. Document a
  download-inspect-run alternative, a specific-version path, repeat install,
  and removal that preserves runtime data. Verify the archives and installer
  on the advertised Linux and macOS architectures before publication;
  cross-compilation alone is insufficient. Prepare a Homebrew formula from
  release checksums, but advertise the tap, `go install`, and AUR only after
  each channel is tested. The owner alone pushes the public v0.1.0 tag.
  Tag publication must read the remote annotated tag and verify its commit
  before deriving release notes. A failed publication must allow a separate
  public-URL installation check on all advertised platforms without moving
  the already published tag or republishing the release.
- **R-03 Rename compatibility.** Before the public tag, product and CLI become
  `dibs`, daemon `dibsd`, and intended public repository `abevz/dibs`. New
  `DIBS_*` environment variables are introduced; existing `AF_*` variables
  and socket/DB paths remain accepted as
  deprecated aliases so the live installation keeps working. The old live DB
  stays canonical until an explicit migration; do not silently create a second
  authority or database.
- **R-04 First-use setup.** Within a Git repository, `dibs init` infers the
  project, repository, and worktree with an inspectable result. Ambiguous Git
  context requires a question, never a guess. First use starts
  `dibsd` when its socket is absent. Concurrent first calls start at most one
  daemon for a database; startup failure is reported rather than bypassing
  daemon ownership or lease fencing. Explicit daemon start/stop and optional
  service-manager units remain available.
- **R-05 Live view.** `dibs watch` displays ready work, active lease holder and
  remaining TTL, blocked work, and recent events. It reads only through daemon
  APIs and performs no state mutations or direct SQLite reads. It remains
  useful during disconnection and terminal resize without claiming work.
  For `issue run` leases, it displays the self-reported supervisor PID and host.
  New CLI `issue claim` leases report a caller ancestor PID and host when
  available; an explicit session ID takes precedence. Older claims and clients
  without typed process metadata remain unknown. These fields are advisory:
  the claim command can exit and any client can report a typed session ID.
  They never substitute for token and generation ownership.
- **R-06 Newcomer documentation.** The top of the public README explains
  duplicate-work pain, shows a truthful GIF, then the verified four-command
  path and tagline: "Your AI agents call dibs on work. Exactly one wins."
  Show the release-pinned primary install and a download-inspect-run
  alternative. Explain dibs through its own workflow, without comparing it
  with Beads. Keep architecture and SDD history in `docs/`. Claims must match
  the released binaries and tested behavior, and a development-only GIF must
  be labeled as such until its feature is released.
- **R-07 Hooks.** Claude Code and Codex integration shows ready work at session
  start without claiming it. The agent or user chooses one issue explicitly.
  The RC2 mode is one agent command per `issue run --require-complete`; only an
  explicit `dibs hooks complete` inside that run permits closing as done.
  Normal exit without completion and failed runs use the atomic `HANDOFF:`
  path. Lease heartbeats, cancellation and ownership loss use `issue run`.
  Two sessions must not work the same issue, and a lost session's lease must
  become reclaimable. Auto-selection is deferred beyond RC2.
- **R-08 Swarm.** `dibs swarm -n N -- <cmd>` launches an arbitrary configured
  agent command in one separate worktree per claimed issue, supplies issue-run
  context through its environment, and records a branch per issue. A built-in
  Claude Code preset supports the one-line demo. Opening PRs is a later
  opt-in `--pr`, never the default. Keep failed or incomplete worktrees; remove
  only verified, merged, unreferenced worktrees via explicit cleanup. One
  failed worker must not endanger other workers or leave false completion.
  Document a short recipe per supported
  harness and verify at least three harnesses end to end. The owner intends
  to check the generic contract against claude, codex, agy, opencode,
  deepseek harness, and codewhale, plus others.
- **R-09 Demonstration and launch.** A recorded `-n 3` run on a demo
  repository completes distinct ready issues without duplicate claim and
  shows failure isolation. The owner approves the final packet, writes and
  publishes the launch post, and approves the public tag.

## Safety and release gates

- **R-10 One authority.** Zero-config startup, watch, hooks, and swarm must
  preserve the single-writer daemon, atomic ready-qualified claims, token and
  generation fencing, daemon-time expiry, and retry reconciliation defined in
  packet 015. No launcher may keep doing work after confirmed lease loss.
- **R-11 Wave B prerequisites.** Do not release hooks or swarm until `afc-102`
  and `afc-120`–`afc-122` are complete and their safety evidence is reviewed.
  In particular, MCP ownership schemas/propagation, invocation-mode audit,
  and fail-closed CLI parsing must match the daemon contract. A green isolated
  plugin test does not replace these gates.
- **R-12 Verification.** New behavior below `internal/` ships with focused
  tests using production contracts; SQLite store tests use the embedded
  migrations. The first-use race, release installation, multi-session claim,
  failure isolation, and three-harness demonstrations each have recorded
  evidence. No implementation task is marked done from file presence alone.

## Non-goals

No web UI, remote transport, daemon plugin system, multiple writable daemon
instances, replacement storage engine, or automatic PR creation by default.
This packet does not implement its child issues.

## Repository relocation (afc-141)

- A user can update one registered repository's canonical Git path and its registered worktree paths without changing repository, worktree, or issue IDs.
- The daemon verifies that the old and new canonical paths resolve to the same Git common object store. The old path must remain accessible during relocation (a temporary symlink is sufficient); an unprovable or different repository is rejected.
- Every registered worktree must map from the old checkout's parent directory to the new checkout's parent directory and resolve to the same Git worktree. Stale or out-of-tree registrations are rejected for explicit cleanup before retry.
- Registrations created by `dibs init` use the Git common directory, often `<checkout>/.git`; their main worktree must move with the checkout. Linked worktree Git metadata must be repaired before the old compatibility path is removed.
- The update is atomic, audited, and safely replayable with an operation ID. Conflicting reuse of that ID is rejected. Path collisions with another registration are rejected.
- CLI and API return the updated repository and worktree records; MCP can read those results through the existing repository/worktree list tools.
