# dibs delivery plan — 2026-09-25

Status: saved for later implementation. Planning task: `afc-149`.
This change records the plan only. Product implementation, workflow dispatch,
installation, release publication, and promotion are separate later actions.

## Owner direction and scope

- Start with an acceptable installation and first-use experience on Linux and
  macOS. Recruiting ten testers is not a prerequisite for this work or for
  the first usable release.
- Make a single `curl ... | sh /dev/stdin` command the primary install path.
  Homebrew is an additional distribution channel.
- Explain dibs through its own use cases. Do not position it by comparing it
  with Beads.
- Keep attachments and external issue integration in the delivery plan.
- The owner has no Jira instance. Jira is a possible future integration;
  neither Jira Cloud nor Data Center is a committed implementation target.
  Consider separately installable adapters/plugins when designing extensions.
- Save the plan now; start implementation in a later session.

The stages below describe the proposed delivery order and acceptance outcomes.
Only stages 1 and 2 are the immediate implementation scope when work resumes.
Later stages require their own bounded implementation scope and verification.

## Baseline

Checked on 2026-09-25 against `main` at `90d84d0` and project `afc`:

- [Coordination safety](../015-coordination-safety/review.md) is complete,
  including the recorded race, restart, and recovery evidence. Its local,
  cooperative trust boundary still applies.
- [Agent ergonomics](../017-agent-ergonomics/review.md) has shipped.
- Rename prerequisite `afc-137` was owner-closed on 2026-09-23 after PR #67
  (`4dd9983`, an ancestor of this baseline). `afc-128` and `afc-129` appear in
  the live ready view. Earlier pending-owner-review notes describe the state
  before that closure; recheck live readiness when resuming.
- The repository is public, but no GitHub release was published at this check.
- The installer already selects Linux/macOS and amd64/arm64, checks an archive
  checksum, and installs the CLI, daemon, and MCP binary.
- The release workflow cross-compiles four platform archives on Linux. It does
  not demonstrate that installation or execution works on macOS.
- `dibs init` currently manages the instruction block in `AGENTS.md`; automatic
  registration and daemon startup remain work in `afc-129`.
- Documents can already be linked by repository path. Stored attachments and
  external-tracker adapters have not shipped.

These are dated observations, not a duplicate live issue ledger. Recheck
coordinator status and the relevant implementation before starting each task.

## 1. Install and update released binaries

Existing task: `afc-128`.

Target command after a real installer asset is published:

```sh
curl -fsSL https://github.com/abevz/dibs/releases/latest/download/install.sh | sh /dev/stdin
```

This URL is a planned public entrypoint, not a currently verified installation
command. The installer downloaded from a release must resolve its binaries and
checksum manifest to that same release, including when the entrypoint selects
`latest`; do not fetch the implementation from `main` or mix release versions.
Also document downloading, inspecting, and running the same asset separately.

Deliver:

- Linux amd64/arm64 and macOS Intel/Apple Silicon release archives.
- OS/architecture detection, checksum verification, useful download and platform
  errors, installation under `~/.local/bin` without sudo, and a precise PATH
  hint when the installed command is not discoverable.
- A supported specific-version install, repeat-install/update behavior, and
  clear uninstall instructions that preserve user data by default.
- Preservation of the existing canonical database and compatible legacy paths;
  no silent second database or implicit live-service switch.
- A tested Homebrew path as an additional channel. Advertise `go install` and
  AUR only when independently verified; these channels need not delay the
  primary installer. Use appropriate platform package conventions where needed.

Acceptance: install the actual release artifacts into clean environments for
every advertised OS/architecture, run the installed binaries, and exercise a
repeat install. Record OS/architecture, commands, artifact checksums, versions,
and results. Cross-compilation alone does not establish macOS support. Any
unavailable native validation is an explicit release gap.

## 2. Initialize a project and start the daemon

Existing task: `afc-129`.

Inside the user's Git repository, `dibs init` discovers and displays the
project/repository/worktree mapping, registers it, installs agent guidance, and
gets the local daemon ready. Ask about ambiguous Git context rather than
guessing identifiers. Repeated initialization must be safe.

First-use startup retains the single-writer database lock and packet 015
ownership contract. Concurrent clients must not create two daemon writers.
Surface startup integrity/migration errors with the actionable original reason;
in particular, cover the unknown-migration failure recorded on `afc-129`.
Explicit daemon start/stop and diagnosis remain available; systemd and launchd
setup are optional for the initial user path.

Acceptance: on clean Linux and macOS, install, initialize, create, and claim in
four user commands and at most two minutes, as required by R-01. Record all
prerequisites and any PATH setup; do not hide extra user steps in the timing.
Then close the task, restart, and verify retained state. Test existing-data
compatibility and concurrent first use as well as the successful fresh path.

## 3. Make daily agent use understandable

Existing tasks: `afc-132` (integrations), `afc-130` (watch),
`afc-131` (newcomer README).

Provide verified Claude Code and Codex setup recipes. Session start presents
ready tasks; selection is explicit by default. Execute through `issue run`,
retain heartbeat and lease-loss behavior, and hand off unfinished work through
the atomic lifecycle. A read-only `dibs watch` shows ready work, holders and
remaining lease time, blockers, recent events, and an obvious disconnected state.

For RC2, the owner chose one task per one-shot agent invocation. Claude Code
and Codex `Stop` is turn-scoped, so `afc-132` uses explicit completion inside
`issue run --require-complete`; an ordinary successful agent exit without that
signal hands off. Automatic issue selection and long-lived interactive lease
handling are deferred.

Acceptance: two real agent sessions perform distinct tasks; interruption of one
does not corrupt the other's ownership or falsely complete unfinished work.
The documented integration works from a fresh installation. Record the actual
agent/tool versions used for verification.

