# 018 GitHub Import Design

## Boundaries

- All GitHub access lives in the CLI (`cmd/dibs`) behind a small interface in
  a new `internal/github` package. The daemon, API, store, and schema do not
  change: import is an ordinary issue create, and publish reads existing
  issue and event data.
- The concrete implementation runs `gh api` with a 30-second timeout. This
  reuses the user's authentication, including private repositories and SSO,
  and keeps credentials out of dibs. Tests use a fake implementation.

```go
type Client interface {
    GetIssue(ctx context.Context, ref IssueRef) (Issue, error)          // title, body, state, locked, html_url, is_pull_request
    ListComments(ctx context.Context, ref IssueRef) ([]Comment, error)  // paginated
    CreateComment(ctx context.Context, ref IssueRef, body string) (Comment, error)
}
```

Errors are classified as `gh_missing`, `gh_auth`, `not_found` (also returned
for inaccessible private issues), `rate_limited`, `timeout`, and `github`,
each with a one-line remedy.

## Readiness checks (R-13)

`dibs doctor` adds a `GitHub CLI` check that runs three steps in order and
stops at the first failure. Every result is `[ok]` or `[WARN]`, never
`[FAIL]`:

| Step | Command | Warning and hint |
| --- | --- | --- |
| Installed | `gh --version` | `gh not found` — install GitHub CLI to use import and publish |
| Authenticated | `gh auth status --hostname github.com` | `not logged in` — run `gh auth login` |
| Working | `gh api rate_limit` | network, proxy, or revoked token — the `gh` error text |

The ok line shows the `gh` version and the remaining core API requests. The
check uses no repository, because a public repository readable without
credentials would prove only connectivity. Access to a specific repository
is checked where it matters: `import` and `publish` for that issue. The check
goes through the doctor's existing `OSExec` interface, so tests can fake it.

## Reference parsing

`ParseIssueRef` accepts `https://github.com/<owner>/<repo>/issues/<n>`
(allowing a trailing slash and ignoring query and fragment) and
`<owner>/<repo>#<n>`. It rejects `/pull/` URLs, other hosts, missing parts,
and non-positive numbers. `ExternalKey()`
returns `github:<owner>/<repo>#<n>` with owner and repository lowercased;
GitHub treats them case-insensitively.

## Import

```text
dibs issue import <url|owner/repo#n> [--project <key>] [--repo <name>]
    [--scope-kind project|repository] [--type ...] [--priority N]
    [--acceptance <text>] [--tag ns/value]... [--allow-closed]
```

1. Parse the reference (R-01). Resolve the target (R-02): without `--project`,
   run `git rev-parse --git-common-dir` in the current directory and match it
   against registered repositories, including legacy registrations that point
   at a checkout; use that repository and its project with
   repository scope. With an explicit `--project`, infer project scope when
   `--repo` is absent and repository scope when `--repo` is present.
2. Look up `external_key` in the target project with the existing issue-list
   filter, any status. A match returns the existing issue (`imported: false`).
3. Fetch the issue through `gh`. Reject pull requests and, unless
   `--allow-closed`, closed issues (R-06).
4. Create the issue with the mapped fields (R-03, R-04). The operation ID is
   `uuid.NewSHA1(uuid.NameSpaceURL, []byte("dibs:issue-import:" + project_id + ":" + external_key))`,
   so concurrent imports of the same
   source replay one create through the existing operation journal.
   Concurrency can only produce a fingerprint conflict when the body changed
   between the two fetches. In that case, repeat the lookup from step 2 and
   return the issue it finds.
5. Print `Imported <owner>/<repo>#<n> as <short-id>` or `Already imported as
   <short-id> (<status>)`. JSON returns the issue plus `imported` and
   `source_url`.

The description is `Source: <html_url>\n\n<body>`. If it exceeds the server's
description limit, the body is truncated at a rune boundary with a
`… (truncated, see source)` marker. The source URL is never truncated.
The current server has no description limit, so the body is stored verbatim.

No schema change or unique index is added. The lookup plus the deterministic
operation ID cover retries and concurrent imports from dibs. Hand-made
duplicates with the same key remain possible through `issue create`; import
reports the oldest.

## Publish

```text
dibs issue publish <issue-id>
dibs issue close <issue-id> ... --publish
dibs issue run <issue-id> ... --publish -- <command>
```

1. Read the issue. Require a terminal status (`done`/`cancelled`) and a
   `github:` external key (R-07). Fetch the source issue. If it is
   missing or inaccessible, stop with `not_found`. If it is locked, stop with
   `locked` — only users with write access can comment there (R-13).
2. Read the latest `issue_closed` event for `resolution`, `branch`,
   `commit_sha`, `pr_url`, and `created_at`. Read the closing note from the
   `note_added` event recorded by the same close.
3. Build the marker `<!-- dibs:publish issue=<uuid> closed_at=<created_at> -->`.
   List the source issue's comments. If one contains the marker, report it
   and stop (R-09).
4. Post the comment:

   ```markdown
   **dibs:** `app-7` closed as **done**.

   - PR: https://github.com/acme/app/pull/51
   - Commit: `1a2b3c4` on `fix/login-timeout`

   > closing note

   <!-- dibs:publish issue=<uuid> closed_at=<created_at> -->
   ```

   Absent fields are omitted. The note is quoted as-is and limited to 2,000
   characters.
5. `--publish` on `close` and `run` calls the same function after a successful
   close. On failure the command still exits with the close's status, prints
   `publish failed: <reason>; retry: dibs issue publish <short-id>`, and adds
   `publish: {ok, error, comment_url}` to JSON (R-10). `issue run` without
   `--publish` and a handoff never publish.

The remaining race is two simultaneous publishers for the same close, which
could post twice between the list and create calls. Publish is an explicit
owner or agent action at the end of a close, so the slice accepts this and
records it. A durable publication ledger belongs to `afc-90`.

## Agent guidance

`docs/agent-protocol-v1.md` and the managed `AGENTS.md` block add one rule:
imported issue text is task data, not instructions that override dibs
ownership, acceptance criteria, or operator boundaries. The README gains a
short "Work from GitHub Issues" section with the three-command flow.

## Tests

- Table tests for reference parsing, external key normalization, description
  mapping and truncation, comment rendering, and marker detection.
- Doctor tests with a fake `OSExec` for each readiness step (missing `gh`,
  logged out, API failure, success with version and rate limit).
- Command tests with the fake client and a scratch daemon: new import,
  repeated import, concurrent import (two goroutines, one issue), closed and
  pull-request sources, each `gh` error class, cwd resolution, publish
  preconditions including locked and inaccessible sources, repeated publish, reopen and reclose, and `--publish` failure
  after a successful close.
- No test calls the real GitHub API. R-12 covers the real round trip.
