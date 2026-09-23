This repo is coordinated by [dibs](https://github.com/abevz/dibs).

- **Canonical protocol**: run `dibs protocol`; source: `https://github.com/abevz/dibs/blob/main/docs/agent-protocol-v1.md`
- **Identity**: `dibs` automatically infers your agent name and process PID from the process tree. You may optionally override this by exporting `DIBS_ACTOR=<agent-name>`.
- **Session cycle**: `ready → claim → heartbeat → handoff/close`
- **Worktree hygiene**: implement in a sibling worktree when the coordinated checkout is a read/merge anchor; after removing a merged task worktree, use `dibs worktree prune --repo <repo-id>` (or `dibs worktree unregister --worktree <id>` for one known safe record).
- **Never** edit files without an active claim.
- **Never** touch the coordinator database.
- **Never** restate specs in issue descriptions — link them.
- **Never** close an issue without a note (`--note`) — the audit trail is for whoever comes after you.
