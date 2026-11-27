# Review

The implementation follows the four approved owner decisions. It adds
migration 0013 for ordered control changes, checkout bindings, acknowledged
revisions, and retired attempts. See the [operator guide](../../operator-control.md)
for the CLI, operator-only MCP, trusted Claude Code hook, and cache behavior.

## Live demo

On 2026-11-27, Claude Code **2.1.322** ran interactively in a registered scratch
checkout with only Bash exposed and the reference hook in trusted settings.
The session used manual enrollment with a separate 20-second heartbeat loop
and a 60-second TTL. The scratch daemon and the separate operator environment
used no live dibs data.

| Step | Observed result |
| --- | --- |
| Baseline | Bash admitted on generation 1, with original expiry `21:46:03Z`. The marker contained `baseline`. |
| Pause | The first subsequent tool call exited 2 with the fixed operator-review reason. The marker and heartbeat expiry were unchanged. |
| Renewal | After the initial expiry, generation 1 still held the lease, now through `21:47:03Z`. There was exactly one pause note for the issue revision. A competing claim was rejected. |
| Resume | Clearing the issue scope while the global pause remained still denied Bash. Clearing the global pause let the same session append `resumed` with generation 1. |
| Stop and reclaim | The serial hook denied the tool, wrote one maintenance handoff, and released the lease. The issue was open and the checkout still on its original branch. After resume, the old session was still denied. A replacement claimed generation 2. |

With the helper temporarily absent, Claude Code blocked the attempted write
through `onFailure` and displayed its runtime diagnostic. The final marker
contained exactly `baseline` and `resumed`. The issue event stream ordered
creation (1), first claim (2), notes (3–5), controlled release (6), and second
claim (7). This evidence excludes transcript content and credentials.

## Other checks

Scratch-installed binaries exercised the gate on a detached checkout via a
symlink, including Bash, Read, Edit, MCP, and subagent-shaped hook inputs.
A cached restriction survived a daemon outage and restart. After explicit
resume, an outage allowed the same worktree's tool attempt with a warning.
The ordinary MCP process rejected an operator request even with an inherited
bearer. A separately configured operator MCP instance succeeded. A managed
runner using a noninteractive test child stripped the operator bearer and
suppressed ordinary completion after a controlled stop even with a completion
marker present.

Store and HTTP tests cover all scopes, authorization, idempotent changes,
typed and legacy heartbeat outcomes, concurrent pause acknowledgement,
safe handoff, and expiry or operator release. Cache tests cover offline
decisions, scope matching, lease fencing, and stale resume/restriction
ordering. The PR records final build, suite, cross-compile, and review
results against the shipped diff.

Stop pending is supported when a runtime cannot prove quiescence. Reliable
drain and scheduler suppression remain with afc-193 and afc-194. Claude Code
is the reference integration. Its tool gate applies to covered admission
checks. The cache assumes a stable coordinator socket and database.
