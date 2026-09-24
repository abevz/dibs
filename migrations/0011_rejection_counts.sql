-- Bounded durable rejection evidence, separate from issue lifecycle events.
create table rejection_counts (
  issue_id text not null references issues(id) on delete cascade,
  kind text not null,
  holder text not null,
  reason_code text not null,
  count integer not null default 0 check (count >= 0),
  first_seen_at text not null,
  last_seen_at text not null,
  last_presented_generation integer not null,
  current_generation_at_last_rejection integer not null,
  last_invocation_mode text not null,
  primary key (issue_id, kind, holder, reason_code)
);
