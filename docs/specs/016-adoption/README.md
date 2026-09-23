# 016 Adoption: First External Users

Status: draft for owner approval. Planning issue: `afc-127`; epic: `afc-126`.

The first external user should be able to install the released CLI, initialize
coordination inside a Git repository, and make a successful first claim in at
most three user commands and two minutes on a clean supported machine. A
recorded demonstration should show several agents draining one ready queue
without duplicate ownership. Measure both outcomes; a passing unit test is not
a substitute for the clean-machine or multi-agent demonstration.

This packet defines the adoption path, not a new execution authority. The
daemon remains the only supported writer to SQLite; clients use its local Unix
socket API. Packet [015](../015-coordination-safety/README.md) owns the safety
contract. The coordinator's live issues own execution status.

## Delivery waves

| Wave | Scope | Gate |
| --- | --- | --- |
| A | `dibs`/`dibsd` rename; `afc-128` release path; `afc-129` init and first-use daemon start; `afc-130` read-only watch; `afc-131` newcomer README | Can proceed alongside `afc-102`; public tag and README claims wait for verified release and clean-machine path. |
| B | `afc-132` Claude Code/Codex hooks; `afc-133` swarm with isolated worktrees | Wait for `afc-102` and `afc-120`–`afc-122` to be complete and verified. |
| C | `afc-134` demo GIF and launch post | After B demonstration; owner writes and publishes final text. |

`requirements.md` states acceptance, `design.md` records boundaries and owner
decisions, `tasks.md` maps the existing child issues to slices, and `review.md`
records packet and implementation review. No child implementation is included
in `afc-127`.

## Packet approval

The owner must review the open decisions in `design.md` and approve this packet
before it is treated as an implementation contract. Existing owner decisions in
the `afc-127` notes are recorded as decisions, not reopened as questions.
