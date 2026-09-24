# API v1

HTTP+JSON over the unix socket. The daemon is the single write authority;
this contract is the product surface. `dibs` and agent wrappers are thin
clients over these endpoints.

Test shape:

```text
curl --unix-socket ~/.local/state/dibs/dibsd.sock \
  http://localhost/v1/health
```

## Implementation layout

The API stack is intentionally thin and split into seven layers:

- daemon entrypoint: `cmd/dibsd/main.go`
- route registration and JSON helpers: `internal/api/daemon.go`,
  `internal/api/errors.go`
- endpoint handlers: `internal/api/projects.go`, `repos.go`, `worktrees.go`,
  `artifacts.go`, `issues.go`, `stats.go`
- read-only execution-statistics aggregation: `internal/report`
- typed client over the unix socket: `internal/client/client.go`
- API-facing store boundary: `internal/store`
- persistence and most business rules: `internal/store/sqlite/*.go`

The effective call path is:

```text
dibs or curl
  -> internal/client (for dibs)
  -> Unix socket HTTP API
  -> internal/api handlers
  -> internal/store.CoordinatorStore
  -> internal/store/sqlite
  -> SQLite
```

## Conventions

- all bodies are JSON
- all timestamps are RFC 3339 UTC (`YYYY-MM-DDTHH:MM:SSZ`)
- issues are addressable by `short_id` (`afc-42`) everywhere an
  `{issue_id}` appears
- mutating requests carry `actor` (client-asserted identity, see
  architecture doc)
- list endpoints accept query filters, not fixed views

## Error taxonomy

Errors use one envelope:

```json
{
  "error": {
    "code": "version_conflict",
    "message": "expected version 3, current version is 5"
  }
}
```

| HTTP | code               | meaning                                            |
|------|--------------------|----------------------------------------------------|
| 400  | `validation_failed`| malformed body, unknown status, bad scope          |
| 404  | `not_found`        | unknown project/repo/worktree/artifact/issue       |
| 409  | `version_conflict` | `expected_version` does not match current version  |
| 409  | `lease_held`       | another holder has an unexpired lease              |
| 409  | `issue_not_ready`  | status/type/dependencies make the issue ineligible  |
| 409  | `already_linked`   | artifact is already linked to the issue            |
| 409  | `idempotency_conflict` | `operation_id` reused with a different request |
| 409  | `short_id_taken`   | an issue with this short_id already exists         |
| 410  | `lease_expired`    | supplied `lease_token` is expired or unknown       |
| 422  | `dependency_cycle` | a `blocks` edge would create a cycle               |
| 500  | `internal_error`   | internal daemon/database failure                   |

Clients handle `version_conflict` by rereading and retrying;
`lease_held` by backing off or picking other ready work;
`issue_not_ready` by rereading dependencies and picking ready work;
`lease_expired` by re-claiming;
`idempotency_conflict` by using a fresh `operation_id` for a genuinely new
request, never by retrying the same one.

## Health

- `GET /healthz` — liveness, also `GET /v1/health`

## Endpoint map

This is the compact route-to-implementation inventory for the current daemon.

### `internal/api/projects.go`

- `POST /v1/projects` -> `handleCreateProject` -> `sqlite.CreateProject`
- `GET /v1/projects` -> `handleListProjects` -> `sqlite.ListProjects`

### `internal/api/repos.go`

- `POST /v1/repos` -> `handleCreateRepo` -> `sqlite.CreateRepo`
- `GET /v1/repos?project=` -> `handleListRepos` ->
  `sqlite.ListReposByProjectKey` / `sqlite.ListRepos`

### `internal/api/worktrees.go`

- `POST /v1/worktrees` -> `handleRegisterWorktree` -> `sqlite.UpsertWorktree`
- `GET /v1/worktrees?repo=` -> `handleListWorktrees` ->
  `sqlite.ListWorktrees`
- `DELETE /v1/worktrees/{worktree_id}` -> `handleDeleteWorktree` ->
  `sqlite.DeleteWorktree`

### `internal/api/artifacts.go`

