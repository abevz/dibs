# API curl Examples

The `dibs` daemon exposes a local HTTP API over a Unix domain socket. This allows any standard HTTP client (like `curl`) to query and mutate state without using the `dibs` CLI.

> **Note**: For `curl`, use the `--unix-socket` flag. The base URL hostname is ignored by curl when using a Unix socket, so `http://localhost` is conventionally used.

## Configuration

Set your socket path as an environment variable to make copying these examples easier:

```bash
export DIBS_SOCK=~/.local/state/dibs/dibsd.sock
export DIBS_ACTOR="my-curl-script"
```

---

## 1. Diagnostics & Health

### Check daemon health
```bash
curl -s --unix-socket $DIBS_SOCK http://localhost/v1/health | jq
```

### Read execution statistics
The report is read-only. `since` accepts RFC 3339 or a positive Go duration;
`until` accepts RFC 3339 and defaults to the daemon clock.
```bash
curl -s --unix-socket $DIBS_SOCK \
  "http://localhost/v1/stats?project=afc&since=24h" | jq '.report'
```

The response reports current inventory and coverage together with windowed
flow metrics. Inspect `data_quality` before treating legacy events as causal
history; report data never includes lease tokens or note bodies.

---

## 2. Reading Issues

### List all issues
Supports query parameters: `?project=afc&status=open&type=bug&tag=area/frontend`
```bash
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues?project=afc&status=open" | jq

# Only bugs:
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues?project=afc&type=bug" | jq

# Only issues carrying both tags (repeated tag= is ANDed):
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues?tag=area/frontend&tag=theme/dark" | jq
```

### Get a single issue by Short ID
```bash
curl -s --unix-socket $DIBS_SOCK http://localhost/v1/issues/afc-15 | jq
```

Dependencies in the issue payload expose UUID and short ID explicitly:

```json
{
  "issue": {
    "id": "9342e277-7d81-4eca-bad2-b31bccc67c86",
    "short_id": "afc-15",
    "dependencies": [
      {
        "issue_id": "9342e277-7d81-4eca-bad2-b31bccc67c86",
        "issue_short_id": "afc-15",
        "depends_on_id": "9f03193f-3044-42a2-ae9c-a4756ec1f78d",
        "depends_on_short_id": "afc-12",
        "kind": "blocks"
      }
    ]
  }
}
```

### List "ready" issues
Returns actionable issues that are not leased and not blocked by unfinished
`blocks` dependencies.
```bash
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues/ready?project=afc" | jq

# Scope repository lookup by project when logical names are reused across projects:
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues/ready?project=afc&repo=main" | jq

# Gate the ready view to a factory-routing tag (repeated tag= is ANDed):
curl -s --unix-socket $DIBS_SOCK "http://localhost/v1/issues/ready?project=afc&tag=exec/auto" | jq
```

If you do not provide `project`, use the repository UUID rather than an
ambiguous logical name.

---

## 3. Creating & Modifying Issues

> **Important**: Mutating endpoints require an `actor` field to ensure audit traceability. When modifying existing entities, `expected_version` is required for optimistic concurrency control.

### Create a new issue
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "project": "afc",
    "scope_kind": "project",
    "issue_type": "bug",
    "title": "Investigate database locks",
    "description": "We are seeing intermittent SQLite locks.",
    "acceptance_criteria": "- Root cause identified\n- Repro no longer locks under concurrent writers",
    "priority": 2,
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues | jq
```

### Update an issue (PATCH)
Use this to update descriptions, acceptance criteria, titles, or priorities. If the issue is `in_progress`, you must also provide the `lease_token`.
```bash
curl -s -X PATCH --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "description": "Updated context based on new logs.",
    "expected_version": 1,
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15 | jq
```

---

## 4. The Agent Lifecycle (Claim, Heartbeat, Close)

### Claim an issue
Claiming transitions the issue to `in_progress`, returns a secret `lease_token`,
a safe `attempt_id` for correlating lifecycle events, an issue-local monotonic
`lease_generation`, and `version`.
`session_id` is optional non-secret caller correlation metadata.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "holder": "'"$DIBS_ACTOR"'",
    "ttl_seconds": 3600,
    "session_id": "codex-session-20260713"
  }' \
  http://localhost/v1/issues/afc-15/claim | jq
```

*Extract the token and generation from the response, e.g.,
`export TOKEN="uuid-from-response"` and `export LEASE_GENERATION=1`.
Claiming increments the issue's version as a side effect — use the `version`
from this response, not one read earlier, as `expected_version` on the
eventual close/handoff.*

### Heartbeat (Renew Lease)
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "lease_token": "'"$TOKEN"'",
    "ttl_seconds": 3600
  }' \
  http://localhost/v1/issues/afc-15/heartbeat | jq
```

### Hand off an issue (Back to Open)
Records the required next-step note and releases the lease in one transaction.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "lease_token": "'"$TOKEN"'",
    "note": "HANDOFF: review the current branch, run go test ./..., then continue from the latest issue note."
  }' \
  http://localhost/v1/issues/afc-15/handoff | jq
```

