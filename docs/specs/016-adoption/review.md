# 016 Adoption Review

Status: approved by owner; packet active. Rename `afc-137` was owner-closed on
2026-09-23 after PR #67 (`4dd9983`). Earlier per-PR pending-review notes below
are historical and do not override that coordinator closure.

## Saved delivery plan — afc-149 (2026-09-25)

- Added [implementation-plan.md](implementation-plan.md) and linked it from the
  packet README and roadmap. It records installation-first delivery, the one-line
  Linux/macOS installer, first use, integrations/watch, attachments, GitHub, and
  independently releasable extensions and promotion.
- Recorded the latest owner constraints: standalone positioning without Beads
  comparisons; no fixed tester-count gate; no Jira instance or committed Jira
  target; optional adapters/plugins remain a later design discussion.
- This is a saved plan, not product implementation or a release approval.
  Existing implementation issue states and publication gates are unchanged.
  Its resume checklist calls out the older packet contracts and live
  dependencies that must be reconciled before the affected work starts.
- Documentation validation and independent review evidence belong to the
  `afc-149` closure record; no application build or runtime test is required
  for this documentation-only change.
- Clarified original packet approval and rename closure after review identified
  stale top-level status text. The live ready view includes `afc-128` and
  `afc-129`; their completed rename prerequisite is retained in the task map.

## Packet authoring (`afc-127`)

