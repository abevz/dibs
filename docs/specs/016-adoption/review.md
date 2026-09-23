# 016 Adoption Review

Status: approved by owner; packet active. afc-137 implementation awaits owner review.

## Packet authoring (`afc-127`)

- Owner approval: approved by Aleksey Bevz on 2026-09-23 (PR #66 @ da3c8b2).
- Scope: README, requirements, design, task map, and this review stub.
- Requirements/design alignment: updated for the owner's decisions; independent
  review evidence belongs on PR #66.
- Owner decisions: the notes dated 2026-09-23, including PR #66 resolutions,
  are recorded in `design.md`; no product decision remains open in this draft.
- Acceptance evidence: packet files and one-to-one child map are reviewable.

## Implementation ledger

### afc-137 — rename with legacy compatibility (owner review pending)

- Scope: `github.com/abevz/dibs` module/imports; `dibs`, `dibsd`, and
  `dibs-mcp` binaries; old command aliases; canonical `DIBS_*` configuration;
  old live DB/socket selection; new service units and explicit switch docs.
  Project key `afc`, issue IDs, external keys, and installed old service unit
  are unchanged. Release publication and tags remain with `afc-128`.
- Focused evidence: `TestCanonicalEnvironmentWinsOverLegacy`,
  `TestLegacyPathsRemainCanonicalUntilMigration`,
  `TestDaemonUsesExistingLegacyDatabaseWithoutCreatingSecondDB`,
  `TestOperatorTokenCanonicalEnvironmentWins`,
  `TestIssueRunExportsLeaseEnvToChild`, and
  `TestBuildInstallLegacyAliasesPreserveStdout` passed. The daemon test uses
  real embedded migrations and verifies that starting against an existing
  legacy DB does not create a second DB.
- Installed-binary proof: `make build-install` under temp HOME
  `/tmp/dibs-install-u_s1kj48` started a scratch `dibsd`; `dibs` created and
  claimed `smoke-1`, then `afctl` created and claimed `smoke-2`. Alias stdout
  matched, deprecation appeared only on stderr, and the scratch new DB was
  used. A local fake-release fixture verified the checksum installer and its
  three old-name symlinks. No live daemon or installed service was restarted.
- Local gates: `gofmt -w cmd internal`, `git diff --check`, shell syntax checks,
  `make build`, `make test`, and `make vet` passed. Lint passed with
  `GOTOOLCHAIN=go1.26.4 make lint` (`0 issues`); the system Go 1.27 toolchain
  cannot be parsed by the locally installed linter built with Go 1.26.3.
- PR #67 CI passed on implementation HEAD `3c03618`; independent read-only
  review found no material issue on that head. Owner review is pending. The
  owner must perform the manual service switch in `docs/operations.md` after
  merge. There is no automatic DB migration or `dibs migrate-paths` in this
  slice; a later explicitly designed migration can move live SQLite/WAL state.
- Owner review follow-up in PR #67: the Linux switch reuses the existing
  `~/.config/af-coordinator/operator.env` through a new `dibsd.service.d`
  drop-in before `dibsd` starts; no token is copied or printed. The health API
  reports only whether the daemon has a token, and `dibs doctor` warns when a
  new daemon has none while legacy token configuration exists. Older daemons
  omit that health field and are not misreported. Launchd has no equivalent
  per-agent `EnvironmentFile`; its manual token handoff is documented.
- The main-only post-merge hook rebuilds and `try-restart`s only an active
  `dibsd`. An active legacy service gets the manual-switch message; inactive
  units are never started. The mock-systemctl shell regression failed against
  the prior hook and passed after the fix, including build/restart failures.
  Focused Go tests, `make build`, `make test`, `make vet`, gofmt, and
  `GOTOOLCHAIN=go1.26.4 make lint` passed. Independent read-only review found
  no material issue, and PR #67 CI passed on follow-up implementation HEAD
  `7584c01`. Owner review remains pending.

## Discovered bugs

### afc-138 — MCP stdio framing (owner review pending)

- The old server waited for LSP `Content-Length` headers, so Claude Code and
  Codex could not complete MCP initialization. Removed that framing because
  there are no known header-framed clients; MCP stdio now uses one compact
  UTF-8 JSON-RPC message per line, ignores blank lines, and gives no response
  to notifications. Protocol output stays on stdout; alias notices and errors
  stay on stderr.
- Regression evidence: raw newline JSON sent through `Server.Run` against a
  scratch daemon with real migrations covers initialize, notification silence,
  tools/list, and a ready-issues tools/call. A separate request exceeds 64 KB.
  Before the fix the protocol test got `missing Content-Length header` and the
  large-line test could not parse the framed response; after the fix
  `go test ./internal/mcp -count=1` passed. `make build`, `make test`,
  `make vet`, gofmt, and `GOTOOLCHAIN=go1.26.4 make lint` passed.
- Installed-binary check: `make build-install` updated
  `~/.local/bin/dibs-mcp`; `dibsd` stayed running as PID 415831 at revision
  `4dd9983`. No daemon restart was performed.
- Claude Code command: `claude mcp add dibs -s user -- dibs-mcp` returned
  `Added stdio MCP server dibs with command: dibs-mcp to user config`.
  `claude mcp list` returned `dibs: dibs-mcp  - ✔ Connected`.
  A read-only tool call using
  `claude -p --no-session-persistence --mcp-config /tmp/afc-138-claude-mcp-config.json --strict-mcp-config --allowedTools mcp__dibs__list_ready_issues --permission-mode dontAsk --max-budget-usd 0.5 --verbose --output-format stream-json 'Call the dibs MCP list_ready_issues tool for project afc. Use no shell or file tools. Report count and first two short IDs.'`
  returned `dibs_server=[{name:dibs,status:connected,source:dynamic}]`,
  `tool_use=mcp__dibs__list_ready_issues`, and `9 ready issues; afc-121,
  afc-120`. The temporary MCP config contained only
  `{"mcpServers":{"dibs":{"command":"dibs-mcp"}}}`.
- Codex: `[mcp_servers.dibs]` was absent from `~/.codex/config.toml`, so this
  exact block was appended (no other config line changed):

  ```diff
  +
  +[mcp_servers.dibs]
  +command = "dibs-mcp"
  ```

  `codex mcp list` returned `dibs dibs-mcp ... enabled Unsupported` (the last
  column is authentication support); `codex mcp get dibs` reported
  `enabled: true`, `transport: stdio`, `command: dibs-mcp`. A read-only
  `codex exec --ephemeral -s read-only -C /home/abevz/github/af-coordinator/afc-138-mcp-stdio -o /tmp/afc-138-codex-mcp-result.txt 'Use only the dibs MCP tool list_ready_issues with project afc. Do not run shell commands or modify files. Report the number of ready issues and first two short IDs.'`
  exposed `mcp: dibs/list_ready_issues started`, then denied the call with
  `MCP tool call requires approval, but approval policy is never`; it returned
  no issue data. Claude Code's allowed read-only call verified the tool result.
  PR CI and owner review are pending.