### Bare release (Recovery)
Releases the lease without a note. Reserve it for recovery and compatibility;
normal agent stops should use the atomic HANDOFF endpoint above.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "lease_token": "'"$TOKEN"'"
  }' \
  http://localhost/v1/issues/afc-15/release | jq
```

### Close an issue
Resolves agent-owned work (e.g., `done`, `cancelled`). Requires an active
matching lease and a final note.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "resolution": "done",
    "branch": "codex/afc-27",
    "pr_url": "https://github.com/abevz/dibs/pull/27",
    "commit_sha": "ba6d011",
    "expected_version": 2,
    "lease_token": "'"$TOKEN"'",
    "actor": "'"$DIBS_ACTOR"'",
    "note": "Fixed the locking issue by adjusting WAL parameters."
  }' \
  http://localhost/v1/issues/afc-15/close | jq
```

### Operator-close an unclaimable epic
This explicit local-operator path requires a reason and version, and has no
lease-token field.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "resolution": "done",
    "expected_version": 3,
    "actor": "'"$DIBS_ACTOR"'",
    "reason": "all child issues are complete"
  }' \
  http://localhost/v1/issues/afc-10/operator-close | jq
```

### Operator-reopen terminal work
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "expected_version": 4,
    "actor": "'"$DIBS_ACTOR"'",
    "reason": "new evidence requires follow-up"
  }' \
  http://localhost/v1/issues/afc-10/operator-reopen | jq
```

### Operator-release a stuck in_progress claim
Recovers an issue whose lease token was lost before TTL expiry (e.g. a
script crashed right after claiming). Only accepts an `in_progress` issue,
never a lease token, and returns the issue directly to `open` without a
terminal transition.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "expected_version": 2,
    "actor": "'"$DIBS_ACTOR"'",
    "reason": "flaky-script crashed before persisting the lease token"
  }' \
  http://localhost/v1/issues/afc-10/operator-release | jq
```

---

## 5. Notes, Dependencies & Links

### Add a note to an issue
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "body": "Found a workaround for the bug.",
    "author": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15/notes | jq
```

### Read notes
```bash
curl -s --unix-socket $DIBS_SOCK http://localhost/v1/issues/afc-15/notes | jq
```

### Add a dependency (Blocker)
Mark `afc-15` as blocked by `afc-12`.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "depends_on": "afc-12",
    "kind": "blocks",
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15/dependencies | jq
```

### Add a dependency (Parent)
Mark `afc-15` as a child of `afc-10`.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "depends_on": "afc-10",
    "kind": "parent",
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15/dependencies | jq
```

### Add a link (Artifact)
Link an external document, URL, or local path to the issue.
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "artifact": "https://github.com/my/repo/pull/1",
    "relation": "implements",
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15/links | jq
```

### Read Audit Events
Get the full chronological audit trail of an issue.
```bash
curl -s --unix-socket $DIBS_SOCK http://localhost/v1/issues/afc-15/events | jq
```

### Add a tag
Apply a namespaced tag (`namespace/value`, closed charset).
```bash
curl -s -X POST --unix-socket $DIBS_SOCK \
  -H "Content-Type: application/json" \
  -d '{
    "tag": "area/frontend",
    "actor": "'"$DIBS_ACTOR"'"
  }' \
  http://localhost/v1/issues/afc-15/tags | jq
```

### Remove a tag
`tag` is a query parameter, not a path segment, since the value contains `/`.
```bash
curl -s -X DELETE --unix-socket $DIBS_SOCK \
  "http://localhost/v1/issues/afc-15/tags?tag=area/frontend&actor=$DIBS_ACTOR"
```

### List tags
Tags are part of the issue payload — read them via `GET /v1/issues/{id}` (see
section 2) or the CLI: `dibs issue tag list afc-15`.

---

## 6. Export

### Stream a normalized JSONL export
Each line is a record envelope with a stable `type` and `payload`.
```bash
curl -s --unix-socket $DIBS_SOCK http://localhost/v1/export/jsonl
```

Example lines:

```json
{"type":"issue","payload":{"id":"...","short_id":"afc-29","title":"JSONL export"}}
{"type":"dependency","payload":{"issue_id":"...","issue_short_id":"afc-29","depends_on_id":"...","depends_on_short_id":"afc-24","kind":"parent","created_at":"2026-07-08T16:07:00Z"}}
{"type":"reference","payload":{"issue_id":"...","issue_short_id":"afc-29","artifact_id":"...","artifact_path":"docs/specs/005-aion-forge-integration-readiness/tasks.md","artifact_kind":"tasks","relation":"implements","link_created_at":"2026-07-08T16:08:00Z"}}
{"type":"event","payload":{"sequence":42,"id":"...","event_type":"issue_closed","created_at":"2026-07-08T16:08:01Z"}}
```

Event records are emitted in ascending `sequence`, not timestamp or UUID
order. Legacy rows before `event_ordering_enabled` have deterministic ordering
only; tied timestamps do not imply causal order. `GET /v1/stats` derives its
point-in-time report from the same normalized coordinator record families; it
is an envelope response, not another JSONL record type.
