# Requirements

- **R1. Targets and authority.** Provide operator-only control by issue,
  repository, registered worktree, or daemon-wide scope. Use the daemon's
  existing operator credential boundary. An agent's actor, holder name,
  lease token, or ability to post a note grants no control authority.
- **R2. Pause and resume.** Deny a matching agent's next covered tool call
  when its admission snapshot is taken after the pause commits. Record
  `paused at …` once per effective pause and lease generation. Keep renewing
  the same lease at the normal heartbeat cadence while the session is alive
  and can prove ownership. If proof fails, the normal TTL still expires.
  Resume clears only the selected scope. A still-live, otherwise unrestricted
  session can proceed on a new tool attempt without reclaiming.
- **R3. Stop.** Deny new covered tools, then record a reason-bearing handoff
  and release the lease atomically at a verified safe point. Preserve the
  checkout and work. An acknowledged stop ends that attempt even if the
  operator clears the signal later. An expired, replaced, or lost lease
  cannot be used to hand off. An unclaimed worktree can be blocked, but has
  no issue lease to release or issue on which to write a note.
- **R4. Delivery.** Extend the successful heartbeat result with optional
  `control`, `reason`, `set_by`, and `set_at`. Include a `control_ref` with
  the source target and revision when a restriction is present so the hook
  can reconcile cached observations. Preserve `expires_at` and the existing
  idempotency contract. Expose the signal on CLI and MCP heartbeat
  results and provide an independent live read for hooks. Older clients can
  continue to renew leases and will ignore the new signal.
- **R5. Enforcement.** Ship and document a reference Claude Code
  `PreToolUse` hook. It uses the claimed issue or registered checkout, reads
  current state for every covered attempt, and denies with a fixed reason
  when control applies. Cache observed pause and stop locally by issue and
  worktree. Keep blocking until a fresh response confirms an explicit
  resume for the matching restriction, including when the daemon is
  unreachable. On daemon unavailability with no known restriction, let
  normal tool handling proceed and show a warning that control could not be
  checked. Lease-loss handling still applies. Enforcement requires a
  trusted hook and covers future tool admission.
  Record the available Codex hook and its coverage limits.
- **R6. Restricted data.** Admit only defined control values and reason codes.
  Include `ask_owner` in the restrictive vocabulary, with no public setter or notification
  integration in v1. Render fixed messages for the model. Informational
  and explanatory text belongs in issue notes. The safety path must never
  parse issue notes as control or forward their contents into hook output.
- **R7. Verification.** Cover authorization, scope overlap, pause and lease
  renewal, resume, stop and handoff, outage with and without a known
  restriction, historical heartbeat replay, and old-client compatibility.
  Demonstrate a real running Claude Code session blocked on its first
  covered post-pause tool attempt and able to execute a new attempt after
  resume. Use a scratch daemon.
