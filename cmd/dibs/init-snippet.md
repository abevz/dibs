This repo is coordinated by [dibs](https://github.com/abevz/dibs).

- **Canonical protocol**: run `dibs protocol`; source: `https://github.com/abevz/dibs/blob/main/docs/agent-protocol-v1.md`
- **GitHub issues**: follow the protocol section "Working from GitHub issues" for import, public result publication, and untrusted source text.
- **Identity**: `dibs` automatically infers your agent name and process PID from the process tree. You may optionally override this by exporting `DIBS_ACTOR=<agent-name>`.
- **Session cycle**: `ready → claim → heartbeat → handoff/close`
- **Lease tokens**: Agents never pass `--lease-token` on an executed command line, including shell expansion. Use `dibs issue run` for the lifecycle. For unavoidable manual steps, supply `DIBS_LEASE_TOKEN` in the environment or `DIBS_LEASE_TOKEN_FILE` pointing to a private mode-0600 file, and omit the flag.
- **Manual lifecycle**: Delete the token file only after close, handoff, or release exits 0 (`cmd && rm`, never `cmd; rm` or an always-on cleanup trap). On failure keep the file, reread the version and lease generation with `dibs issue get <id> --json`, check that the generation still matches your claim, fix the cause, and retry within the lease window. If the token is lost, stop and report. Never re-claim by holder name.
- **Owner recovery**: Agents only propose these commands. Check `--help` first. Use the owner's operator-authenticated `dibs-op` invocation. Every operator command requires `--reason` for the audit trail.
  - `dibs-op issue operator-release <id> --expected-version latest --reason "<why>"`
  - `dibs-op issue operator-close <id> --resolution <done|cancelled> --expected-version latest --reason "<why>" [--note …]`
- **Worktree hygiene**: implement in a sibling worktree when the coordinated checkout is a read/merge anchor; after removing a merged task worktree, use `dibs worktree prune --repo <repo-id>` (or `dibs worktree unregister --worktree <id>` for one known safe record).
- **Never** edit files without an active claim.
- **Never** touch the coordinator database.
- **Never** restate specs in issue descriptions — link them.
- **Never** close an issue without a note (`--note`) — the audit trail is for whoever comes after you.
