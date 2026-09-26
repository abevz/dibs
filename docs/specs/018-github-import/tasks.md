# 018 GitHub Import Tasks

Implementation starts only after the owner approves this packet in
[review.md](review.md). Each slice gets its own worktree, claim, and PR; the
owner merges.

| Slice | Issue | Depends on | Acceptance evidence |
| --- | --- | --- | --- |
| Import | `afc-163` | Packet approval | `internal/github` client interface, `gh`-backed implementation, and reference parser; `dibs issue import` per R-01–R-06 and design "Import"; table and command tests with a fake client and scratch daemon, including concurrent import producing one issue; help catalog entry; scratch check with a fake `gh` on `PATH`. |
| Publish | `afc-164` | `afc-163` | `dibs issue publish` and `--publish` on `issue close`/`issue run` per R-07–R-10 and design "Publish"; tests for preconditions, rendering, marker idempotency, reopen and reclose, and publish failure after a successful close; no secret or local path in rendered comments. |
| End-to-end and release | `afc-165` | `afc-163`, `afc-164` | README "Work from GitHub Issues" section; agent-protocol trust rule (R-11); owner tags `v0.1.0-rc.4`; the R-12 real round trip on a throwaway issue in an owner-chosen repository, using the published binaries; transcript and comment links recorded in `review.md`. |

## Order

1. Owner approves this packet (records it in `review.md` and on `afc-162`).
2. `afc-163`, then `afc-164`; they share `internal/github`.
3. `afc-165` after both merge. The owner approves and creates the tag; the
   agent does not tag or publish releases.

## Verification budget

Follow the repository `AGENTS.md` "Verification budget". Neither slice is
platform-specific, so no cross-compilation is needed beyond CI. Tests must
not depend on the developer's `gh` login or environment.
