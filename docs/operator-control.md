# Operator control for agent sessions

Dibs can pause covered tool calls or request a stop for an issue, repository,
registered worktree, or a single daemon. This guide covers the reference
Claude Code integration. See the [contract](specs/025-operator-control/design.md)
for how scope, lease ownership, and compatibility work.

## Operator setup

Configure `DIBS_OPERATOR_TOKEN` for the daemon and for your separate operator
CLI environment. Use the same bearer on both. Existing dibs operator
credentials work with these commands. Keep the bearer and operator MCP
configuration in an operator-owned environment or config that agent
processes never load. Run ordinary `dibs-mcp` for agents. An operator-only
MCP session can run `dibs-mcp --operator` with the bearer in its separate
environment to expose `control_pause`, `control_stop`, and `control_resume`.
The daemon records the actor label for auditing and returns `set_by: operator`.
Credentials and trusted hook files must be outside agent reach on deployments
that allow untrusted shell commands. Same-UID cooperative deployments depend
on the operator keeping that configuration isolated. [Operator token setup](operations.md).

From the operator environment choose one target per command.

```sh
dibs control pause --issue demo-1 --reason operator_review
dibs control stop --worktree /absolute/registered/checkout --reason maintenance
dibs control resume --issue demo-1
```

Other selectors are `--repo` (registered UUID or unambiguous logical name)
and `--global` (this daemon). Resume applies to the selected target. Keep an
`--operation-id` for an exact retry after an ambiguous result. Built-in
reason codes are `operator_requested` (the default), `operator_review`, and
`maintenance`. Put explanations in issue notes. The signal cannot carry
instructions. The `ask_owner` value is reserved for a later release.

## Claude Code setup

Install matching dibs CLI and daemon versions, register the checkout, and use
Claude Code 2.1.295 or later. In trusted settings outside the checkout, add
this command hook. Set `command` to the installed absolute binary path.
Use this settings layer only in sessions intended to be enrolled.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "/absolute/bin/dibs control hook",
            "onFailure": "block",
            "timeout": 5
          }
        ]
      }
    ]
  }
}
```

The hook uses the Claude Code session cwd and the trusted launch binding. Keep
the hook installed for subagents, and register or configure their checkouts.
The hook evaluates each covered tool call. Already admitted calls and
detached work can finish. [Claude Code hook coverage and setup](https://code.claude.com/docs/en/hooks#pretooluse).

A supervisor can start its client with
`dibs issue run <issue> --operator-control --require-complete -- <command>`.
Run it in the registered checkout. It binds the claim to the checkout and
refreshes the lease and control observations in the background. Its child
gets lease context. Give the child the trusted hook settings before it can
use tools.

An already claimed interactive session can use `dibs control enroll` from
the registered checkout before starting Claude Code. Supply
`DIBS_ISSUE_ID`, `DIBS_LEASE_GENERATION`, and
`DIBS_LEASE_TOKEN_FILE` pointing to the private lease token file, or supply the
lease token in the trusted environment. Maintain regular heartbeats outside
the model. For an unclaimed session, run from a registered checkout without
lease variables. Issue acknowledgements require a claim.

With the default hook, stop blocks new covered tools, reports stop pending,
and retains the lease while the supervisor remains alive and can renew it.
A trusted lifecycle driver that knows every in-flight tool and subagent has
quiesced can send the stop acknowledgement. The reference helper's
`--serial` option makes that assertion at its next top-level hook boundary.
Use it only in an operator-controlled serial session whose tool policy
precludes background commands, parallel tool execution, and subagents.
The demo uses that limited setup. In other sessions, let the operator verify
quiescence and use explicit recovery if needed. Automatic drain is tracked in
afc-193. Dispatch gating is tracked in afc-194.

## Outages and recovery

A cached restriction keeps blocking during a daemon outage until an explicit
matching resume is confirmed. With no known restriction the hook warns and
leaves normal tool permissions in effect. Known lease loss or passing the last
proven expiry blocks a claimed session. A stopped attempt stays retired after
resume and requires a new launch. The cache is private under
`$XDG_STATE_HOME/dibs/control` or `~/.local/state/dibs/control`. Keep it outside
checkout and agent-controlled paths. A successful status read can confirm a
lease deadline and never extends it.

Codex has a `PreToolUse` hook for covered local tools. Hosted tools and
transport for an already running command are outside that hook. Codex may
continue on hook errors. The supplied hook integration is for Claude Code.
Other runtimes may consume heartbeat, status, and acknowledgement APIs with
their own trusted gate. [Codex tool coverage](https://learn.chatgpt.com/docs/hooks#tool-coverage).
