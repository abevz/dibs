# One-shot Claude Code and Codex integration

Requires a `dibs` build containing `dibs hooks` and a registered repository (`dibs init`). Install the project SessionStart hook once:

```sh
dibs hooks install --agent claude
dibs hooks install --agent codex
```

The installer merges one command into `.claude/settings.json` or `.codex/hooks.json` and keeps other entries. It records the exact path of the `dibs` executable used for installation; run it again after moving the binary. Codex prompts to trust project hooks; inspect and trust the command with `/hooks`. Both hooks show up to ten ready issues at session start and never claim one.

Choose an issue ID, then start one agent command in a dedicated terminal or worktree:

```sh
dibs issue run afc-123 --require-complete -- claude -p 'Work on afc-123. Read its acceptance criteria. When all criteria pass, run dibs hooks complete, then finish successfully. Otherwise explain remaining work and exit.'

dibs issue run afc-124 --require-complete -- codex exec 'Work on afc-124. Read its acceptance criteria. When all criteria pass, run dibs hooks complete, then finish successfully. Otherwise explain remaining work and exit.'
```

Use the actual IDs and instructions for your tasks. Set `DIBS_ACTOR` to a distinct value per agent when your environment does not infer it. `issue run` claims atomically and heartbeats while the child runs. A second session cannot claim the same issue. A command that fails or exits without `dibs hooks complete` records `HANDOFF:` and releases the lease. Lease loss stops the child; a dead session's lease expires and becomes reclaimable. Do not call `dibs hooks complete` before the work meets the issue's acceptance criteria.

If your agent's login shell finds an older `dibs` on PATH, use the absolute binary path shown by the SessionStart hook when calling `hooks complete`.

`Stop` events in both agents happen after an individual response, so this integration deliberately does not treat a Stop event as task completion. The one-shot child exit is the lifecycle boundary. `dibs hooks complete` records only an intent marker; the parent closes the issue after the child exits successfully and still owns the lease.

To remove the integration, delete only the `dibs hooks session-start` group from the relevant JSON file. Do not delete unrelated hooks.
