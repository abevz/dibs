# MCP Server v1

`dibs-mcp` is a stdio MCP wrapper over the daemon API. It is a client of the
unix-socket HTTP API and never talks to SQLite directly.

## Launch

```bash
DIBS_SOCKET=~/.local/state/dibs/dibsd.sock \
DIBS_ACTOR=codex-1234 \
dibs-mcp
```

`DIBS_ACTOR` is optional for read-only tools, but mutating tools use
it as the default actor/holder/author when the request does not pass one.

## Exposed tools

- `health`
- `get_issue`
- `list_ready_issues`
- `claim_issue`
- `heartbeat_issue`
- `handoff_issue`
- `add_note`
- `list_notes`
- `list_issue_events`
- `close_issue`
- `operator_close_issue`
- `operator_reopen_issue`
- `operator_release_issue`
- `add_tag`
- `remove_tag`

`claim_issue` accepts optional non-secret `session_id` correlation metadata
and returns the daemon-generated `attempt_id` and `version` with the secret
lease token plus the non-secret, issue-local `lease_generation`. A fresh claim
increments the generation; heartbeat does not. Repeating a public holder name
never recovers an active token; the call returns `lease_held`. Claiming
increments the issue's version as a side effect, so
callers must use this returned `version` — not one read earlier via
`get_issue` — as `expected_version` on the eventual close/handoff.
`handoff_issue` requires that active token plus a non-empty `note` beginning
`HANDOFF:`; it invokes the daemon's atomic note-and-release path.
`operator_release_issue` never accepts a lease token; it recovers an issue
stuck `in_progress` because its lease token was lost before TTL expiry,
clearing the lease and returning the issue directly to `open` without a
terminal transition.
`list_ready_issues` accepts an optional `tags` array; an issue must carry
every listed tag to match (AND). `add_tag`/`remove_tag` apply or remove a
namespaced tag (`namespace/value`); `get_issue` and `list_ready_issues`
already surface an issue's `tags` field.

## Design constraints

- tools are thin wrappers over `internal/client`
- daemon API remains the only write authority
- no direct SQLite reads or writes
- no second coordinator protocol beyond the MCP transport wrapper