- `POST /v1/artifact-roots` -> `handleCreateArtifactRoot` ->
  `sqlite.CreateArtifactRoot`
- `GET /v1/artifact-roots?repo=` -> `handleListArtifactRoots` ->
  `sqlite.ListArtifactRoots`
- `POST /v1/artifacts` -> `handleCreateArtifact` -> `sqlite.CreateArtifact`
- `GET /v1/artifacts?repo=` -> `handleListArtifacts` ->
  `sqlite.ListArtifacts`

### `internal/api/issues.go`

- `POST /v1/issues` -> `handleCreateIssue` -> `sqlite.CreateIssue`
- `GET /v1/issues/{issue_id}` -> `handleGetIssue` -> `sqlite.GetIssue`
- `GET /v1/issues?...` -> `handleListIssues` -> `sqlite.ListIssues`
- `GET /v1/issues/ready?project=&repo=&tag=` -> `handleListReadyIssues` ->
  `sqlite.ListReadyIssues`
- `POST /v1/issues/{issue_id}/tags` -> `handleAddTag` -> `sqlite.AddTag`
- `DELETE /v1/issues/{issue_id}/tags?tag=&actor=` -> `handleRemoveTag` ->
  `sqlite.RemoveTag`
- `POST /v1/issues/{issue_id}/claim` -> `handleClaimIssue` ->
  `sqlite.ClaimIssue`
- `POST /v1/issues/{issue_id}/heartbeat` -> `handleHeartbeatLease` ->
  `sqlite.HeartbeatLease`
- `POST /v1/issues/{issue_id}/release` -> `handleReleaseLease` ->
  `sqlite.ReleaseLease`
- `POST /v1/issues/{issue_id}/handoff` -> `handleHandoffLease` ->
  `sqlite.HandoffLease`
- `PATCH /v1/issues/{issue_id}` -> `handleUpdateIssue` ->
  `sqlite.UpdateIssue`
- `POST /v1/issues/{issue_id}/close` -> `handleCloseIssue` ->
  `sqlite.CloseIssue`
- `POST /v1/issues/{issue_id}/operator-close` -> `handleOperatorCloseIssue` ->
  `sqlite.OperatorCloseIssue`
- `POST /v1/issues/{issue_id}/operator-reopen` -> `handleOperatorReopenIssue` ->
  `sqlite.OperatorReopenIssue`
- `POST /v1/issues/{issue_id}/operator-release` -> `handleOperatorReleaseIssue`
  -> `sqlite.OperatorReleaseIssue`
- `POST /v1/issues/{issue_id}/dependencies` -> `handleAddDependency` ->
  `sqlite.AddDependency`
- `DELETE /v1/issues/{issue_id}/dependencies/{depends_on}?kind=` ->
  `handleRemoveDependency` -> `sqlite.RemoveDependency`
- `POST /v1/issues/{issue_id}/links` -> `handleLinkArtifact` ->
  `sqlite.LinkArtifact`
- `DELETE /v1/issues/{issue_id}/links?artifact=&relation=&actor=` ->
  `handleUnlinkArtifact` -> `sqlite.UnlinkArtifact`
- `GET /v1/issues/{issue_id}/links` -> `handleListIssueLinks` ->
  `sqlite.ListIssueLinks`
- `POST /v1/issues/{issue_id}/notes` -> `handleCreateNote` ->
  `sqlite.CreateNote`
- `GET /v1/issues/{issue_id}/notes` -> `handleListNotes` ->
  `sqlite.ListNotes`
- `GET /v1/issues/{issue_id}/events` -> `handleListEvents` ->
  `sqlite.ListEvents`
- `GET /v1/events?since=&limit=&wait_ms=` -> `handleWatchEvents` ->
  `sqlite.ListGlobalEvents`

### `internal/api/stats.go`

- `GET /v1/stats?project=&repo=&since=&until=` -> `handleStats` ->
  `report.Build` over the API-facing read-only store contract

## Registry

- `POST /v1/projects` — create project (`key`, `name`, `description`);
  key must start with a letter, contain only lowercase letters and digits (no
  leading/trailing/double hyphens), max 16 characters
