-- +goose Up
-- +goose StatementBegin

-- ADR-026: schedd consumes NotifyAccountDeletionPending and evicts
-- live instances the moment a customer hits DELETE /v1/account.
-- The new state is distinct from STOPPED so a dashboard operator can
-- tell "this instance was killed because the customer's account is
-- about to disappear" from "this instance was idle-reaped". Drop the
-- old CHECK and recreate with the new value present.

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
    ));

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
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