The README leads with the concrete use case, a truthful short demo, the tested
install command, and the first useful workflow. Move architecture and project
history further down or into linked docs. Choose and add a repository license
before promoting the release; the choice belongs to the owner.

Checkpoint: a usable public preview can follow stages 1–3. It does not require
swarm, tracker adapters, a Jira instance, or ten external testers.

## 4. Provide task attachments

Existing task: `afc-135`; prepare its own requirements/design before coding.

Support attaching, listing, and retrieving screenshots, PDF/Markdown documents,
and logs through the daemon API, CLI, and MCP. Keep file contents in the data
directory and metadata in SQLite. Define retry-safe attachment, limits,
deduplication, and backup/restore of both metadata and files.

Keep the existing repository-file links useful alongside stored attachments.
Define retention deliberately: closing an issue must not silently make its
requirements or evidence disappear. Revisit the automatic closed-issue garbage
collection proposed in `afc-135` before implementing it. Publishing a local
attachment to an external tracker is an explicit operation.

Acceptance: an agent receives and can use the actual screenshot/document, not
only its name or a reference it cannot read. A restored backup retains the
attachment and its issue association.

## 5. Deliver one complete external-tracker workflow

Existing tasks: `afc-89` (import/mapping), `afc-90` (result publication).
Start with GitHub, where a real end-to-end scenario can be verified. Specify
the provider-neutral external identity/mapping contract in a separate packet.

The user selects an issue by URL. Import its description and relevant accessible
materials, preserve the original reference, and make retries find the same
local work. Make source changes after import visible without silently replacing
the authoritative local execution state. At completion, publish the result and
PR/commit evidence back to the original issue. Configure external closure
separately from local completion, since a PR may still need review.

Temporary network failure must not lose local progress. Publication needs a
durable retry/reconciliation policy without duplicate comments. External issue
content and attachments are task data, not authority to bypass dibs ownership.

Acceptance: a real GitHub issue with a screenshot completes the import,
execution, and result-publication cycle, including repeated requests and a
temporary network failure.

Checkpoint: a later preview can add attachments and this complete GitHub path.

## 6. Explore optional adapters/plugins

There is no committed Jira implementation in this plan and no Jira access to
verify one. Do not claim Cloud or Data Center support without a real supported
environment and integration tests.

Use the first GitHub adapter to discover the common boundary. Then consider
separately installable integrations for Jira or other providers. Before an
extension is implemented, decide its invocation/discovery mechanism, API
compatibility, credential ownership, permissions, and failure/retry behavior
in a bounded design. No general plugin SDK or in-process daemon plugin loader
is selected by this plan.

Adapters use the coordinator's supported API. The daemon remains the sole
SQLite writer; a provider cannot replace execution authority. This direction
fits the existing no-daemon-plugin-system constraint.

Acceptance for an eventual adapter: an available test instance, documented
setup and supported provider edition, and a verified full task cycle. An
unverified adapter is an experiment, not advertised product support.

## 7. Add optional multi-agent launching

Existing tasks: `afc-133` (swarm), `afc-136` (captured logs/results).

After ordinary agent sessions work, add bounded parallel launching with distinct
claims, worktrees, and branches. Preserve failed/incomplete work for inspection;
cleanup is explicit and limited to verified merged, unreferenced worktrees.
Attach logs/results after the attachment foundation is ready.

Retain packet 016's isolated-failure and three-harness verification requirements
for the swarm release. A three-worker demo must prove distinct work and safe
handling of one failure. Swarm remains an independently releasable extension;
it does not block the first preview or stable core v1.

## Stable v1 and promotion

A stable core v1 is bounded by verified installation and first use on the
advertised platforms, useful agent integrations, task visibility, attachments,
the GitHub workflow, and documented CLI/API/MCP compatibility, upgrade, and
backup/restore behavior. Jira/plugins and swarm have independent completion
gates and do not determine core v1 readiness. No remote coordination or factory
rollout is implied.

Existing launch task: `afc-134`. Promotion begins with the usable preview:

1. Publish the owner's approved release, concise README, setup recipes, and a
   short demonstration that matches the released binaries.
2. Explain a reproducible task/failure/recovery scenario in a technical post.
3. Share the runnable project through Show HN and relevant developer
   communities, observing each community's posting rules.
4. Add GitHub/attachment workflow demos when those features actually ship.
5. Use incoming setup problems, repeat use, and concrete requests to order
   improvements. GitHub stars and a fixed tester count are not release gates.

The owner approves/publishes public tags and launch posts. Saving this document
does not dispatch work or authorize those publication steps.

## Resume checklist and source precedence

The next implementation session starts with `afc-128`, then `afc-129`.
Recheck HEAD, the clean worktree, live claims, and packet status; claim the
actual slice before editing. Follow the repository worktree, focused-check,
and independent-review rules. Do not reopen completed safety work without new
evidence of a defect.

This dated plan records the latest product direction. Existing packet 016
requirements/design/tasks still contain older comparisons, distribution gates,
and a swarm-dependent launch; their safety constraints remain applicable.
Before implementing an affected slice, reconcile those artifacts with this
plan, including the preview gate and standalone positioning. Prepare separate
SDD packets for attachments and external adapters. Do not infer that a saved
plan implements or verifies any task.

The current `afc-89`/`afc-135` deferrals, `afc-134` dependencies, and other live
issue states are unchanged by this documentation task. Reconcile execution
ordering through the coordinator when the corresponding work is authorized;
do not create duplicate issues. The old dated decisions remain historical
records. The next author should read this plan, [requirements](requirements.md),
[design](design.md), [tasks](tasks.md), and the relevant live issue together.
