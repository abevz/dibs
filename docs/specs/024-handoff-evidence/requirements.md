# Requirements

- R1: A failed, cancelled or incomplete issue run must not make unfinished work immediately claimable. Preserve ownership until the last proven lease deadline; record recovery context without releasing.
- R2: Issue get, successful claim and deliberate handoff expose the latest HANDOFF and evidence inventory, including project-scoped issues.
- R3: Register screenshot, log, review and integration evidence with issue ID, SHA-256, optional commit and/or diff SHA-256. At least one revision binding is required. Store immutable copies outside Git in durable local state. Existing repository artifact registration remains compatible.
- R4: Close and publish may request evidence kinds bound to the final diff hash. A review requirement requires status PASS and intact content. Enforce close before its state transition.
- R5: Retention previews and explicitly prunes terminal issue evidence older than a chosen number of days, retaining metadata. Cleanup previews/removes only registered temporary source copies whose durable copy and source still match their checksum. Unknown /tmp files are never inferred safe to delete.
- R6: Cover failure recovery and register -> handoff -> claim visibility with production-backed tests. Update agent protocol and API docs. The owner has authorized delivery through CI-gated merge. Live installation/restart is deferred to a separate explicit owner command and the upgrade runbook.

- R7: Migration 0012 must preserve existing data, be repeatable through the migration ledger, and cause older binaries to refuse the upgraded schema via VerifyKnownMigrations. Document coordinated upgrade and database-plus-binary rollback while dq is quiescent.
