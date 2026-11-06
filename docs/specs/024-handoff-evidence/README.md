# Handoff recovery and durable evidence

Scope: afc-185 and afc-186. Requirements, design and tasks own the change.
Implementation and the deferred-upgrade runbook are verified. Git delivery
requires independent PASS review and green CI; coordinator issues close only
after merge. Live installation is a separate owner-authorized operation.

See [upgrade runbook](upgrade-runbook.md) for quiescence, consistent SQLite
backup, coordinated binary installation and database-plus-binary rollback.
