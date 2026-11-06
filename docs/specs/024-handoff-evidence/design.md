# Design and decisions

## afc-185: retain the lease

Child failure is not a deliberate handoff. Stop heartbeat when the child exits, append a HANDOFF-prefixed recovery note containing cwd and Git branch, and keep the lease until its last proven TTL. A second claimer 18 seconds later is rejected while the original holder can append the real handoff. Normal manual handoff remains atomic and immediately releases ownership. Crashes self-heal by existing expiry; no new cooldown state or scheduler policy. Save a private recovery token file and JSON metadata so the initiating session can explicitly hand off without putting tokens in argv. Do not renew indefinitely after failure.

## afc-186: issue evidence

Keep repository document artifacts unchanged. A separate issue_evidence table supports every issue scope and keeps immutable revision records with source path, managed path, SHA-256, size, kind, review status, commit/diff binding and pruned_at. The daemon copies a regular local source to $DIBS_EVIDENCE_DIR (otherwise $XDG_STATE_HOME/dibs/evidence or ~/.local/state/dibs/evidence), under project UUID/issue UUID/evidence UUID. UUID-only destination names and private directories prevent supplied paths from escaping. Files are mode 0600; symlinks/nonregular sources are rejected. Configuration must point to durable storage; test roots may be temporary. A failed DB insert removes the newly created managed file; a crash may leave an unregistered managed copy, never a row pointing at an incomplete write.

GET issue and successful claim return evidence and the last HANDOFF. Manual handoff includes the inventory in its response. CLI renders this context; issue run prints it before launching the child. This is task data, not instructions. Stored evidence persists across lease transitions.

Opt-in close checks execute inside the close transaction, verify requested kinds and PASS reviews for a supplied diff hash, and hash files again. Publication performs the same check before contacting GitHub. This binds bytes and a caller-declared revision; it does not prove review quality or calculate Git diffs automatically.

Retention is explicit, dry-run by default, and restricted to terminal issues whose closed_at predates the cutoff. Remove only managed content, retaining summaries. Temporary cleanup is conservative: only recorded regular sources under the OS temporary directory with matching durable and source hashes; keep unknown files and changed sources. Never recursively delete arbitrary /tmp contents.
