# 018 GitHub Import Review

## Owner direction (2026-09-26)

- The owner judged that GitHub integration matters for attracting users and
  chose a thin slice over the full stage 4–5 work in the
  [016 plan](../016-adoption/implementation-plan.md): import by URL and one
  result comment, no attachments store, no source-change tracking. Target
  release: `v0.1.0-rc.4`. The public launch post waits for this slice; a small
  circle of testers can start on rc.3.
- `afc-89`, `afc-90`, and `afc-135` remain the full workflow and attachments
  work and are not closed by this packet.

## Design decisions

- **`gh` instead of a built-in HTTP client.** It reuses the user's auth,
  including private repositories and SSO; dibs stores no GitHub token; the
  daemon remains offline and local-only.
- **No schema change.** Idempotent import uses a lookup by external key plus a
  deterministic create operation ID; exactly-once publish uses a marker in the
  GitHub comment instead of a local ledger.
- **Readiness without a probe repository.** `dibs doctor` checks `gh`
  installation, authentication, and a working authenticated API call
  (`rate_limit`). A public probe repository would prove only connectivity;
  per-repository access is checked by `import` and `publish` (owner request,
  2026-09-26).
- **Accepted gap.** Two simultaneous publishes of the same close can post
  twice. A durable ledger is left to `afc-90`.

## Amendment: MCP and protocol (2026-09-26)

- Owner direction: the GitHub workflow must be part of the agent protocol
  and usable through MCP. MCP moved from out of scope into slice `afc-169`
  (R-14, R-15), and the real round trip now covers `dibs-mcp` as well.
- Verified against the code before writing: `dibs-mcp` is a local stdio
  client (`cmd/dibs-mcp/main.go` wires `internal/client`), so it may call
  `gh` without giving the daemon network access. Import and publish live in
  package `main` today, hence the `internal/ghsync` move. The MCP server
  constraint "thin wrappers over `internal/client`" is amended for these two
  tools only. `dibs protocol` embeds `docs/agent-protocol-v1.md`, and a test
  keeps the two identical.
- MCP `import_issue` requires `project`: a stdio server's working
  directory is chosen by the client and is not a reliable repository target.
- Owner approved this amendment on 2026-09-26 (in session, relayed by the
  agent); `afc-169` may start after `afc-164` merges.

## Approval

- [x] Owner approved this packet on 2026-09-26 (in session, relayed by the
  agent); implementation of `afc-163`–`afc-165` may start.

## Evidence

### afc-164 owner decisions (2026-09-26)

- The CLI-only `hooks complete` marker may carry PR URL, commit SHA, branch,
  and note. Nonempty values override `issue run` launch flags; no flags keep
  the legacy marker. This is an approved extension so an agent can record a
  PR created during its run.
- Publication markers use `issue_closed` event ID, not its second-resolution
  timestamp. Comment list and POST use the fetched issue's `comments_url` to
  tolerate repository transfer or rename.
- Missing `github:` keys stop `issue close --publish` before close and
  `issue run --publish` before claim. After a successful local close, a
  publication failure does not change close success; JSON carries structured
  publication status. Explicit publish fails nonzero when it cannot post.
- `locked` always stops preflight, even if the caller has write access. This
  is an accepted simplification for this slice.
- An explicit publish of an operator-closed issue is outside R-07 for this
  slice. The reader rejects a latest `issue_operator_closed` event so it
  cannot accidentally publish a preceding ordinary close after reopen.
- A close note is used only when its `note_added` event immediately precedes
  the latest `issue_closed` with the same actor and timestamp. The API gives
  notes second-resolution times and no link to the close event, so another
  note by that author in the same second remains ambiguous. Adding `note_id`
  to the close payload is follow-up work for `afc-90`.
- The note is public, line quoted, capped at 2,000 runes with a final `…`.
  Markdown and @mentions remain as written. The branch is also public.
  Exact nonempty values of the three current lease/operator token env vars
  are rejected if present in either field. Close/run also reject their exact
  active lease token even when the parent environment does not hold it; no
  heuristic cleaning is applied.
- `gh api --slurp` first appears in [GitHub CLI 2.48.0](https://github.com/cli/cli/releases/tag/v2.48.0);
  doctor warns for older versions. `cancelled` closes publish; handoffs and
  lease expiry do not.

### afc-164 implementation evidence

- CLI-only `issue publish`, `issue close --publish`, `issue run --publish`,
  extended `hooks complete`, GitHub comment client, and doctor version guard
  were implemented without daemon, API, store, or schema changes.
- Focused tests covered preflight before close/claim, event and note selection,
  marker idempotency across two closes in one second, moved-repository
  `comments_url`, locked/not-found, token rejection, line quoting and rune
  truncation, hook metadata override, JSON result shape, and publish failure
  after successful close for both close and run. Both close/run retained a
  successful exit; explicit publish exited nonzero on the same fake `gh`
  failure.
- `gofmt` and `go build ./...` passed; `go test ./...` passed. Full logs:
  `/tmp/dibs-afc164-full-build.log` and `/tmp/dibs-afc164-full-test.log`.
- A temporary real `dibsd` with isolated HOME, DIBS_DB, DIBS_SOCKET and fake
  `gh` imported `o/r#1`, ran `issue run --require-complete --publish` with
  PR URL, branch and note set by `hooks complete`, then repeated
  `issue publish --json`. The fake GitHub state held one comment with the
  PR, note, and close-event marker; repeat reported `already: true`; local
  issue status was `done`. Scratch state: `/tmp/dibs-afc164.erBTHy`.
- A real GitHub API round trip, release binaries, and owner-created rc.4 tag
  were intentionally not tested here; they belong to `afc-165`.

### afc-163 (implementation in review)

- Owner clarifications: with explicit `--project`, scope follows presence of
  `--repo`; descriptions remain complete because the server has no limit;
  UUIDv5 uses `uuid.NameSpaceURL` and the name
  `dibs:issue-import:<project_id>:<external_key>`.
- Added a CLI-only GitHub client, source parser, import command, and optional
  doctor check. Daemon, API, store, and migrations are unchanged.
- Focused tests: `go test ./cmd/dibs -run 'TestIssueImport|TestImportOperationID|TestMapExitCodeErr'`
  and `go test ./internal/github ./internal/doctor` passed. Full verification:
  `go build ./...` and `go test ./...` passed.
- Scratch daemon with temporary HOME, DIBS_DB, and short DIBS_SOCKET plus fake
  `gh`: new and repeated import, two concurrent imports (one dibs issue),
  closed source rejection and `--allow-closed`, PR rejection, current-checkout
  target resolution, doctor success and all three warning points, and GitHub
  error-class JSON output passed. No test called the real GitHub API.
- Real GitHub round trip, publish, README/protocol updates, tag, and release
  remain in `afc-164`/`afc-165` or owner work as described in tasks.md.
- PR #113 follow-up: current-checkout target resolution now reuses
  `firstuse.SameRegisteredGitDir` and recognizes legacy checkout paths;
  `ParseIssueRef` accepts one trailing slash; typed `github.Error` includes a
  short, one-line `gh` stderr in human and JSON messages. Focused regression
  tests cover all three changes. `go build ./...` and `go test ./...` passed
  after the fix. A scratch daemon recognized a legacy checkout registration
  from a linked worktree and returned the expected JSON import and error.
