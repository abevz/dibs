-- Durable mutation idempotency ledger (AFC-SDD-0159).
--
-- A row records one client-identified logical mutation and its committed
-- public outcome. The row is written in the same transaction as the mutation
-- it describes, so the ledger can never claim an effect that did not commit
-- and can never miss one that did.
--
-- operation_id is a client-generated opaque capability. Knowledge of it, plus
-- an identical request fingerprint, is what authorizes replay -- holder,
-- session_id, and actor remain attribution and never authorize anything
-- (AFC-SDD-0154).
--
-- outcome_json may contain a lease token for claim operations. It is therefore
-- readable only through an exact operation replay: no list/read/diagnostic API
-- exposes this table.
create table operations (
  operation_id     text primary key,
  operation_kind   text not null,
  target_id        text not null,
  actor            text not null default '',
  fingerprint      text not null,
  status           text not null check (status in ('completed')),
  outcome_json     text not null default '{}',
  created_at       text not null,
  retain_until     text not null
);

create index idx_operations_retain_until
  on operations(retain_until);
