# MCP Server v1

`dibs-mcp` is a stdio MCP wrapper over the daemon API. It is a client of the
unix-socket HTTP API and never talks to SQLite directly.

## Stdio transport

Each request is one UTF-8 JSON-RPC object on one line. Responses are compact
JSON-RPC objects followed by `\n`; notifications have no response. Blank input
lines are ignored. Stdout contains protocol messages only; diagnostics and the
deprecated `afc-mcp` alias notice go to stderr. `Content-Length` framing is
not supported.

## Launch

```bash
DIBS_SOCKET=~/.local/state/dibs/dibsd.sock \
DIBS_ACTOR=codex-1234 \
dibs-mcp
```

`DIBS_ACTOR` is optional for read-only tools, but mutating tools use
it as the default actor/holder/author when the request does not pass one.

## Client setup

Install the current binary, then register it with Claude Code:

```bash
make build-install
claude mcp add dibs -s user -- dibs-mcp
claude mcp list
```

For Codex, add this block to `~/.codex/config.toml` only if it does not
already have a `[mcp_servers.dibs]` entry, then restart Codex:

```toml
[mcp_servers.dibs]
command = "dibs-mcp"
```

`codex mcp list` confirms the server registration. Ask either client to call
`list_ready_issues` with `project: "afc"` to confirm end-to-end access. Both
clients connect to the daemon through the existing socket; they do not need a
daemon restart when `dibs-mcp` is rebuilt.

## Exposed tools

- `health`
- `get_issue`
- `list_ready_issues`
- `create_issue`
- `claim_issue`
- `heartbeat_issue`
- `release_issue`
- `handoff_issue`
- `add_note`
- `list_notes`
- `list_issue_events`
- `update_issue`
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

## Retrying lifecycle mutations

`create_issue`, `claim_issue`, `heartbeat_issue`, `release_issue`,
`update_issue`, `handoff_issue`, and `close_issue` accept an optional
`operation_id` (8–128 printable ASCII characters). Supply and retain one ID
for each logical mutation before the first call, then reuse that ID with the
same arguments after an uncertain response. Exact replay returns the original
outcome; changed arguments or reuse for another operation return
`idempotency_conflict`. For `update_issue`, retain the original numeric
`expected_version` on retry. A new ID is a new logical mutation and must
meet the current lease, version, and state checks.

When omitted, the MCP server generates a UUID for each call and includes it
as `operation_id` in the result or tool-error `structuredContent`. That ID
can be reused if the caller received the response. A caller that might lose
the entire MCP response must generate and persist its own ID before sending.
Claim operation IDs can recover lease tokens, so store them privately. The
daemon remains the only mutation authority; MCP does not retry automatically.

An exact heartbeat replay returns the original `expires_at` even after the
lease was released, expired, or replaced. It is historical evidence, not
proof of current ownership. After resolving an ambiguous heartbeat with the
same ID, issue a heartbeat with a **new** ID before continuing work; stop the
child if that fresh heartbeat returns `lease_expired` or cannot prove ownership
before the last known deadline. Responses have no `replayed` field; callers
know whether they reused their ID. See the decision table in
`docs/agent-protocol-v1.md`. MCP is the secondary interface for shell-less
clients; shell-capable agents should use `dibs issue run` so lease tokens stay
out of argv.

## Design constraints

- tools are thin wrappers over `internal/client`
- daemon API remains the only write authority
- no direct SQLite reads or writes
- no second coordinator protocol beyond the MCP transport wrapper
