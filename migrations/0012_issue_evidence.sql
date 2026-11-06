CREATE TABLE issue_evidence (
 id TEXT PRIMARY KEY,
 issue_id TEXT NOT NULL REFERENCES issues(id),
 kind TEXT NOT NULL CHECK (kind IN ('screenshot','log','review','integration')),
 path TEXT NOT NULL UNIQUE,
 source_path TEXT NOT NULL,
 sha256 TEXT NOT NULL,
 size INTEGER NOT NULL,
 commit_sha TEXT NOT NULL DEFAULT '',
 diff_hash TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 pruned_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX issue_evidence_issue ON issue_evidence(issue_id);