- `GET  /v1/projects` — list
- `POST /v1/repos` — register repository (`project`, `logical_name`,
  `canonical_git_dir`, `default_branch`, remotes)
- `GET  /v1/repos?project=` — list
- `POST /v1/worktrees` — register/update worktree by `absolute_path`
  (upsert: re-registration refreshes branch, HEAD, `last_seen_at`)
- `GET  /v1/worktrees?repo=` — list
- `DELETE /v1/worktrees/{worktree_id}` — unregister one worktree record;
  only allowed for non-main worktrees with no remaining issue or artifact
  references
- `POST /v1/artifact-roots` — register artifact root (`repo`, `root_path`,
  `kind`)
- `GET  /v1/artifact-roots?repo=` — list registered artifact roots
- `POST /v1/artifacts` — register artifact (`repo`, `relative_path`,
  `kind`, `title`). Performs an upsert: if the artifact already exists by
  `(repo, relative_path)`, updates `title` and `kind` without changing ID.
- `GET  /v1/artifacts?repo=` — list

## Statistics

- `GET /v1/stats?project=&repo=&since=&until=` — a versioned, read-only
  execution-flow report in a `{ "report": ... }` envelope. `project` is a
  project key; `repo` is a repository UUID or logical name (names must be
  unambiguous without `project`); `since` accepts RFC 3339 or a positive Go
  duration such as `24h`; `until` accepts RFC 3339 and defaults to the daemon
  clock. Invalid or inverted windows return `validation_failed`.
- Inventory, ready, in-progress, note coverage, and spec-link coverage are
  current snapshots of the selected scope. Created and transition metrics use
  the inclusive `[since, until]` window; an omitted `since` includes retained
  history. SCM close-metadata coverage uses each issue's latest terminal close
  in that window.
- Percentiles are seconds, use nearest-rank selection, and always include
  `sample_size`. Ratios include `numerator` and `denominator`; a zero
  denominator reports a zero ratio rather than an invented percentage.
- `data_quality.exact_ordering_from_sequence` is the global
  `event_ordering_enabled` cutoff. Events before it have deterministic legacy
  display order only, so `legacy_events_included` is true whenever the scoped
  time window contains one. The endpoint derives directly from coordinator
  records and does not create rollup tables or contact a telemetry service.
- The report is system-flow evidence, not an agent leaderboard or productivity
  score. Lease tokens, note bodies, and other secrets are never included.

## Issues

- `POST /v1/issues` — create; daemon allocates `short_id`; body includes
  `project`, `scope_kind`, optional `repo`/`worktree`, `title`,
  optional `external_key`, `description`, `acceptance_criteria`, `priority`, `issue_type`
  (`task` default, `bug`, `feature`, `epic`, `chore`). An optional
  `operation_id` uses the durable operation ledger: the same ID and canonical
  request return the original issue and short ID, including after later issue
  changes or daemon restart, without a second issue, event, or sequence
  increment. The fingerprint binds project, actor, and all issue fields;
  defaults and tag order are normalized. A changed request or operation kind
  returns `idempotency_conflict` (409). Omitting the ID preserves legacy
  create behavior. The caller must retain the ID before sending the request
  to recover from an ambiguous timeout.
- `GET  /v1/issues/{issue_id}` — fetch one, including current lease if any
- dependency payloads inside issue responses use explicit identity fields:
  `issue_id`, `issue_short_id`, `depends_on_id`, `depends_on_short_id`
- `GET  /v1/issues?project=&repo=&worktree=&status=&assignee=&type=&external_key=&tag=` — query.
  `project`, `status`, `type`, and `tag` accept one value, comma-separated values,
  or repeated keys (for example, `project=afc,aion` is equivalent to
  `project=afc&project=aion`). Values within `project`, `status`, and `type`
  are ORed; `tag` values are ANDed (an issue must carry every listed tag);
  supplied filters are ANDed with each other. Surrounding whitespace is
  trimmed and empty CSV elements return `validation_failed`; every `type`
  value must be a public issue type.
