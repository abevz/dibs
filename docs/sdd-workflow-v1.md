# SDD workflow v1

## Purpose

Define the local SDD standard for `dibs` so implementation stays
spec-first and operational coordination does not replace design discipline.

## Working definition

For this repository, SDD means that every meaningful feature is driven by a
spec packet:

```text
requirements.md -> design.md -> tasks.md -> implementation -> review.md
```

The packet is the source of truth for:

- problem and scope
- functional and non-functional requirements
- design decisions and trade-offs
- implementation task slicing
- review evidence after delivery

The coordinator database is not the source of truth for feature design. It is
the source of truth for execution state.

## Repository convention

Spec packets live under:

```text
docs/specs/NNN-feature-slug/
```

Minimum packet for v1:

- `README.md`
- `requirements.md`
- `design.md`
- `tasks.md`
- `review.md`

Optional later additions:

- `decisions/`
- `traceability.md`
- `glossary.md`
- `schemas/`

## When a spec is required

Use a spec packet when:

- the work changes behavior
- the work spans multiple files or components
- the work needs tests or acceptance criteria
- the work affects API, storage, lease rules, or operational semantics

Skip a full spec packet only for:

- tiny typo fixes
- mechanical renames
- trivial one-file edits with no behavioral impact

## Execution boundary

Use SDD for planning and contract definition.

Use `dibs` issues for:

- claiming work
- tracking in-progress execution
- recording blockers and handoff notes
- coordinating parallel agents
- linking runtime work back to the relevant spec artifacts

This is the intended split:

```text
SDD = plan and design truth
dibs = execution and coordination truth
```

## Artifact model

The coordinator should track spec artifacts explicitly.

At minimum, v1 should support:

- repository spec roots such as `docs/specs/`
- artifact kinds such as `spec_packet`, `requirements`, `design`, `tasks`, `review`, `adr`
- links from issues to one or more artifacts

That allows a claimed issue to point at the exact file that defines the work.

## Authoring rules

- `requirements.md` must define scope and acceptance criteria before coding
- `design.md` must remove major implementation ambiguity before coding
- `tasks.md` must slice work into executable units
- `review.md` captures what shipped, what was verified, and what remains

If coding still requires inventing product or architecture decisions on the fly,
the spec is incomplete and needs another design pass.

## Initial packet

The bootstrap packet for this repository is:

`docs/specs/001-foundation/`

It defines the first implementation horizon for the coordinator itself.
