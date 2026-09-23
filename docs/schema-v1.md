# Schema v1

## Notes

- SQLite dialect
- `version` is incremented on each update
- all timestamps are RFC 3339 UTC text: `YYYY-MM-DDTHH:MM:SSZ`
  - this format sorts lexicographically, so text comparison on `expires_at`
    and `created_at` is correct
  - never mix in unix epoch integers or local-time strings
- nullable timestamp columns (`claimed_at`, `closed_at`) use SQL `NULL` when
  unset, never the empty string
- foreign keys must be enabled
- issues and projects are never hard-deleted in v1; terminal states are
  `cancelled` for issues and archival outside the DB for projects, so the
  event log stays intact

## Tables

### projects

`next_issue_seq` backs short id allocation. The daemon increments it inside
the same write transaction that inserts the issue.

Project key rules: must start with a letter, contain only lowercase letters
and digits (no leading, trailing, or double hyphens), max 16 characters. The
key forms the short-id prefix (`<key>-<n>`), so keep it short.

```sql
create table projects (
  id text primary key,
  key text not null unique,
  name text not null,
  description text not null default '',
  next_issue_seq integer not null default 1,
  created_at text not null,
  updated_at text not null
);
```

### repositories

```sql
create table repositories (
  id text primary key,
  project_id text not null references projects(id) on delete cascade,
  logical_name text not null,
  canonical_git_dir text not null,
  default_branch text not null default 'main',
  hosting_kind text not null default '',
  hosting_slug text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(project_id, logical_name)
);
```

### repo_remotes

At most one primary remote per repository, enforced by a partial unique
index (see Indexes).

```sql
create table repo_remotes (
  id text primary key,
  repository_id text not null references repositories(id) on delete cascade,
  remote_name text not null,
  fetch_url text not null,
  push_url text not null default '',
  is_primary integer not null default 0,
  created_at text not null,
  updated_at text not null,
  unique(repository_id, remote_name)
);
```

### worktrees

Non-main worktree rows may be unregistered later, but only when no issue or
artifact still references that worktree. Main worktrees are protected from
unregister/delete operations.

```sql
create table worktrees (
  id text primary key,
  repository_id text not null references repositories(id) on delete cascade,
  absolute_path text not null unique,
  branch text not null default '',
  head_commit text not null default '',
  remote_name text not null default '',
  remote_branch text not null default '',
  is_main integer not null default 0,
  is_ephemeral integer not null default 0,
  last_seen_at text not null,
  created_at text not null,
  updated_at text not null
);
```

### artifact_roots

At most one primary root per repository, enforced by a partial unique index.

```sql
create table artifact_roots (
  id text primary key,
  repository_id text not null references repositories(id) on delete cascade,
  root_path text not null,
  kind text not null default 'sdd',
  is_primary integer not null default 0,
  created_at text not null,
  updated_at text not null,
  unique(repository_id, root_path)
);
```

### artifacts

```sql
create table artifacts (
  id text primary key,
  repository_id text not null references repositories(id) on delete cascade,
  worktree_id text references worktrees(id) on delete set null,
  artifact_root_id text references artifact_roots(id) on delete set null,
  kind text not null,
  relative_path text not null,
  title text not null default '',
  external_key text not null default '',
  status text not null default '',
  created_at text not null,
  updated_at text not null,
  unique(repository_id, relative_path)
);
```

### issues

`short_id` is the human-facing stable id, format `<project_key>-<N>`
(for example `afc-42`). `id` stays an opaque unique key.

There is no `claimed` status. Whether an issue is claimed is derived from
the presence of an unexpired row in `leases`.

Invariant enforced by the daemon: `scope_kind` must match the populated
scope columns (`repository` requires `repository_id`, `worktree` requires
`worktree_id`). If an FK is nulled by `on delete set null`, the daemon
downgrades `scope_kind` accordingly in the same transaction.

`issue_type` classifies the work item (`task` default, `bug`, `feature`,
`epic`, `chore`). Epics are containers: children reference them via a
`parent` dependency; the daemon rejects claims on epics and excludes them
from the ready view. Added in migration `0002_issue_type.sql`.

`acceptance_criteria` holds the checkable conditions for calling the issue
done — the queryable counterpart to the `## Verification` section of an SDD
leaf. It is optional free text (Markdown, typically a bullet list) and is
distinct from `description` so criteria are not smuggled into prose. Added
in migration `0003_acceptance_criteria.sql`.

`external_key` stores a reference owned by an external system such as a mirrored
GitHub/Gitea issue key or a Temporal workflow id. The coordinator keeps this as
reference metadata only; external execution state still lives outside the
coordinator. Added in migration `0004_issue_external_key.sql`.

