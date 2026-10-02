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

The unmodified full-feature replay test still fails at the frozen initial
standards migration when all standards ledger entries are removed. This explicit
operational recovery path does not make default migration replay pass and does
not resolve that acceptance gate. Public standards activation also still needs
consumer verification and dedicated native acceptance.
