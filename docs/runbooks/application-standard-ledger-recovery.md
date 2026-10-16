# Reviewed application-standard ledger recovery

This operator command handles missing ledger entries for the exact frozen
application-standard migration sources listed in the recovery manifest. It
requires the complete expanded schema already to exist and verifies the
enrollment and materialized-field backfills. It appends current recovery events
and an immutable receipt; it does not recreate historical application, rollout
or native admission events. Ordinary daemon startup never invokes recovery.

Run locally on PostgreSQL 16 through a Unix socket, using the database's owning
role and PostgreSQL 16 `pg_dump` on `PATH`. Obtain a verified database backup
and keep ordinary migrations and manual DDL stopped for this maintenance
operation. The migration advisory lock fences cooperative migrators, while
apply locks the ledger and enrollment tables against concurrent writes.

Use the same reviewed source build for all three steps:

```sh
export DATABASE_URL='postgresql:///gregale?host=/run/postgresql&user=faas'
migrate -prepare-ledger-recovery
migrate -ledger-recovery-plan > recovery-plan.json
```

Prepare executes only the additive recovery-audit migration through Goose. It
does not fill the historical gaps. It is needed when an earlier frozen migration
prevents ordinary upgrade from reaching the audit migration. Repeating prepare
does not append another successful audit-migration event.

Review the plan's database/cluster/actor identity hash, complete schema hash,
embedded source hash, exact ledger hash, application count and repair list. Each
repair entry identifies its immutable filename, version, SHA-256 and checked
postcondition. Missing migrations outside the reviewed manifest appear under
`remaining`; recovery never records those as applied.

After approving that exact plan, pass its `approval_hash`:

```sh
migrate -ledger-recovery-apply APPROVED_SHA256 > recovery-receipt.json
migrate -status
```

Apply rereads the inputs under its locks and refuses a changed plan. All current
recovery events and the immutable receipt commit in one transaction. Cancellation
or a failed receipt insert rolls back the ledger events. Repeating the exact
successful approval returns its original receipt after checking its recorded
events; it does not produce another repair or fresh authority. Another database,
cluster or acting role cannot reuse the approval.

Unknown migration history, missing legacy entries, a rolled-back latest event,
schema drift, changed object owners/privileges, incomplete backfills and a stale
canonical source binding refuse recovery. Investigate those failures before
creating a new plan. Schema and credential bodies do not appear in the plan or
receipt. Credentials reach `pg_dump` through its child environment.

The reviewed manifest currently covers 80 frozen standards migrations, including
measured restore, promotion and serving-parent capture. The complete 81-migration
PR replay check uses reviewed repair for those exact sources and ordinary Goose
replay for the separate audit migration. A PostgreSQL regression also removes
the complete frozen ledger from an application with a published measured
promotion, repairs it, and verifies that inherited settings, residency, admission
grants, receipts and snapshot catalog history remain unchanged.

A new frozen migration must be reviewed and added to the manifest explicitly.
Its filename or prefix never authorizes recovery. Ordinary startup continues to
refuse missing frozen entries until the exact reviewed repair is applied. Public
standards activation still needs consumer verification and dedicated native
acceptance.
