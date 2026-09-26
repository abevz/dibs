# 018 GitHub Import Requirements

## Import

- **R-01 Source reference.** `dibs issue import` accepts a GitHub issue URL
  (`https://github.com/<owner>/<repo>/issues/<n>`, with optional trailing slash,
  query, or fragment) or `<owner>/<repo>#<n>`. Pull request URLs, other hosts,
  and malformed references are rejected before any network or daemon call.
- **R-02 Target.** The new dibs issue belongs to the project and repository
  given by `--project`/`--repo`. When `--project` is omitted, the command
  resolves the registered repository for the current Git checkout and uses its
  project, with repository scope; if that is not possible it fails and asks
  for `--project`.
- **R-03 Content.** The dibs title is the GitHub title. The description starts
  with a `Source: <html_url>` line followed by the GitHub body verbatim, so
  links and images remain reachable. Optional `--type`, `--priority`,
  `--acceptance`, and repeated `--tag` apply as in `issue create`.
- **R-04 Stable identity.** The external key is `github:<owner>/<repo>#<n>`
  with owner and repository lowercased. It is the key already used for
  hand-made GitHub mirrors, so existing mirrors are recognized.
- **R-05 Idempotency.** If an issue with that external key already exists in
  the target project (any status), the command creates nothing and reports the
  existing issue (`imported: false` in JSON). Concurrent imports of the same
  source into the same project produce one dibs issue.
- **R-06 Source state.** A closed GitHub issue is not imported unless
  `--allow-closed` is given. Missing `gh`, missing authentication, a missing or
  inaccessible issue, and timeouts produce distinct actionable errors and
  create nothing.

## Publish

- **R-07 Explicit command.** `dibs issue publish <issue-id>` posts the result
  of a dibs issue closed by `issue close` or `issue run` that has a GitHub
  external key as one comment on the source issue. Open or in-progress issues,
  issues without a GitHub key are rejected without a GitHub network call.
  Operator-closed issues are rejected without posting a comment.
- **R-08 Content.** The comment states the dibs short ID, resolution, closing
  note, and the PR URL, commit, and branch recorded at close when present. It
  adds no lease or operator tokens, attempt/session IDs, lease host/PID, or
  local DB, socket, or worktree paths. The closing note and branch are text
  deliberately supplied by the closer and are published as written. If either
  contains the exact current value of `DIBS_LEASE_TOKEN`,
  `DIBS_OPERATOR_TOKEN`, or `AF_OPERATOR_TOKEN`, publication is rejected.
  Close/run also reject their exact active lease token when it came from a
  token file or claim response.
- **R-09 Exactly once.** The comment carries a hidden marker tied to the dibs
  issue and its close. Repeating the publish for the same close posts nothing
  and reports the existing comment. Reopening and closing again allows one new
  comment for the new close.
- **R-10 Close integration.** `dibs issue close` and `dibs issue run` accept
  `--publish`. A missing GitHub external key is rejected before close or claim.
  Otherwise, publication happens only after the local close succeeds. A
  publication failure never undoes the local close: the command reports
  the failure and the exact retry command, and JSON output includes the
  publish result. `dibs hooks complete` may write PR URL, commit SHA, branch,
  and note metadata to its JSON completion marker; nonempty values override
  launch flags on a successful `issue run` close. No flags preserve the old
  completion marker and behavior.
- **R-13 Readiness checks.** `dibs doctor` reports a "GitHub CLI" check at
  warning level (dibs works without GitHub): `gh` is on `PATH` with its
  version (at least 2.48.0 for `gh api --slurp`), it is authenticated for `github.com`, and an authenticated
  `gh api rate_limit` call succeeds, showing remaining requests. Each failed
  step names its remedy (`install gh`, `gh auth login`, network or token
  problem). Before posting, `publish` confirms that the source issue is
  reachable and not locked, and reports `locked` or `not_found` without
  posting.

## Safety and documentation

- **R-11 Trust boundary.** Imported titles and bodies are task data. They
  cannot grant ownership, bypass leases, or authorize operator actions; the
  agent protocol says so. The daemon performs no network access; only the CLI
  calls `gh`.
- **R-14 MCP tools.** `dibs-mcp` exposes `import_issue` (same inputs and
  result as `dibs issue import --json`, except that `project` is required
  because the MCP process working directory is not a reliable target) and
  `publish_issue` (same result as `dibs issue publish --json`).
  `close_issue` accepts an optional `publish` boolean with the R-10 semantics:
  publication runs only after a successful close, and a publish failure is
  reported in the result without failing the close. Tool descriptions state
  that imported text is untrusted task data and that `publish` posts
  publicly to GitHub. CLI and MCP share one implementation, so their behavior
  and error codes match.
- **R-15 Protocol.** `docs/agent-protocol-v1.md`, and therefore
  `dibs protocol`, gains a "Working from GitHub issues" section. It covers
  import by URL or `owner/repo#N`, working the imported issue through the
  normal claim/run lifecycle, publishing with `--publish`, `issue publish`,
  or MCP `publish`, recording PR/commit/branch with `dibs hooks complete`
  inside `issue run`, retrying a failed publish, the R-11 trust rule, and
  the `gh` prerequisite shown by `dibs doctor`. `docs/mcp-server-v1.md` lists
  the new tools. The managed `AGENTS.md` block names the section in one line.
- **R-12 Evidence.** A real GitHub issue with a screenshot in its body
  completes import, `issue run`, close with `--publish`, a repeated import, a
  repeated publish, and a publish retry after a simulated `gh` failure, using
  the released `v0.1.0-rc.4` binaries. A second real issue completes import,
  claim, close with `publish`, and a repeated `publish_issue` through
  `dibs-mcp`. README and the agent protocol document
  the workflow.
