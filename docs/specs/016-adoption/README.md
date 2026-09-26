# 016 Adoption: First External Users

Your AI agents call dibs on work. Exactly one wins.

Status: packet approved on 2026-09-23; active. Planning issue: `afc-127`;
epic: `afc-126`. The saved delivery update below is for later implementation.

## Saved delivery plan — 2026-09-25

The [delivery plan](implementation-plan.md) records the latest owner direction:
Linux/macOS installation and first use come first; positioning uses dibs' own
workflows; attachments and GitHub integration follow; Jira is a possible future
adapter/plugin, with no instance available for validation. A usable preview
does not wait for swarm or a fixed number of testers.

Implementation is deferred to a later session. Read the plan's resume checklist
before acting on the earlier delivery waves below; reconcile the affected
requirements/design/tasks and live dependencies before implementing each slice.
Planning task: `afc-149`.

## Original packet scope

The first external user should be able to install the released CLI, initialize
coordination inside a Git repository, create an issue, and make a successful
first claim in four user commands and at most two minutes on a clean supported
machine, without `make` or a service manager. A
recorded demonstration should show several agents draining one ready queue
without duplicate ownership. Measure both outcomes; a passing unit test is not
a substitute for the clean-machine or multi-agent demonstration.

This packet defines the adoption path, not a new execution authority. The
daemon remains the only supported writer to SQLite; clients use its local Unix
socket API. Packet [015](../015-coordination-safety/README.md) owns the safety
contract. The coordinator's live issues own execution status.

## Original delivery waves (2026-09-23; historical)

This table preserves the earlier ordering and dependency conditions. The
`afc-137` rename and packet 015 safety prerequisites have since completed;
`afc-128` and `afc-129` are ready at the saved plan's baseline. The later plan
above records the revised preview order; reconcile affected task contracts
before implementation rather than treating this historical table as live status.

| Wave | Scope | Gate |
| --- | --- | --- |
| A | `afc-137` `dibs`/`dibsd` rename; `afc-128` release path; `afc-129` init and first-use daemon start; `afc-130` read-only watch; `afc-131` newcomer README | Can proceed alongside `afc-102`; `afc-137` blocks `afc-128` and `afc-129`. Public tag and README claims wait for verified release and clean-machine path. |
| B | `afc-132` Claude Code/Codex hooks; `afc-133` swarm with isolated worktrees | Wait for `afc-102` and `afc-120`–`afc-122` to be complete and verified. |
| C | `afc-158` README race demo; `afc-134` launch post | After hooks (`afc-132`) and the race demo re-run against released binaries; swarm (`afc-133`) is not a prerequisite. Owner writes and publishes final text. |

`requirements.md` states acceptance, `design.md` records boundaries and owner
decisions, `tasks.md` maps the existing child issues to slices, and `review.md`
records packet and implementation review. No child implementation is included
in `afc-127`.

## Original packet approval

The owner approved the original packet on 2026-09-23 (PR #66); see
[review.md](review.md). The dated delivery update records subsequent direction
and the reconciliation needed before affected implementation begins. Saving
that update does not mark any implementation slice complete.
