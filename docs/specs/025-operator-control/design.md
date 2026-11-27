# Design and decisions

## Current behavior

Baseline is origin/main at `a28b4fb7c6a8d8ec05871a06cffa657998aea60e`.
The coordinator records afc-187 and afc-188 as done. The clean
`fix/afc-187-188` worktree at `656cf35` is an ancestor of this baseline via
[PR #138](https://github.com/abevz/dibs/pull/138). The proposal uses that
baseline's token-file lifetime rule and holder handoff semantics. It needs no
work from the sibling checkout. afc-181 remains open with no review notes.
Its operator-release and background-work concerns inform this design. Its
broader review stays with afc-181. [Unattended-agent guidance](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5).

| Area | Behavior at the baseline |
| --- | --- |
| Heartbeat and lease | `POST /v1/issues/{issue_id}/heartbeat` resolves the issue, requires a token and positive generation, defaults nonpositive TTL to 3600 seconds, and returns `expires_at` on success. The store updates expiry and heartbeat metadata only for the matching live lease, with expiry calculated from daemon time plus TTL. An exact operation replay returns the historical expiry before checking current ownership. A fresh heartbeat after release, expiry, or replacement returns `lease_expired` (HTTP 410). [internal/api/issues.go:256–305](../../../internal/api/issues.go#L256-L305), [internal/store/sqlite/issues.go:777–819](../../../internal/store/sqlite/issues.go#L777-L819), [internal/store/sqlite/issues.go:944–961](../../../internal/store/sqlite/issues.go#L944-L961). |
| CLI and MCP heartbeats | The shared client decodes only `expires_at` with ordinary JSON decoding. The CLI gets a lease token from its environment when omitted and emits expiry in text or JSON. MCP `heartbeat_issue` takes issue, token, generation, TTL and an optional operation ID, then returns expiry with the operation ID. [internal/client/client.go:379–394](../../../internal/client/client.go#L379-L394), [internal/client/client.go:697–709](../../../internal/client/client.go#L697-L709), [cmd/dibs/cmd_issue.go:770–830](../../../cmd/dibs/cmd_issue.go#L770-L830), [internal/mcp/server.go:394–419](../../../internal/mcp/server.go#L394-L419), [internal/mcp/server.go:990–1005](../../../internal/mcp/server.go#L990-L1005). |
| Managed and manual sessions | `dibs issue run` renews every TTL/3, supplies issue ID, token, generation and attempt ID to its child, and cancels its workload group on ownership loss. A successful run can close the issue. The heartbeat loop treats an exact retry as historical and requires a fresh success before taking a new deadline. Manual token files survive a failed lifecycle request. [cmd/dibs/cmd_issue_run.go:156–266](../../../cmd/dibs/cmd_issue_run.go#L156-L266), [cmd/dibs/cmd_issue_run.go:274–315](../../../cmd/dibs/cmd_issue_run.go#L274-L315), [cmd/dibs/cmd_issue_run.go:397–492](../../../cmd/dibs/cmd_issue_run.go#L397-L492), [docs/agent-protocol-v1.md:33–46](../../../docs/agent-protocol-v1.md#L33-L46). |
| Operator identity | Dibs uses Unix-socket HTTP. The operator release handler requires an exact bearer match to the daemon's configured `DIBS_OPERATOR_TOKEN` and returns 403 for absent configuration or missing/wrong credentials. The CLI resolves an audit actor and supplies the token from the environment. The MCP tool resolves the caller-provided or configured actor and calls the client, but the stock `dibs-mcp` entrypoint installs no operator token. The actor string is a label and the bearer proves possession of a shared secret. [internal/api/daemon.go:40–65](../../../internal/api/daemon.go#L40-L65), [internal/api/issues.go:656–702](../../../internal/api/issues.go#L656-L702), [cmd/dibs/cmd_issue.go:1497–1521](../../../cmd/dibs/cmd_issue.go#L1497-L1521), [internal/client/client.go:666–689](../../../internal/client/client.go#L666-L689), [internal/mcp/server.go:721–742](../../../internal/mcp/server.go#L721-L742), [internal/mcp/server.go:920–931](../../../internal/mcp/server.go#L920-L931), [cmd/dibs-mcp/main.go:16–27](../../../cmd/dibs-mcp/main.go#L16-L27). |
| Operator release | Requires a reason, actor, and matching issue version. In one transaction it opens an `in_progress` issue, clears its claim, increments version, deletes the lease and writes `issue_operator_released` with reason and lease evidence. The next fresh heartbeat using that lease fails. This path records an event and has no handoff-note write. [internal/api/issues.go:667–685](../../../internal/api/issues.go#L667-L685), [internal/store/sqlite/issues.go:1811–1867](../../../internal/store/sqlite/issues.go#L1811-L1867), [internal/store/sqlite/issues.go:1899–1922](../../../internal/store/sqlite/issues.go#L1899-L1922), [internal/store/sqlite/issues.go:793–808](../../../internal/store/sqlite/issues.go#L793-L808), [internal/store/sqlite/issues.go:944–952](../../../internal/store/sqlite/issues.go#L944-L952). |
| Holder handoff | The CLI accepts `--expected-version latest` for handoff and defaults to current state. The store fences by live token and generation, writes the holder's `HANDOFF:` note, removes the lease and returns the issue to open (or preserves blocked) in one transaction. Its operation ID permits retry after a lost response. [docs/agent-protocol-v1.md:237–249](../../../docs/agent-protocol-v1.md#L237-L249), [internal/core/issue.go:524–538](../../../internal/core/issue.go#L524-L538), [internal/store/sqlite/issues.go:992–1109](../../../internal/store/sqlite/issues.go#L992-L1109). |
| Notes | A note has an ID, issue ID, caller-supplied author, body, and creation time. The note API accepts author and body without operator or holder credentials. The store appends the note and `note_added` event together. CLI `issue note add/list` and MCP `add_note/list_notes` expose that path. [internal/core/issue.go:11–24](../../../internal/core/issue.go#L11-L24), [internal/api/issues.go:972–1035](../../../internal/api/issues.go#L972-L1035), [internal/store/sqlite/issues.go:2358–2414](../../../internal/store/sqlite/issues.go#L2358-L2414), [cmd/dibs/cmd_issue.go:1971–2086](../../../cmd/dibs/cmd_issue.go#L1971-L2086), [internal/mcp/server.go:479–501](../../../internal/mcp/server.go#L479-L501), [internal/mcp/server.go:542–556](../../../internal/mcp/server.go#L542-L556). |
| Checkout identity | An issue can carry repo and worktree IDs. Registered worktrees have their own ID, absolute path and repo ID. The lease and heartbeat request carry no execution-checkout ID. First-use discovery already canonicalizes the Git checkout root and common directory. [internal/core/issue.go:100–108](../../../internal/core/issue.go#L100-L108), [internal/core/issue.go:153–176](../../../internal/core/issue.go#L153-L176), [internal/core/issue.go:285–292](../../../internal/core/issue.go#L285-L292), [internal/core/worktree.go:8–22](../../../internal/core/worktree.go#L8-L22), [internal/firstuse/setup.go:34–63](../../../internal/firstuse/setup.go#L34-L63). |

## Proposed scope and authorization

Store durable control state by target kind and ID. A setter chooses exactly
one of `--issue`, `--repo`, `--worktree`, or `--global`. Resolve names to
stable IDs server-side and reject ambiguous or nonexistent targets. A global
setting covers one daemon. Serialize changes, record who set or cleared the
target and when, and assign each change an opaque revision for conditional
acknowledgement. A same-operation retry returns the original result.
`resume` clears that target alone and succeeds if already clear.

Match the claimed issue and its recorded repo/worktree, plus the actual
execution worktree and its repo. Bind the registered execution checkout to
the lease generation at enrollment using a separate holder-authorized
registration step. Use that binding in the heartbeat and live hook read. This covers a project-scoped issue launched in a registered
checkout. Resolve checkout by canonical Git root and registered identity.
Reuse first-use path discovery where applicable and support detached HEAD.
A manual session needs trusted launch configuration with its issue and
generation, or the worktree-only path if it has no claim. Never infer a claim
from the most recent issue in a checkout. Worktree-only sessions also see
repo and global restrictions. Keep the original execution binding when a
session's cwd changes and include additional recognized current-checkout
restrictions on the live read. An unresolved required identity is an error.
Subagents sharing an enrolled issue or checkout inherit its matching
controls, including when their trusted launch context names a separate
registered checkout. Individual subagent targeting has no selector in
this proposal.

Combine restrictions by severity `stop > pause > ask_owner`. At equal
severity prefer issue, then worktree, then repo, then global. Use a stable ID
to break ties within a kind, including when the recorded checkout and
execution checkout differ. Clearing one target recomputes the effective
signal. A broad stop remains effective under a narrower pause.

Use the existing bearer check on every setter and clearer, including MCP
requests. The proposed CLI is `dibs control pause|stop|resume` with the target
flags above and an optional `--reason CODE` for pause and stop. Provide
equivalent typed MCP tools in an explicitly operator-owned invocation.
Supply the token only to that invocation, withhold it from agent MCP
servers, and strip it from supervised child environments. Currently
`issue run` copies the environment except for a short list of legacy lease
keys, so operator credential isolation requires a change.
[cmd/dibs/cmd_issue_run.go:208–215](../../../cmd/dibs/cmd_issue_run.go#L208-L215),
[cmd/dibs/cmd_issue_run.go:499–509](../../../cmd/dibs/cmd_issue_run.go#L499-L509).
For the control contract, `set_by` is the fixed, daemon-assigned role
`operator`. Keep the caller's actor label in the audit trail and attribute
no verified individual identity to it. Environment filtering and socket
permissions alone cannot isolate mutually untrusted processes under the
same OS identity. The operator-only guarantee assumes the helper,
configuration, and credentials are outside the agent's write/read reach as
appropriate. Deployments that permit untrusted agent commands need OS or
sandbox isolation for the operator credential and trusted gate. A
same-UID cooperative deployment provides a workflow guardrail.

## Delivery and compatibility

On a fresh, successful authorized heartbeat, resolve the effective signal
in the lease renewal transaction and return its snapshot alongside the
legacy expiry field. Example:

```json
{
  "expires_at": "2026-11-27T19:15:00Z",
  "control": "pause",
  "reason": "operator_review",
  "set_by": "operator",
  "set_at": "2026-11-27T19:00:00Z"
}
```

Use UTC RFC 3339 for timestamps. Omit all four fields when clear. A
restricted response carries a known reason code, defaulting to
`operator_requested`. Start with a small fixed code table such as
`operator_requested`, `operator_review`, and `maintenance`. Reject arbitrary
reason text, unknown values, and extra request fields. Render messages
using a local lookup, for example `Paused by the operator (review).`
Timing and the fixed setter role are metadata. Keep explanations in issue
notes and keep note bodies, actor labels, credentials, and raw response
errors out of hook output.

Keep the heartbeat request and operation fingerprint compatible. Persist
the complete new heartbeat result for replay and decode existing
string-only outcomes as expiry-only results. Preserve old client method
signatures with wrappers around a typed client method. Forward the typed
fields in CLI JSON and MCP results, retaining existing keys and expiry
text. New CLI text can additionally display the fixed reason. Older
clients ignore the extra keys, renew normally, and provide no enforcement
guarantee. A new enrolled hook treats a legacy daemon with no control
status endpoint as unavailable. An exact heartbeat retry is a historical
snapshot and must never authorize tools, clear a block, or trigger a fresh
handoff. A new operation ID proves current lease ownership under the
usual deadline. The hook uses a separate live status request even on a
healthy heartbeat loop.

## Pause, stop, and resume

**Pause.** Deny the next covered tool at the admission check, write one
generated `paused at <UTC timestamp>` note for the effective control revision
and lease generation, and keep the same lease. The first fresh observation
by the heartbeat loop or hook can acknowledge the pause. Deduplicate both
paths in the daemon and validate the live generation and control revision
before inserting the note. Keep lease renewals and control checks outside
the model tool path. TTL advances normally on each successful heartbeat.
It continues counting down during an outage or after the heartbeat owner
exits. The pause flag alone neither freezes TTL nor extends ownership.
An unclaimed session has no issue on which to acknowledge. If enabled,
`ask_owner` keeps the lease and blocks in the same way, with a fixed
owner-attention message and an acknowledgement distinct from pause.

**Stop.** The live gate denies at the next covered admission check. Heartbeats report the restriction
while the holder is winding down. A trusted lifecycle driver waits for a
safe point and submits a generation- and control-revision-fenced stop
acknowledgement. For the supported interactive demo use a serial session
with no background tools. Its next hook invocation establishes the tool
boundary. For managed concurrent runs, a trusted supervisor must confirm
quiescence of in-flight tools and subagents before release. If a renderer
or integration cannot establish quiescence, keep new tools blocked and
report stop pending for operator recovery. Background work and detached
processes require a separate mechanism to drain or terminate safely. A
heartbeat observing stop alone cannot prove quiescence.

Reuse the holder handoff transaction to persist a generated
`HANDOFF: stopped by operator (<reason code>)` note, include available
sanitized work/recovery context, and release the lease. Recheck the active
effective stop and generation in that transaction. Use a stable acknowledgement
operation ID so a lost response can be reconciled even after release.
Once the handoff commits, retire that attempt. A managed runner distinguishes
this outcome from child completion and ownership loss and suppresses its
ordinary auto-close and recovery branches. Fence managed finalization
against active control in the daemon transaction so a completion racing a
committed restriction cannot publish or bypass stop. A child that exits
while paused cannot continue heartbeating indefinitely. Preserve recovery
context and let that lease expire on the normal path. An interactive terminal may stay
open while its retired attempt remains blocked. The hook needs the original
generation in the trusted launch context to deny a retired or replaced
attempt after a later resume. Clearing before acknowledgement cancels the
pending stop if no other stop applies. A failed stop handoff keeps the gate
closed and preserves the private token while the lease remains valid. On
confirmed loss, report recovery context and leave `operator-release` to the
operator. Keep the worktree and branch for recovery throughout.

**Resume.** Clear just the selected target. A live paused holder proceeds on
its next tool attempt after a fresh clear status. A denied call is retried
explicitly by the user or agent. A stopped or expired attempt requires a
new authorized claim and launch. Flags persist through daemon restarts and
lease turnover until cleared. This packet leaves scheduler and ready/claim
eligibility alone. A newly launched enrolled session still applies matching
flags before its first covered tool and should surface them before starting
the child if the runner can check at launch.

## Claude Code hook and Codex coverage

Install a synchronous command `PreToolUse` hook for all covered Claude Code
tool names in a trusted settings layer, with a fixed absolute helper path.
Read `cwd` and session context from hook input and use the trusted launch
binding for a claimed issue and its lease generation. The helper calls a
bounded local live-status endpoint, through a read-only CLI such as
`dibs control status --json`. A claimed session's status validates its
active token and generation without extending TTL. Use environment or a
private credential file for the token. The helper needs no operator
credential. Worktree-only lookup can read applicable restrictions through
the daemon socket without a lease. Pause or stop acknowledgement, where
supported, uses a separate holder-authorized operation. Keep helper
prerequisites and runbook instructions outside repository-controlled
settings. [Claude Code hook input and output](https://code.claude.com/docs/en/hooks#hook-input-and-output).

Emit the fixed reason with an explicit denial, such as exit 2 and stderr
for Claude Code. Choose **fail closed** for enrolled sessions. A local
outage may overlap an operator's attempt to stop work. A still-valid lease
proves ownership and cannot prove the absence of a new restriction.
Timeouts, unreachable daemon, invalid responses, and missing required
context produce a fixed denial. Use a helper timeout shorter than the hook
deadline and set `onFailure: "block"` (Claude Code 2.1.295 or later).
Status reads do not perform a heartbeat or cache an allow decision. The
daemon's status snapshot is the admission point. A snapshot taken after
the control mutation commits must observe that mutation or a later one.
A previously admitted tool can start or finish after the commit, including
parallel calls that already obtained permission. The latency guarantee
applies at the admission check for each covered tool. Tool hooks cannot revoke an
already running shell or fence external effects. Document the supported
hook configuration and test inheritance by subagents before promising it.
[Claude Code PreToolUse and hook failure behavior](https://code.claude.com/docs/en/hooks#pretooluse),
[Claude Code block on hook failure](https://code.claude.com/docs/en/hooks#block-the-action-when-a-hook-fails).

**Codex has a `PreToolUse` hook** with explicit denial for Bash,
`apply_patch`, MCP calls, and other covered local function tools. Hosted
tools such as WebSearch and input or polling for an existing unified-exec
session skip that hook. Some specialized tool paths can also opt out.
Codex documents that a failed or malformed hook may let a tool run. A
future adapter must return an explicit denial on daemon errors and test
its supported surface and versions. The Claude Code reference hook and
demo are the proposed implementation deliverables. The v0.172.0 source
tests the structured denial. [Codex v0.172.0 denial test](https://github.com/openai/codex/blob/rust-v0.172.0/codex-rs/hooks/src/events/pre_tool_use.rs#L356-L375),
[Codex tool coverage](https://learn.chatgpt.com/docs/hooks#tool-coverage),
[Codex PreToolUse contract](https://learn.chatgpt.com/docs/hooks#pretooluse).

## Questions for the owner

- **Q1.** Should `ask_owner` have a public setter in the first release, and
  where should the owner see the request? The packet reserves the value
  and defines blocking behavior. It proposes no notification integration.
- **Q2.** Is stop pending with explicit operator recovery acceptable for
  runtimes that cannot prove subagent or background-tool quiescence, or
  should reliable drain support be required before those runtimes enroll?
- **Q3.** Should active flags also suppress ready/claim or scheduler
  dispatch in a separately scoped change? This proposal gates enrolled
  executions. A stopped issue may become ready while its flag remains set.
- **Q4.** Does the shared bearer with role-only `set_by` meet the owner audit
  requirement, and how will the owner isolate operator-only MCP invocation
  from agent processes on their host?
