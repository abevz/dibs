# 018 GitHub Import Tasks

Implementation starts only after the owner approves this packet in
[review.md](review.md). Each slice gets its own worktree, claim, and PR; the
owner merges.

| Slice | Issue | Depends on | Acceptance evidence |
| --- | --- | --- | --- |
| Import | `afc-163` | Packet approval | `internal/github` client interface, `gh`-backed implementation, and reference parser; `dibs doctor` "GitHub CLI" readiness check per R-13; `dibs issue import` per R-01–R-06 and design "Import"; table and command tests with a fake client and scratch daemon, including concurrent import producing one issue; help catalog entry; scratch check with a fake `gh` on `PATH`. |
| Publish | `afc-164` | `afc-163` | `dibs issue publish` and `--publish` on `issue close`/`issue run` per R-07–R-10 and design "Publish", including the locked/inaccessible source preflight from R-13; tests for preconditions, rendering, marker idempotency, reopen and reclose, and publish failure after a successful close; no secret or local path in rendered comments. |
| MCP and protocol | `afc-169` | `afc-164` | Move import/publish logic into `internal/ghsync` with no behavior change (existing CLI tests unmodified); MCP `import_issue`, `publish_issue`, and `close_issue.publish` per R-14 and design "Shared core, MCP, and protocol"; MCP tests with a fake client; protocol section per R-15 (embedded copy stays identical); `docs/mcp-server-v1.md` tools list and amended constraint; managed `AGENTS.md` block line; SessionStart source label, one-line titles, and conditional guidance per R-16 and design "Claude Code and Codex integration", with tests; `contrib/hooks/README.md` "Work from a GitHub issue" section. |
| End-to-end and release | `afc-165` | `afc-163`, `afc-164`, `afc-169` | README "Work from GitHub Issues" section; owner tags `v0.1.0-rc.4`; `dibs doctor` shows the GitHub CLI check as ok on the test machine and as a warning with `gh` removed from `PATH`; the R-12 real round trips on throwaway issues in `abevz/dibs-sandbox`, using the published binaries: CLI, `dibs-mcp`, one `claude -p` and one `codex exec` session under `issue run --publish --require-complete` whose comments link their PRs, and one `import_issue` from an interactive Codex session; transcript and comment links recorded in `review.md`. |

`afc-163` merged in PR #113 (`6c90a41`) and its issue is closed. `afc-164`
implementation and verification are complete; its PR and owner merge remain.
`afc-169` follows that merge, and `afc-165` follows `afc-169` and the
owner-created rc.4 tag.

## Order

1. Owner approves this packet (records it in `review.md` and on `afc-162`).
2. `afc-163`, then `afc-164`; they share `internal/github`.
3. `afc-169` after `afc-164` merges, so the move into `internal/ghsync`
   happens once, on finished code.
4. `afc-165` after `afc-169` merges. The owner approves and creates the tag; the
   agent does not tag or publish releases.

## Verification budget

Follow the repository `AGENTS.md` "Verification budget". Neither slice is
platform-specific, so no cross-compilation is needed beyond CI. Tests must
not depend on the developer's `gh` login or environment.
