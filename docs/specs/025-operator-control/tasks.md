# Tasks

Every implementation task is pending owner approval. Keep tests on a scratch
daemon with real migrations and worktrees.

- [ ] **T1. Control state and authorization (R1, R6).** Add persistent scoped
  state, ordered audit records, stable revisions and request replay. Test
  missing/wrong operator token, absent server credential, spoofed actor and
  holder token, exactly one target, ambiguous names and unknown targets.
  Test overlaps at all scopes, deterministic precedence, clearing one target,
  retries and restart persistence. Reject unknown controls, reason text and
  injection-shaped inputs. Note text must never affect resolution.
- [ ] **T2. Enrollment and delivery (R1, R4).** Add the execution-checkout
  binding, shared resolver and live status. Test registered sibling
  worktrees, a project-scoped claim launched in a repo, detached and symlinked
  checkout paths, cwd changes, unclaimed sessions, subagent bindings, and stale
  generations. Prove status reads do not extend TTL. Test old CLI/MCP response
  decoding, new clients against expiry-only responses, and exact replay of
  both historical string and new typed outcomes. Verify the wire, CLI JSON,
  and MCP results preserve `control_ref` when present, including on replay.
  Change control between heartbeats and show only a new operation ID and
  the live lookup see it.
- [ ] **T3. Lifecycle and surfaces (R2, R3, R4, R6).** Wire CLI, operator MCP,
  and holder acknowledgement. Cover one note per revision and generation
  under concurrent heartbeat/hook observations, pause beyond the original TTL
  with live renewals, lease expiry with renewal lost, and resumed tools with
  the same live lease. Verify stop racing resume, expiry, a new claim,
  operator-release, and the runner's ordinary completion. Verify duplicate or
  ambiguous stop results, retained token on failure, exactly one handoff,
  released lease, preserved checkout, and retired-session denial after resume.
  Verify controlled stop suppresses auto-close and publication. Test managed
  children and unprivileged MCP cannot access the operator credential.
- [ ] **T4. Claude Code hook and install guide (R5, R7).** Test explicit
  denial for each supported tool class and subagents. Verify a cached pause
  and a cached stop each deny during disconnect, timeout, and helper restart,
  including signals first seen by heartbeat. Check cache matching for
  issue, repo, worktree, and global restrictions. Cover concurrent
  observations, restart persistence, historical replay, and a resume
  issued while the client was offline. Require a newer explicit resume for
  the matching target before removing a cached restriction. Verify that
  an unrelated resume, omitted live signal, or late older resume leaves a
  newer restriction in effect, and that a late older restriction cannot
  reapply after a newer resume. With an empty or clear cache, test that disconnect, timeout,
  and an older daemon leave normal permissions in effect and show the
  fixed warning. Verify both paths preserve the original lease deadline
  and that an expired or retired attempt still blocks. Exercise malformed
  replies, invalid binding, and unreadable cache as fixed denial cases.
  Verify the fixed reason and absence of credentials, raw response errors,
  or note text in helper output. Separately, confirm a missing helper blocks
  through Claude Code's `onFailure` setting and produces its own runtime
  diagnostic. Test in-flight boundaries and pending stop when concurrent
  activity cannot be drained. Record client versions and hook
  failure behavior. Document Codex coverage and the enforcement boundary.
- [ ] **T5. End-to-end demo and independent review (R2, R3, R7).** Record
  versioned, sanitized evidence under a scratch operator and agent setup.

## Demo acceptance

1. Start a live interactive Claude Code session with the trusted reference
   hook in a registered scratch checkout. Claim a scratch issue, use a short
   TTL with background heartbeat, and capture the initial lease generation
   and expiry. Use a deterministic tool action that appends to a scratch
   marker file. Confirm it can execute once.
2. Commit a pause from the operator session before the next heartbeat tick.
   Immediately request the same tool action. Capture the explicit denial and
   unchanged marker. The first covered tool attempt presented to the hook
   after commit must be denied, even before heartbeat observes the signal.
3. Hold the pause beyond the initial expiry while successful heartbeats
   continue. Confirm the generation is unchanged, expiry has advanced, and
   precisely one `paused at …` note exists. A second holder cannot claim.
4. Resume from the operator session and retry the tool in the same session.
   Confirm one new marker entry and the same generation. Verify that
   resume of a narrower scope leaves any broader applicable restriction.
5. Repeat with stop in a serial session. Confirm denial, one reason-bearing
   handoff, no active lease, an open issue, and intact worktree and branch.
   Clear the stop and verify the retired session stays denied and a new
   authorized claim can proceed.

Record daemon event ordering, heartbeat and hook decisions, marker contents
and issue snapshots with tokens removed. Record PASS or the causal failure
for each step. Phase 1 delivers the test plan. Implementation and the demo
require approval first.
