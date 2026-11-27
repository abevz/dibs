CREATE TABLE control_changes (
  revision INTEGER PRIMARY KEY AUTOINCREMENT,
  target_kind TEXT NOT NULL CHECK (target_kind IN ('issue','repo','worktree','global')),
  target_id TEXT NOT NULL,
  action TEXT NOT NULL CHECK (action IN ('pause','stop','ask_owner','resume')),
  reason TEXT NOT NULL,
  actor TEXT NOT NULL,
  set_at TEXT NOT NULL
);
CREATE INDEX idx_control_target ON control_changes(target_kind, target_id, revision DESC);

CREATE TABLE control_bindings (
  issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  lease_generation INTEGER NOT NULL,
  worktree_id TEXT NOT NULL,
  PRIMARY KEY (issue_id, lease_generation)
);

CREATE TABLE control_observations (
  issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  lease_generation INTEGER NOT NULL,
  revision INTEGER NOT NULL REFERENCES control_changes(revision),
  PRIMARY KEY (issue_id, lease_generation, revision)
);

CREATE TABLE control_retired_attempts (
  issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
  lease_generation INTEGER NOT NULL,
  PRIMARY KEY (issue_id, lease_generation)
);
