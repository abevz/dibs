# afc-181 — Unattended-agent guidance review

Reviewed `origin/main` at `a28b4fb7c6a8d8ec05871a06cffa657998aea60e`
on 2026-11-27. This review covers the dibs daemon, CLI, and shipped
hooks. The behavior of external launchers and factories is outside this
assessment. The linked Anthropic page was available and reread.
[Anthropic unattended runs](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5#unattended-agentic-runs),
[time signals](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5#time-signals-for-multiagent-harnesses),
[pasted text](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5-5#mark-pasted-text-in-user-messages).

## 1. Text-only end turn, checklist, and bounded continuation — Partial

**Explicit completion is covered on the opt-in path.** With
`dibs issue run --require-complete`, a successful child exit also needs a
valid `dibs hooks complete` marker. The hook records a marker, and the
supervisor closes after the child exits successfully. The SessionStart
hook recommends this path and tells the agent to mark completion once
acceptance criteria are met. Without that flag, a zero exit can close a
run whose only output was a progress report. The runner forwards
child output and uses exit status and the marker. It has no
model-turn or `end_turn` interface.
[cmd/dibs/cmd_issue_run.go:54–61](../../cmd/dibs/cmd_issue_run.go#L54-L61),
[cmd/dibs/cmd_issue_run.go:198–238](../../cmd/dibs/cmd_issue_run.go#L198-L238),
[cmd/dibs/cmd_issue_run.go:274–315](../../cmd/dibs/cmd_issue_run.go#L274-L315),
[cmd/dibs/cmd_hooks.go:35–80](../../cmd/dibs/cmd_hooks.go#L35-L80),
[cmd/dibs/cmd_hooks.go:114–137](../../cmd/dibs/cmd_hooks.go#L114-L137).

**Checklist and same-session continuation are gaps.** Acceptance criteria
are issue text that the protocol asks agents to verify. The marker
contains optional result metadata. Holder close checks state, ownership,
and optional evidence, with no checklist evaluation. There is no caller
loop in `issue run` to inspect a text-only end turn, send a continuation,
or cap those messages at two or three.
[internal/core/issue.go:100–120](../../internal/core/issue.go#L100-L120),
[docs/agent-protocol-v1.md:152–174](../agent-protocol-v1.md#L152-L174),
[cmd/dibs/cmd_hooks.go:35–80](../../cmd/dibs/cmd_hooks.go#L35-L80),
[cmd/dibs/cmd_issue_run.go:226–266](../../cmd/dibs/cmd_issue_run.go#L226-L266),
[internal/store/sqlite/issues.go:1525–1586](../../internal/store/sqlite/issues.go#L1525-L1586).

**A session can end with an active claim and no actual handoff.** An
unsupervised holder can exit without sending any lifecycle request. A
supervised failure or missing completion marker stops renewal and tries
to save recovery context and a private token, then appends a
`HANDOFF:`-prefixed recovery note while retaining ownership until expiry.
An abrupt supervisor exit can bypass that branch. A failed note write
can also leave the lease with no new note. Deliberate holder handoff
writes a note and releases atomically. Operator release is an authenticated
recovery path for an `in_progress` issue. It reopens the issue and revokes
the lease with an audit event. A subsequent fresh heartbeat with that
lease fails. That mutation has no process-quiescence check.
[internal/store/sqlite/issues.go:688–734](../../internal/store/sqlite/issues.go#L688-L734),
[internal/store/sqlite/issues.go:777–819](../../internal/store/sqlite/issues.go#L777-L819),
[internal/store/sqlite/issues.go:944–961](../../internal/store/sqlite/issues.go#L944-L961),
[cmd/dibs/cmd_issue_run.go:251–266](../../cmd/dibs/cmd_issue_run.go#L251-L266),
[cmd/dibs/cmd_issue_run.go:342–375](../../cmd/dibs/cmd_issue_run.go#L342-L375),
[internal/store/sqlite/issues.go:1017–1109](../../internal/store/sqlite/issues.go#L1017-L1109),
[internal/api/issues.go:656–702](../../internal/api/issues.go#L656-L702),
[internal/store/sqlite/issues.go:1811–1874](../../internal/store/sqlite/issues.go#L1811-L1874).

**Automatic re-dispatch across attempts is a separate gap.** Ready
excludes active leases. An otherwise eligible `in_progress` issue can
become ready again after expiry. Claim rechecks eligibility, logs a
replaced expired attempt, and increments the generation. Ready and claim
have no maximum-attempt predicate. A caller repeatedly selecting ready
issues can therefore launch further attempts. The protocol documents
`exec/auto` as a factory routing filter, and this review makes no claim
about a particular factory's retry policy.
[internal/store/sqlite/issues.go:395–424](../../internal/store/sqlite/issues.go#L395-L424),
[internal/store/sqlite/issues.go:638–724](../../internal/store/sqlite/issues.go#L638-L724),
[docs/agent-protocol-v1.md:355–369](../agent-protocol-v1.md#L355-L369).

## 2. Background commands and subagents before done — Partial

The managed runner waits for the direct child and joins its heartbeat
goroutine before closing. On cancellation, it signals the workload group,
with a bounded wait and group cleanup. The ordinary successful exit path
does not verify that background commands or subagents have drained.
A worker that outlives its parent, particularly a detached process or
an externally managed subagent, can therefore still be running after
the issue closes. A holder can also close directly with a live token and
generation. That transaction has no knowledge of child processes or
subagent state. Optional evidence checks attest to registered artifacts.
They provide no runtime liveness check.
[cmd/dibs/cmd_issue_run.go:198–266](../../cmd/dibs/cmd_issue_run.go#L198-L266),
[cmd/dibs/cmd_issue_run.go:274–315](../../cmd/dibs/cmd_issue_run.go#L274-L315),
[internal/store/sqlite/issues.go:1525–1612](../../internal/store/sqlite/issues.go#L1525-L1612).

Operator-initiated tool gating, stopping at a safe point, and the policy
for runtimes that cannot prove quiescence belong to afc-189, which already
cites afc-181 and asks the owner about pending stop and drain behavior.
The remaining follow-up below concerns ordinary successful completion.
[afc-189 packet and owner question Q2](https://github.com/abevz/dibs/pull/139).

## 3. Elapsed-time messages and a caller hard timeout — Gap

The CLI exposes a renewable ownership TTL, and the supervisor extends
it every TTL/3 with a live heartbeat. Heartbeat failure or loss of
ownership can cancel the child. A healthy renewal can continue past the
original lease deadline. The runner forwards child streams and provides
no per-message elapsed-budget annotation or fixed wall-clock task timeout.
The CLI context handles interrupt and termination signals, so a caller can
impose a hard deadline and let the normal cancellation path run.
[cmd/dibs/cmd_issue_run.go:54–110](../../cmd/dibs/cmd_issue_run.go#L54-L110),
[cmd/dibs/cmd_issue_run.go:147–224](../../cmd/dibs/cmd_issue_run.go#L147-L224),
[cmd/dibs/cmd_issue_run.go:397–492](../../cmd/dibs/cmd_issue_run.go#L397-L492),
[cmd/dibs/main.go:96–104](../../cmd/dibs/main.go#L96-L104).

## 4. Mark untrusted pasted content — Partial

The protocol tells agents to treat imported GitHub titles and bodies as
task data. The SessionStart hook flattens displayed titles to one line and
adds that instruction when a listed issue is from GitHub. It still inserts
the title into model context without a random per-block boundary. The
importer preserves the upstream title and body as issue fields. Text issue
detail renders those fields verbatim, and the run renders the prior
handoff body as recovery context. Those paths provide provenance or
protocol guidance, with no paired random-ID tags at the reviewed
model-facing boundary. Arbitrary text pasted into an external agent's
conversation belongs to that agent's caller. Delimiter tags help the
model identify source text and are one guardrail.
[docs/agent-protocol-v1.md:84–90](../agent-protocol-v1.md#L84-L90),
[cmd/dibs/cmd_hooks.go:99–150](../../cmd/dibs/cmd_hooks.go#L99-L150),
[internal/ghsync/import.go:88–104](../../internal/ghsync/import.go#L88-L104),
[cmd/dibs/main.go:511–524](../../cmd/dibs/main.go#L511-L524),
[cmd/dibs/cmd_issue_run.go:153–164](../../cmd/dibs/cmd_issue_run.go#L153-L164),
[cmd/dibs/cmd_evidence.go:60–68](../../cmd/dibs/cmd_evidence.go#L60-L68).

## Proposed follow-up issues

| Suggested priority | Title | One-line scope |
| --- | --- | --- |
| P2 | Gate unattended completion on checklist evidence and bounded continuation | In the caller harness, track checklist state, require explicit completion before reporting success, and stop for review after two or three automatic continuations of an incomplete turn. |
| P2 | Bound automatic re-dispatch across failed attempts | In the dispatching caller, count distinct failed or expired attempts per issue and require operator review after a configurable limit. |
| P3 | Expose abandoned attempts without a terminal note | Surface last confirmed heartbeat, expiry, and missing recovery context for exited or crashed runs while keeping the existing holder and operator recovery paths. |
| P2 | Require quiescence before normal managed completion | Have the runtime account for outstanding tool work and subagents, surface their results, and require a settled state before it reports successful completion to dibs. |
| P3 | Add elapsed budget signals and a caller deadline | Stamp model-bound messages with elapsed time and enforce an independent wall-clock timeout through the runner's cancellation and recovery path. |
| P2 | Delimit untrusted task context before passing it to a model | Wrap imported issue text, inherited notes, and pasted text at the applicable prompt boundary with per-block IDs and trusted interpretation instructions. |

The owner decides which follow-ups to file. afc-189 owns the restrictive
operator control channel and its pending-stop behavior.
