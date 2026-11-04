# Account deletion includes its managed PostgreSQL databases

Date: 2026-10-24
Status: Accepted
Amends: ADR-155 (provider-neutral managed PostgreSQL), ADR-021 (account export and staged deletion)
Related: ADR-20261017125602 (non-payment deletion notices)

## Context

An account cannot enter `deleted_pending` while it owns a live personal
managed PostgreSQL database. The trigger `guard_account_managed_postgres_databases`
refuses the transition, and ADR-155 and the preview runbook tell the customer to
delete each database first. Since migration 20261024145700943 the refusal is a
409 for self-service and a counted, alerted block for the dunning sweep
(`meterd_dunning_deletion_blocked_accounts`, `FaasDunningDeletionBlocked`).

The block has two costs:

- A suspended account past the non-payment deadline never reaches erasure. Its
  provider databases keep running at platform cost and its data is never erased.
- A customer who wants to leave must delete each database before they can delete
  the account.

The owner decided that both paths delete the databases. This record says when
and how.

## Decision

An account deletion can include the account's personal managed PostgreSQL
databases. Personal means exactly what the guard counts: databases the account
created in its personal organization, or in an organization without resource
ownership. Databases owned by a company organization are never included.

- **One flag on the account.** `accounts.deletion_includes_managed_postgres` is
  set in the same `UPDATE` that moves the account to `deleted_pending`. The
  guard trigger lets the transition through when the flag is set. On any other
  status, the trigger clears it. A restored or paid-back account therefore
  forgets the choice, and an earlier failed attempt can never leave a stale opt-in
  behind.
- **Databases go when the grace ends, with the rest of the data.** Nothing is
  deleted when the account enters `deleted_pending`. The databases stay running
  through the 30-day grace, so an account restored or paid back during the grace
  keeps them. When the grace ends, apid's grace sweep
  (`pkg/grace` `BeforeAccountDelete`) deletes them before the account row:
  1. It deletes their bindings.
  2. It deletes each database through `managedpostgres.Service.Delete`, the same
     path a customer's `gregale postgres delete` takes.
  3. It retries on the next tick until every database is a provider-confirmed
     `deleted` tombstone.

  A database blocked by a restore descendant waits until the descendant is gone.
  One held by an environment clone waits until the hold is released. The account
  row is deleted only after every database is gone. apid remains the only writer
  of `managed_postgres_*` customer-intent rows. meterd only writes the account's
  status and flag, as it already writes the status.
- **Non-payment deletion always includes them.** Both the durable command
  (`ScheduleNonpaymentDeletionAccount`) and the direct dunning step set the flag.
  The operator kill switch `FAAS_NONPAYMENT_DELETION_MANAGED_POSTGRES=off`
  restores the earlier behaviour on apid and meterd: the account stays suspended,
  is counted by the blocked gauge and is never deleted. The kill switch does not
  touch accounts that are already flagged.
- **Self-service includes them only on request.** `DELETE /v1/account` takes
  `include_managed_postgres_databases=true`. The CLI takes
  `gregale account delete --include-postgres-databases`, and the dashboard form
  has a checkbox. Without the request, the 409
  `account_deletion_managed_postgres_databases` is unchanged. The option is a new
  customer surface and stays dark until launch. Only accounts listed in
  `FAAS_ACCOUNT_DELETION_MANAGED_POSTGRES_ACCOUNTS` (comma-separated UUIDs, or
  `*`) see it. For every other account, the parameter is ignored exactly as an
  unknown one is, the CLI flag is not in help, the dashboard shows no checkbox,
  and OpenAPI and the SDKs do not list it.

## Consequences

- A paid-back or restored account keeps its databases and loses its opt-in.
- The 30-day grace is also the database's last recovery window. Provider-side
  restore history ends when the provider deletes the project.
- An account whose databases cannot be deleted stays in `deleted_pending` past
  its grace. The grace sweep logs `grace: external cleanup deferred` every tick
  until it clears.
- Rolling back the code leaves flagged accounts in `deleted_pending`. An older
  grace sweep cannot delete their databases, so it defers them as it does today.
  The migration is expand-only.

## Alternatives considered

- **Delete the databases when the account enters `deleted_pending`.** This is
  faster to stop cost, but a customer who pays back during grace would get their
  account back without its data. Rejected.
- **Pause provider compute at suspension and keep the data.** This caps cost but
  never reaches erasure. Rejected by the owner.
