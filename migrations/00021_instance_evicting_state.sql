-- +goose Up
-- +goose StatementBegin

-- ADR-026: schedd consumes NotifyAccountDeletionPending and evicts
-- live instances the moment a customer hits DELETE /v1/account.
-- The new state is distinct from STOPPED so a dashboard operator can
-- tell "this instance was killed because the customer's account is
-- about to disappear" from "this instance was idle-reaped".
--
-- Race-free addition (Postgres ≥11): the new CHECK is added with NOT
-- VALID so concurrent INSERTs/UPDATEs from other daemons (apid inserts
-- new instance rows; schedd transitions existing ones) are not blocked
-- during the validate pass. VALIDATE CONSTRAINT then scans the table
-- once with a SHARE UPDATE EXCLUSIVE lock that allows reads/writes,
-- not the DROP+ADD pattern that leaves the column unconstrained for
-- the duration of the ADD. The hard guarantee the operations team
-- needed: at no point in this migration is `state` allowed to take
-- a value outside the new set *for new rows*.

alter table instances
  drop constraint if exists instances_state_check;

alter table instances
  add constraint instances_state_check
    check (state in (
      'pending',
      'cold_booting',
      'waking',
      'running',
      'parked',
      'stopped',
      'evicting_account_deleting'
    )) not valid;

alter table instances
  validate constraint instances_state_check;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
-- Drop + re-add mirrors the original instances_state_check from
-- earlier migrations; the downgrade is matched-set so a rollback
-- leaves existing rows happy.
alter table instances
  drop constraint if exists instances_state_check;
alter table instances
  add constraint instances_state_check
    check (state in (
      'pending',
      'cold_booting',
      'waking',
      'running',
      'parked',
      'stopped'
    ));
-- +goose StatementEnd