- Owner approval: approved by Aleksey Bevz on 2026-09-23 (PR #66 @ da3c8b2).
- Scope: README, requirements, design, task map, and this review stub.
- Requirements/design alignment: updated for the owner's decisions; independent
  review evidence belongs on PR #66.
- Owner decisions: the notes dated 2026-09-23, including PR #66 resolutions,
  are recorded in `design.md`; no product decision remains open in this draft.
- Acceptance evidence: packet files and one-to-one child map are reviewable.

## Implementation ledger

### afc-128 — release installer implementation and preview verification

- The release workflow packages four OS/architecture archives, a checksum
  manifest, a tag-pinned `install.sh`, and a checksum-pinned Homebrew formula.
  Pull requests and manual dispatch build and verify without publishing;
  a tag publishes only after native install checks on Linux amd64/arm64 and
  macOS Intel/Apple Silicon runners pass.
- The installer selects the archive and manifest from the tag embedded in the
  downloaded release asset. It verifies the checksum, installs the three
  binaries without sudo under `~/.local/bin`, prints a PATH hint, and stages
  files before replacing existing binaries. The direct source installer still
  supports `VERSION` for a chosen tag.
- The Apache-2.0 `LICENSE` is included in every platform archive. The shell
  installer places it under the installation prefix's `share/licenses/dibs`;
  the generated Homebrew formula declares the license and installs its text
  in the formula's shared files. The fixture checks repeat installation and
  license delivery; native verification compares the installed copy with the
  archive copy on each platform.
- The first preview uses the `v0.1.0-rc.1` prerelease tag. The publish step
  marks hyphenated versions as prereleases and does not designate them as
  GitHub's latest stable release. The versioned installer URL is the entrypoint
  for this preview.
- PR #87 merged at `bf5436c`, then the owner-approved annotated
  `v0.1.0-rc.1` tag published a GitHub prerelease with four archives,
  `checksums.txt`, `install.sh`, and `dibs.rb`. The tag workflow's native
  archive checks and public-URL installation/first-use smoke passed on Linux
  amd64/arm64 and macOS Intel/Apple Silicon. Evidence:
  [release](https://github.com/abevz/dibs/releases/tag/v0.1.0-rc.1),
  [workflow](https://github.com/abevz/dibs/actions/runs/36167175469).
- A separate Linux amd64 smoke used the published URL in a fresh temporary
  home and Git repository. Install, `dibs init`, create, and claim completed
  in 2 seconds; the installed binary reported revision `bf5436c`, and its
  LICENSE matched the official Apache-2.0 text. Evidence is under
  `/tmp/dibs-v0.1.0-rc.1-2peaac6q/public-smoke/evidence/` (the claim output is
  private and contains a lease token).
- The first published release initially inherited its merge commit message as
  notes because the publish checkout fetched no tag objects at depth one.
  The notes were replaced with the reviewed preview instructions. Future tag
  publication fetches tag objects and checks for an annotated tag before
  deriving release notes from it.
- Local evidence: shell syntax and installer fixture checks passed, including
  exact tag selection despite an ambient `VERSION=latest`, repeat install,
  preserved user data, and refusal of a tampered archive. Four real archives
  built; the packaged Linux amd64 archive installed and launched from a
  temporary home, and a second install succeeded. `go build ./...` and the
  complete `go test ./...` passed with the live operator-token environment
  removed from the test process. The initial unfiltered test run failed only
  in an environment-sensitive config test and a previously observed API
  shutdown timeout; both passed in focused reruns. `actionlint` and Ruby
  formula syntax checks passed. Full local logs are under
  `/tmp/dibs-afc-128-q78uxn1y/`.
- Final staged implementation underwent independent read-only review. A finding
  that manual dispatch on a tag could publish was fixed by requiring a tag push;
  the revised workflow was reviewed again and `actionlint` passed.
- The versioned preview installer can now be advertised. A tested Homebrew tap
  was published at [abevz/homebrew-dibs](https://github.com/abevz/homebrew-dibs).
  Its initial formula is byte-for-byte the `dibs.rb` asset from
  `v0.1.0-rc.1` (SHA-256 `2fd8e9da28f6495150a635f7cc46a106da3a513858f3e12e36b0965ab48a940b`).
  [Tap PR #1](https://github.com/abevz/homebrew-dibs/pull/1) fixed a CI-only
  self-copy after Homebrew's setup action mapped the checkout into the tap.
  The corrected PR and [merged-main run](https://github.com/abevz/homebrew-dibs/actions/runs/36230532596)
  installed and tested `abevz/dibs/dibs` on Linux amd64/arm64 and macOS
  Intel/Apple Silicon. Each job checked all three installed binaries and the
  license. The tap formula and workflow underwent independent read-only review;
  `actionlint` and Ruby syntax checks passed.
- A separate Linux amd64 Go 1.27.1 check installed all three commands with
  `go install ...@v0.1.0-rc.1` into a temporary `GOBIN`; `go version -m`
  reported the tagged module. That command prints `revision unknown` because
  it does not supply the release workflow's revision linker flag. The guide
  documents it as a Go-user path with the Linux-only evidence stated, while
  the tested release installer and Homebrew remain the newcomer paths. AUR
  remains unadvertised. The scratch verification did not restart a live service or
  change a live database.

### afc-129 — first use implementation and preview verification

- `dibs init` now discovers the current Git repository and worktree, shows the
  mapping, starts the companion `dibsd` when needed, reconciles project/repo/
  worktree records through the daemon API, and updates the managed AGENTS.md
  block. Ambiguous project and default-branch cases request explicit flags.
  A dry run does not start a daemon or mutate records/files.
- Non-diagnostic CLI commands start the daemon on demand. `dibs daemon
  start/stop` offers explicit control for a dibs-started process; manager-owned
  services must be stopped through their manager. The daemon's existing database lock
  remains the sole-writer arbiter; first-call starters wait for the same
  healthy socket. Startup failures include the original daemon log reason.
- Local scratch evidence: `init` twice, create, claim, stop, restart, and
  retained issue state passed with installed local binaries. Two concurrent
  `daemon start` calls converged on one socket. An unknown migration failed
  without serving the API and surfaced `unknown applied migration`. The
  four-command local sequence took well under two minutes. An existing legacy
  database was selected after restart, retained its issue, and did not create
  a second database at the new default path. The native smoke starts the first
  daemon through `init` and verifies `daemon stop` refuses a foreground-managed
  process. `go test ./...`, `go build ./...`,
  `shellcheck`, and `actionlint` passed with the live operator-token variables
  removed from the test process.
- PR #86 review found that a reachable socket with unverifiable health could
  receive first-use writes, and that `daemon stop` could finish before the
  database lock was released. The follow-up fails closed on unverifiable
  health and waits for both socket removal and lock release. Focused tests,
  the full Go suite, and the built-binary first-use smoke passed after the fix.
  The first CI rerun exposed an older issue-run mock with no health endpoint;
  the fixture now returns the configured database identity as the real daemon
  does. An uncached `go test -count=1 ./...` passed with that fixture.
- PR #86's final revision received independent review and native first-use CI
  on all four platforms. The published release repeated native first-use smoke
  from its public URL; the separate Linux four-command path took 2 seconds.
  No live daemon or database was restarted.

### afc-144 — coordinated repository guidance

- `cmd/dibs/init-snippet.md` now carries the afc-116 rule: use `dibs issue run`
  for the lifecycle; never put lease tokens on an executed command line.
  Manual lifecycle calls use `DIBS_LEASE_TOKEN` or a private
  `DIBS_LEASE_TOKEN_FILE` instead.
- Refreshed managed blocks with `dibs init` in dibs, budget-tracker,
  vault-bridge, vault-bridge-5, job-scout-bot, utils, platform-iac,
  englishdrills, and hybrid-cloud-optimizer. Updated handwritten `afctl`
  workflow text in utils, platform-iac, and englishdrills. The aion-forge
  guidance belongs to aion-924 and was not changed.
- Verified all nine managed blocks exactly match the current snippet and no
  in-scope AGENTS.md contains `afctl`; `go test ./cmd/dibs -run TestInit`
  passed. `make build` and `go test ./...` passed in budget-tracker.
  `GOTOOLCHAIN=go1.26.4 pre-commit run --all-files` passed in platform-iac;
  the system Go 1.27 toolchain is newer than its installed golangci-lint.
- `hybrid-cloud-optimizer/AGENTS.md` is ignored and explicitly local-only;
  the owner chose a local update without a commit. Tracked changes were pushed
  to the respective repository branches for owner review.

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

### afc-130 — live read-only watch

- Added `dibs watch` with project scope, a one-shot text/JSON snapshot, and an
  interactive board that refreshes every two seconds. It shows ready issues,
  active holders and lease time remaining, active blockers, and recent events.
  A disconnected daemon leaves the last complete snapshot visible as stale;
  terminal resize redraws the board without any claim or mutation.
- The board reads only daemon APIs. `GET /v1/events/recent` seeds the newest
  events and returns a cursor for subsequent `GET /v1/events` updates, so a
  fresh watch does not mislabel old history as recent activity. The CLI does
  not start a stopped daemon when entering watch.
- Focused store, API, client, CLI, and rendering tests passed. An isolated
  daemon/database smoke showed a new issue in `READY` and `RECENT EVENTS` from
  `watch --once`; an interactive terminal opened, refreshed, and quit with `q`.
  `go test ./...` passed with ambient operator-token variables removed;
  `go mod tidy -diff` and `git diff --check` were clean. Independent review
  and CI evidence are recorded on the implementation PR.

### afc-131 — newcomer README

- Rewrote the opening around duplicate task work, the exact public tagline,
  a real two-frame `dibs watch` GIF, and the four-command preview quickstart.
  The caption states that watch is on development `main` and is not included
  in published `v0.1.0-rc.1`. The inspect-before-run path and installation
  guide remain linked from the first screen.
- The GIF uses `watch --project demo --once` output from an isolated daemon and
  database with three demo issues, one blocker, and an actual claim. Only blank
  terminal rows were removed before rendering the two text frames as an
  animated GIF; issue IDs, lease holder, TTL, and events were not fabricated.
- Reconciled R-06, the task map, and design notes with the owner's later
  no-Beads-comparison decision recorded in `implementation-plan.md`.
  Architecture and SDD explanations now live behind links to existing docs.
- Replayed `init`, create, and claim in a fresh temporary Git repository named
  `myapp` with an isolated daemon: init inferred key `myapp`, create returned
  `myapp-1`, and claim succeeded. The published release installation and native
  Linux/macOS checks remain the `afc-128` evidence above. Documentation/link
  validation and independent review are recorded with the implementation PR.

## Wave B integrations

### afc-132 — one-shot Claude Code/Codex hooks (implementation review pending)

- Owner selected one task per `issue run` for RC2. Current Claude Code 2.1.280
  and Codex CLI 0.157.1 expose turn-scoped `Stop`, so SessionStart is read-only
  and completion is explicit through `issue run --require-complete` plus
  `dibs hooks complete`. A successful agent exit without that marker uses the
  existing atomic `HANDOFF:` path. Auto-selection is deferred.
- `dibs hooks install --agent claude|codex` merges a project SessionStart hook
  without replacing unrelated configuration. It pins the executable path so
  an agent login shell with an older `dibs` on PATH uses the intended version.
  The old hook snippets were removed because their schema and script paths
  were stale.
- Focused CLI tests cover config preservation, idempotence, symlink refusal,
  explicit completion, and unfinished handoff. In an isolated daemon/database,
  simultaneous `issue run` calls for one issue returned a successful first
  claim and `lease_held` for the second; the first completed and closed.
  Evidence: `/tmp/afc132-smoke.noUqg6/`.
- Two separate Codex CLI sessions used project hooks and completed distinct
  isolated issues (`demo-2`, then `demo-1`) through the explicit marker;
  both ended `done`. Initial Claude Code attempts hit intentionally low USD
  budgets and correctly handed off without closing. A final Claude Code
  2.1.280 run read `NOTE.txt`, called the pinned `dibs hooks complete`, and
  closed `demo-3` as `done`. Evidence: `/tmp/afc132-agents.p4U4rT/`.
- Independent review found an installer match that could replace a wrapped
  user command. The match now accepts only a standalone generated Dibs
  command; a regression test preserves `cleanup.sh && ...`. Review also
  questioned daemon startup; an isolated stopped-daemon smoke proved
  `hooks session-start` starts `dibsd` and returns the ready context. Evidence:
  `/tmp/afc132-autostart/`.
- The first broad `go test ./...` run was invalidated by an ambient
  `AF_OPERATOR_TOKEN` in the environment. The clean full suite passed with
  `AF_OPERATOR_TOKEN` and `DIBS_OPERATOR_TOKEN` removed; `go vet ./cmd/dibs
  ./internal/watch` and `git diff --check` passed. Independent review is
  required before merging.

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
  PR #68 CI `test` passed on implementation HEAD `ca7207a`. Owner review remains pending.