```sql
create table issues (
  id text primary key,
  short_id text not null unique,
  project_id text not null references projects(id) on delete cascade,
  repository_id text references repositories(id) on delete set null,
  worktree_id text references worktrees(id) on delete set null,
  scope_kind text not null
    check (scope_kind in ('project', 'repository', 'worktree')),
  issue_type text not null default 'task'
    check (issue_type in ('task', 'bug', 'feature', 'epic', 'chore')),
  title text not null,
  external_key text not null default '',
  description text not null default '',
  acceptance_criteria text not null default '',
  status text not null
    check (status in ('open', 'in_progress', 'blocked', 'deferred', 'done', 'cancelled')),
  priority integer not null default 3,
  assignee text not null default '',
  version integer not null default 1,
  lease_generation integer not null default 0 check (lease_generation >= 0),
  claimed_at text,
  closed_at text,
  created_at text not null,
  updated_at text not null
);
```

### issue_artifacts

```sql
create table issue_artifacts (
  issue_id text not null references issues(id) on delete cascade,
  artifact_id text not null references artifacts(id) on delete cascade,
  relation text not null default 'implements',
  created_at text not null,
  primary key (issue_id, artifact_id, relation)
);
```

### issue_tags

Namespaced tags (`namespace/value`) that classify/route an issue without
carrying state — status, lease, and version stay first-class columns on
`issues`. Validated on write (closed charset, length bound, reserved
namespaces rejected); a tag is a plain `(issue_id, tag)` fact with no other
payload. `list`/`ready` filtering ANDs multiple tags via one `EXISTS`
subquery per tag rather than a join, so no row multiplication occurs.

```sql
create table issue_tags (
  issue_id   text not null references issues(id) on delete cascade,
  tag        text not null,
  created_at text not null,
  primary key (issue_id, tag)
);
```

### dependencies

Kinds:

- `blocks`: the only kind that affects ready computation
- `parent`: hierarchy, no ready effect
- `related`: informational link
- `discovered-from`: provenance of follow-up work

The daemon must reject inserts that would create a cycle in the `blocks`
graph (recursive CTE reachability check inside the insert transaction).

```sql
create table dependencies (
  issue_id text not null references issues(id) on delete cascade,
  depends_on_issue_id text not null references issues(id) on delete cascade,
  kind text not null default 'blocks'
    check (kind in ('blocks', 'parent', 'related', 'discovered-from')),
  created_at text not null,
  primary key (issue_id, depends_on_issue_id, kind)
);
```

### leases

An expired lease (`expires_at` in the past) is treated as absent everywhere.
Heartbeats update `expires_at` and `updated_at` only; they do not create
events. `lease_generation` is an issue-local monotonic fencing value: every
fresh claim increments it, while heartbeat does not.
`attempt_id` is a daemon-generated, non-secret identity for the current lease
episode. `session_id` is optional caller-supplied, non-secret correlation
metadata; it never replaces the lease holder or token.

```sql
create table leases (
  issue_id text primary key references issues(id) on delete cascade,
  holder text not null,
  lease_token text not null unique,
  expires_at text not null,
  attempt_id text not null,
  session_id text not null default '',
  lease_generation integer not null check (lease_generation >= 0),
  created_at text not null,
  updated_at text not null
);
```

Migration `0006_lease_attempts.sql` assigns `legacy-<issue_id>` attempt IDs to
leases that existed before attempt telemetry, so later release or close events
remain attributable without fabricating a token.

Migration `0008_lease_generation.sql` assigns generation `1` to an active
pre-upgrade lease and its issue. Issues without an active lease remain at `0`,
so their first fresh claim receives generation `1`.

### operations

```sql
create table operations (
  operation_id     text primary key,
  operation_kind   text not null,
  target_id        text not null,
  actor            text not null default '',
  fingerprint      text not null,
  status           text not null check (status in ('completed')),
  outcome_json     text not null default '{}',
  created_at       text not null,
  retain_until     text not null
);
```

The AFC-SDD-0159 idempotency ledger. One row is one client-identified logical
mutation and the public outcome it committed. Migration
`0009_operation_ledger.sql` creates it; existing databases gain an empty table
and unchanged behavior.

`status` admits only `completed` by design. The row is inserted inside the
mutation's own transaction, so there is no pending state to represent: if the
transaction rolls back the row disappears with the effect it describes, and the
ledger can never report a mutation that did not commit.

`fingerprint` is a SHA-256 over the canonical, length-prefixed request
arguments. A retry matching `operation_id`, `operation_kind`, `target_id`, and
`fingerprint` replays `outcome_json`; any mismatch is `idempotency_conflict`
and mutates nothing.

`outcome_json` for a claim contains that claim's `lease_token`. The table is
therefore never read by any list, issue-read, export, or diagnostic path — an
exact operation replay is the only way to retrieve it. `operation_id` is a
client-generated capability, not an identity: it is never written to events or
returned by issue reads.

`retain_until` is `created_at + 30 days` and marks when a stored outcome
becomes eligible for deletion. No automatic reaper runs yet, so rows persist
until a future cleanup task removes expired ones; growth is bounded by
operation volume.

### notes