- `GET  /v1/events?since=&limit=&wait_ms=` — global cursor-paginated event
  stream ordered by `event.sequence`; `since` is an opaque `v2` cursor returned
  as `next_since`, `limit` defaults to 100 and is capped at 500, and `wait_ms`
  enables bounded long-poll up to 30000 ms. During the compatibility window,
  the daemon also resolves an existing `v1` `(created_at, id)` cursor to its
  migrated sequence; malformed or unknown cursors return `validation_failed`.
- `GET  /v1/issues/ready?project=&repo=&tag=` — computed ready view; excludes
  epics (they are containers, not units of work). When `repo` is given
  alongside `project`, repository logical-name resolution is scoped to that
  project; without `project`, prefer repository UUIDs over ambiguous names.
  `tag` is repeatable; multiple values are ANDed, same as `GET /v1/issues`.
- `PATCH /v1/issues/{issue_id}` — edit metadata and non-terminal routing
  (`title`, `issue_type`, `external_key`, `description`,
  `acceptance_criteria`, `priority`, `assignee`, `status`); requires
  `expected_version`, plus `lease_token` if the issue is claimed. It cannot
  close or reopen an issue.
- `POST /v1/issues/{issue_id}/close` — agent close; requires an active,
  matching `lease_token` and `expected_version`. Body: `resolution` (`done`
  | `cancelled`), optional `branch`, `pr_url`, `commit_sha`, and optional
  `note` (appends note and closes atomically). Repeated, stale, expired, and
  unleased closes fail. The response echoes the structured close metadata and
  `closed_at`; when the issue already carries an `external_key`, the close
  response and `issue_closed` event include it too.
- `POST /v1/issues/{issue_id}/operator-close` — explicit local-operator close
  for unclaimable epics or administrative resolution. Requires
  `resolution`, `expected_version`, `actor`, and non-empty `reason`; it never
  accepts a lease token and emits `issue_operator_closed` with the source and
  target status.
- `POST /v1/issues/{issue_id}/operator-reopen` — explicit local-operator path
  for reopening `done` or `cancelled` work. Requires `expected_version`,
  `actor`, and non-empty `reason`; it never accepts a lease token and emits
  `issue_reopened` with the source and target status.
- `POST /v1/issues/{issue_id}/operator-release` — explicit local-operator
  recovery path for an issue stuck `in_progress` because its lease token was
  lost before TTL expiry. Requires `expected_version`, `actor`, and
  non-empty `reason`; it never accepts a lease token, only accepts an
  `in_progress` issue, clears the lease, and returns the issue directly to
  `open` (no terminal transition) via `issue_operator_released`.
- `POST /v1/issues/{issue_id}/tags` — apply a namespaced tag. Body: `tag`
  (`namespace/value`, closed charset, reserved-namespace rejected),
  `actor`. Emits `issue_tagged`; `409 already_tagged` on duplicate.
- `DELETE /v1/issues/{issue_id}/tags?tag=&actor=` — remove a tag (query
  params, not a path segment, since the tag value contains `/`). Emits
  `issue_untagged`; `404 not_found` if the issue does not carry the tag.

## Leases

- `POST /v1/issues/{issue_id}/claim` — body: `holder`, `ttl_seconds`, and
  optional non-secret `session_id`; returns `lease_token`, `expires_at`,
  daemon-generated `attempt_id`, issue-local `lease_generation`, and `version`.
  Every fresh claim increments `lease_generation`; heartbeat does not.
  Claiming increments the
  issue's version as a side effect, so this returned `version` — not one
  read earlier via `GET /v1/issues/{issue_id}` — is what a caller must use
  as `expected_version` on the close/handoff that ends this attempt. The
  attempt ID correlates lifecycle events; lifecycle events and issue reads
  expose the non-secret generation, while the lease token remains secret and
  never appears in events or issue reads. Claim fails `lease_held` if any
  unexpired lease exists, including when the caller repeats the same holder
  string; holder attribution never authorizes token recovery. Claim moves an
  eligible issue `open -> in_progress`, rejects epics with `validation_failed`,
  and rejects an issue with an unfinished `blocks` dependency as
  `issue_not_ready`.
  Ready-list output is advisory; claim repeats the shared eligibility predicate
  inside its transaction and is the authoritative decision.

  Claim also accepts an optional `operation_id`: an opaque, high-entropy,
  client-generated idempotency key (dibs uses a UUIDv4). Retrying a claim with
  the same `operation_id` and identical arguments returns the original
  committed response — same `lease_token`, `lease_generation`, `attempt_id`,
  `expires_at`, and `version` — without claiming again and without advancing
  the fencing generation. The outcome is recorded in the claim's own
  transaction, so it survives daemon restart. Reusing an `operation_id` with a
  different target or different arguments returns `idempotency_conflict` and
  performs no mutation. Omitting `operation_id` preserves the previous
  behavior exactly.

  This is **operation retry, not lease recovery**. It answers "what did my
  committed operation do?" using a secret the caller generated before sending
  the request. A caller that never had the `operation_id` gets ordinary
  `lease_held` no matter how exactly it reproduces `holder` or `session_id` —
  those remain attribution, never authentication. To clear a lease whose
  operation ID is genuinely lost, `operator-release` remains the separate,
  audited break-glass path.
