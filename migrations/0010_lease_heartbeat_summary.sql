-- A bounded per-attempt renewal summary survives in the terminal event.
alter table leases add column heartbeat_count integer not null default 0
  check (heartbeat_count >= 0);
alter table leases add column last_heartbeat_at text not null default '';
