# 018 GitHub Import Review

## Owner direction (2026-09-26)

- The owner judged that GitHub integration matters for attracting users and
  chose a thin slice over the full stage 4–5 work in the
  [016 plan](../016-adoption/implementation-plan.md): import by URL and one
  result comment, no attachments store, no source-change tracking. Target
  release: `v0.1.0-rc.4`. The public launch post waits for this slice; a small
  circle of testers can start on rc.3.
- `afc-89`, `afc-90`, and `afc-135` remain the full workflow and attachments
  work and are not closed by this packet.

## Design decisions

- **`gh` instead of a built-in HTTP client.** It reuses the user's auth,
  including private repositories and SSO; dibs stores no GitHub token; the
  daemon remains offline and local-only.
- **No schema change.** Idempotent import uses a lookup by external key plus a
  deterministic create operation ID; exactly-once publish uses a marker in the
  GitHub comment instead of a local ledger.
- **Readiness without a probe repository.** `dibs doctor` checks `gh`
  installation, authentication, and a working authenticated API call
  (`rate_limit`). A public probe repository would prove only connectivity;
  per-repository access is checked by `import` and `publish` (owner request,
  2026-09-26).
- **Accepted gap.** Two simultaneous publishes of the same close can post
  twice. A durable ledger is left to `afc-90`.

## Approval

- [ ] Owner approval of this packet (pending).

## Evidence

None yet; slices `afc-163`–`afc-165` record theirs here.