```sql
create table notes (
  id text primary key,
  issue_id text not null references issues(id) on delete cascade,
  author text not null,
  body text not null,
  created_at text not null
);
```

An atomic HANDOFF writes its required `HANDOFF:` note under the active lease
holder and releases that lease in one transaction. Its `note_added` event is
allocated before the matching `issue_released` event, whose attempt outcome is
`handoff`; neither event stores the lease token.

### events

The event log is append-only and must survive its subject. `sequence` is the
daemon-assigned canonical causal order; `id` remains the stable public UUID.
The FK uses `on delete set null` so history is never cascaded away;
`payload_json` should carry enough context (issue short id, title) to stay
meaningful.

```sql
create table events (
  sequence integer primary key autoincrement,
  id text not null unique,
  issue_id text references issues(id) on delete set null,
  actor text not null,
  event_type text not null,
  payload_json text not null default '{}',
  created_at text not null
);
```

### Derived statistics

`GET /v1/stats` and `dibs stats` add no tables, migrations, or mutable
analytics state. The read-only report scans the normalized project,
repository, issue, note, artifact-reference, and event records at request
time. Event `sequence` remains the causal ordering source: the global
`event_ordering_enabled` marker separates deterministic-but-legacy history
from exact ordering. Report responses expose that cutoff and do not infer
causal attempt order for events before it.

## Indexes

```sql
create index idx_issues_project_status_priority
  on issues(project_id, status, priority, updated_at);

create index idx_issues_repo_status
  on issues(repository_id, status, updated_at);

create index idx_issues_worktree_status
  on issues(worktree_id, status, updated_at);

create unique index idx_repo_remotes_primary
  on repo_remotes(repository_id) where is_primary = 1;

create unique index idx_artifact_roots_primary
  on artifact_roots(repository_id) where is_primary = 1;

create index idx_artifact_roots_repo_kind
  on artifact_roots(repository_id, kind, root_path);

create index idx_artifacts_repo_kind
  on artifacts(repository_id, kind, relative_path);

create index idx_artifacts_worktree
  on artifacts(worktree_id, kind, relative_path);

create index idx_issue_artifacts_issue
  on issue_artifacts(issue_id, relation);

create index idx_issue_artifacts_artifact
  on issue_artifacts(artifact_id, relation);

create index idx_issue_tags_tag
  on issue_tags(tag);

create index idx_dependencies_issue
  on dependencies(issue_id);

create index idx_dependencies_depends_on
  on dependencies(depends_on_issue_id);

create index idx_leases_expires_at
  on leases(expires_at);

create unique index idx_leases_attempt_id
  on leases(attempt_id);

create index idx_events_issue_sequence
  on events(issue_id, sequence);

create index idx_events_sequence
  on events(sequence);

create index idx_worktrees_repository_path
  on worktrees(repository_id, absolute_path);
```

Migration `0005_event_sequence.sql` copies legacy rows in deterministic
`(created_at, id)` order, which is not retrospective causal evidence. If
legacy rows exist, it then appends the `event_ordering_enabled` system event;
its sequence is the exact-order cutoff. A fresh database has no legacy rows,
so its cutoff is sequence `0`.

## Ready query shape

Conceptually, a ready issue is:

- not `done`, `cancelled`, `deferred`, or `blocked`
- not blocked by an unfinished issue through a `blocks` dependency
- not covered by an unexpired lease

(`in_progress` with an expired lease stays visible: the work is claimable
again.)

This should be implemented in SQL inside the daemon, not in clients.

## Mutation contract

Operations require different levels of proof, not one blanket rule:

| Operation | Requires |
|---|---|
| agent close and edits under an active claim | valid `lease_token` + `expected_version` |
| operator close or reopen | `actor`, `expected_version`, non-empty `reason`; never a lease token |
| metadata edit of an unclaimed issue (title, priority, description, acceptance_criteria) | `expected_version` only |
| append note, link artifact | neither (append-only) |

Common rules:

- if an issue has an unexpired lease, only its holder may use ordinary mutation
  paths; explicit local operator close/reopen remains a deliberate audited
  override
- generic update cannot close or reopen terminal work; use the explicit close
  or operator-reopen path instead
- every successful mutation increments `version` and appends an event row
  in the same transaction
- on version mismatch the daemon returns a conflict and the client rereads

## Short id allocation

Inside the issue-create transaction:

1. `update projects set next_issue_seq = next_issue_seq + 1 where id = ?`
2. read the pre-increment value
3. build `short_id = lower(project_key) || '-' || seq`

Single-writer daemon plus one transaction makes this race-free.

## SDD artifact contract

The schema must support:

- registering repository artifact roots such as `docs/specs/`
- registering concrete spec artifacts such as `requirements.md`, `design.md`,
  `tasks.md`, `review.md`, and ADR files
- linking operational issues to the artifacts they execute or depend on

That preserves the split between:

- SDD as design truth
- coordinator state as execution truth
