# 018 GitHub Import Requirements

## Import

- **R-01 Source reference.** `dibs issue import` accepts a GitHub issue URL
  (`https://github.com/<owner>/<repo>/issues/<n>`, with optional query or
  fragment) or `<owner>/<repo>#<n>`. Pull request URLs, other hosts, and
  malformed references are rejected before any network or daemon call.
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
  of a closed dibs issue that has a GitHub external key as one comment on the
  source issue. Open or in-progress issues and issues without a GitHub key are
  rejected without a network call.
- **R-08 Content.** The comment states the dibs short ID, resolution, closing
  note, and the PR URL, commit, and branch recorded at close when present. It
  contains no lease tokens, operator tokens, local paths, or host names.
- **R-09 Exactly once.** The comment carries a hidden marker tied to the dibs
  issue and its close. Repeating the publish for the same close posts nothing
  and reports the existing comment. Reopening and closing again allows one new
  comment for the new close.
- **R-10 Close integration.** `dibs issue close` and `dibs issue run` accept
  `--publish`. Publication happens only after the local close succeeds. A
  publish failure never undoes or blocks the local close: the command reports
  the failure and the exact retry command, and JSON output includes the
  publish result.
- **R-13 Readiness checks.** `dibs doctor` reports a "GitHub CLI" check at
  warning level (dibs works without GitHub): `gh` is on `PATH` with its
  version, it is authenticated for `github.com`, and an authenticated
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
- **R-12 Evidence.** A real GitHub issue with a screenshot in its body
  completes import, `issue run`, close with `--publish`, a repeated import, a
  repeated publish, and a publish retry after a simulated `gh` failure, using
  the released `v0.1.0-rc.4` binaries. README and the agent protocol document
  the workflow.
