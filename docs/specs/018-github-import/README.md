# 018 GitHub Import (thin slice)

Status: **approved (2026-09-26)**, implementation pending. Authoring task: `afc-162`.
Target release: `v0.1.0-rc.4`.

Most people who try dibs already keep their backlog in GitHub Issues. Asking
them to re-create those tasks in another tracker is the largest adoption
barrier. This packet lets dibs work on top of GitHub Issues with two commands:

```sh
dibs issue import https://github.com/acme/app/issues/42   # -> app-7, linked to #42
dibs issue run app-7 --require-complete -- <agent command>
dibs issue close app-7 ... --publish                      # one result comment on #42
```

The unique value of dibs stays the atomic claim. GitHub remains the place
where work is described and discussed; dibs decides which agent owns it.

## Scope

- Import one GitHub issue by URL or `owner/repo#N` into a dibs issue with a
  stable external key; repeating the import returns the same dibs issue.
- Publish the closed result (resolution, PR/commit/branch, closing note) back
  to the source issue as exactly one comment, safely retryable.
- `dibs doctor` reports whether `gh` is installed, authenticated, and working;
  `publish` checks that the source issue is reachable and not locked first.
- GitHub access goes through the user's `gh` CLI. dibs stores no GitHub
  credentials and the daemon stays offline.

## Out of scope

This slice deliberately stops short of the full external-tracker workflow in
[016 implementation plan](../016-adoption/implementation-plan.md) stage 5
(`afc-89`, `afc-90`) and attachments (stage 4, `afc-135`):

- stored attachments; screenshots and files stay links in the imported body,
  which agents open with `gh`;
- tracking source edits after import, bulk or label-based import, label to
  tag mapping, assignee sync;
- closing or relabeling the GitHub issue (a PR with `Fixes #N` already does
  that);
- a durable publication queue; a failed publish is retried explicitly;
- GitHub Enterprise hosts, pull requests as sources, MCP tools, other trackers.

`afc-89`/`afc-90` stay open for the full workflow and should reuse this
packet's external key format and publish marker.

## Documents

- [requirements.md](requirements.md) — behavior and acceptance.
- [design.md](design.md) — command shapes, data mapping, idempotency, failures.
- [tasks.md](tasks.md) — slices `afc-163`–`afc-165` and release steps.
- [review.md](review.md) — decisions and evidence.