- `POST /v1/issues/{issue_id}/heartbeat` — body: `lease_token`,
  `lease_generation`, `ttl_seconds`, optional `operation_id`; extends
  `expires_at`; appends no event. An exact retry returns the original expiry.
- `POST /v1/issues/{issue_id}/release` — body: `lease_token`,
  `lease_generation`, optional `operation_id`; deletes the
  lease, moves `in_progress -> open` unless left `blocked`, and records the
  attempt outcome. Exact replay returns the original 204 without another
  transition. Lazy replacement of an expired lease emits `lease_expired`
  before the next `issue_claimed` event.
- `POST /v1/issues/{issue_id}/handoff` — body: `lease_token`,
  `lease_generation`, `note`, optional `operation_id`; requires
  a non-empty note beginning `HANDOFF:` and atomically records it under the
  active lease holder before releasing the lease. The event sequence is
  `note_added` then `issue_released` with `end_reason: handoff`; lease tokens
  never enter either event payload. Missing, wrong, or expired leases fail with
  `lease_expired` and leave no note or release behind. Exact replay returns the
  original note ID without another note or release.

Heartbeat, release, handoff, `PATCH /v1/issues/{issue_id}`, and ordinary
`POST /v1/issues/{issue_id}/close` accept an optional client-generated
`operation_id` under the claim/create ledger contract above. The same ID,
kind, target, and request returns the original public outcome before checking
the current lease/version/status; changed arguments or reuse across kinds
return `idempotency_conflict` (409). The ledger row and mutation commit
together. A caller must persist the ID and repeat the original arguments,
including `expected_version`, after an ambiguous response. The CLI accepts
`--operation-id` on these commands; omitting it keeps legacy behavior and an
ambiguous outcome then requires a read/reconciliation step before any new
logical action. Automatic retry policy and operator commands are outside this
contract.

## Notes, links, dependencies, events

- `POST /v1/issues/{issue_id}/notes` — append note (`author`, `body`)
- `GET  /v1/issues/{issue_id}/notes` — list
- `POST /v1/issues/{issue_id}/links` — link artifact (`artifact`,
  `relation`); `artifact` can be a UUID or a repository-relative path
- `DELETE /v1/issues/{issue_id}/links?artifact=&relation=&actor=` — remove a
  link; `artifact` is a UUID or repository-relative path, optional `relation`
  narrows to one relation (omit to remove all); `404 not_found` if absent
- `GET  /v1/issues/{issue_id}/links` — list linked artifacts
- `POST /v1/issues/{issue_id}/dependencies` — add dependency
  (`depends_on`, `kind`); rejects `blocks` cycles with `dependency_cycle` and
  any edge whose endpoints belong to different projects with `validation_failed`.
  Supported `kind` values: `blocks` (default), `parent`, `related`, `discovered-from`
- `DELETE /v1/issues/{issue_id}/dependencies/{depends_on}?kind=` — remove
- `GET  /v1/issues/{issue_id}/events` — activity timeline ordered by
  `event.sequence`; every event response includes `sequence`
