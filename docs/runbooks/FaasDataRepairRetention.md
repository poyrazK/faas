# Repair payload retention

Use the local maintenance CLI against the intended application database. Start
with a disposable application database and review the scope file and connection
environment variable before using a qualified installation. Executor tokens and
hosted customer credentials do not authorize this operation.

## Install or upgrade

Fresh `gregale data-repair schema` installations include retention. For an
existing installation, have the `gregale_repair` schema owner review and apply:

```sh
gregale data-repair schema --upgrade > repair-retention-upgrade.sql
psql "$GREGALE_REPAIR_DATABASE_URL" -X -v ON_ERROR_STOP=1 -f repair-retention-upgrade.sql
```

The upgrade is transactional and repeatable. It preserves table enrollment,
row identities/versions, schema fingerprints and existing approvals/receipts.
Coordinate this owner DDL with application traffic. Never rerun enrollment or
reset versions to install retention.
The original `gregale data-repair retention schema` command remains compatible.

An owner may grant a dedicated trusted maintenance role just these entry points:

```sql
GRANT USAGE ON SCHEMA gregale_repair TO repair_maintenance;
GRANT EXECUTE ON FUNCTION
  gregale_repair.prepare_retention(jsonb,timestamptz,integer),
  gregale_repair.get_retention_plan(uuid,jsonb),
  gregale_repair.apply_retention(uuid,jsonb,text,text) TO repair_maintenance;
```

No direct table DELETE grant is needed. A function-only maintenance role can
purge all scopes in that database, so keep it separate from application writers
and ordinary executor roles.

## Review and purge

Use the existing fixed scope JSON containing account ID, app ID, environment and
optional platform tenant ID. The connection comes from
`GREGALE_REPAIR_DATABASE_URL`, or the name supplied by `--database-env`.

```sh
gregale data-repair retention plan --scope-file repair-scope.json --limit 32
gregale data-repair retention get RETENTION_PLAN_ID --scope-file repair-scope.json
gregale data-repair retention purge RETENTION_PLAN_ID --scope-file repair-scope.json \
  --digest REVIEWED_DIGEST --actor operator@example.com --yes
```

Save the inventory ID and digest before purge. Inspect its scope, cutoff, exact
entries, counts and estimated bytes. Inventories contain no captured row keys,
field values or executable patches. They expire after 15 minutes. A cutoff
defaults to seven days ago on the database clock; `--before RFC3339` can retain
data longer. There is no force option or shorter minimum.

An applied old plan can be compacted while its ID, counts and receipt remain.
An expired, never-approved plan can also be compacted. A superseded parent's
entry has reason `superseded_plan` and identifies its verified saved replacement
with `replacement_plan_id`, `replacement_plan_digest` and `superseded_at`. Both
parent creation and replacement creation must precede the cutoff: an old
approval followed by a new replacement does not shorten the retention minimum.
The source fence and child integrity, scope, source, lineage and comparison are
verified again before purge. Child approval/application does not transfer to
the parent or change the replacement's original expiry.

Inspect `holds`, a bounded list of `{kind, reason, count}` groups. These are the
hold reasons at the saved inventory's cutoff; only `entries` authorize purge.

| Kind | Reason | Meaning |
|---|---|---|
| plan | `unresolved_apply` | Approved with no receipt, closure or saved replacement. Follow [apply recovery](FaasDataRepairStuck.md); age alone cannot establish rollback. |
| plan | `closed_without_replacement` | Apply is closed, but captured values are still needed to prepare a replacement. |
| plan | `replacement_retention_minimum` | The parent or saved replacement is newer than the cutoff. |
| plan | `replacement_unverified` | The replacement pointer has no matching child in the same scope/source, or the parent also has a receipt. Investigate owner metadata; do not edit it to force purge. |
| plan | `receipt_retention_minimum` | Plan creation or committed receipt is newer than the cutoff. |
| plan | `unapproved_plan_not_expired` | Plan creation or unapproved expiry is newer than the cutoff. |
| operation | `plan_payload_required` | An uncompacted plan still references this old journal. |

`held_approved_plans` retains the count of approved, unapplied plans currently
held. Eligible superseded parents are excluded. A corrupt selected parent/child
document or invalid child lineage/comparison rejects preparation or the whole
purge; a fresh inventory cannot bless that evidence with a new digest.

Every uncompacted plan pins both its source and any inverse operation journals.
Compacting a parent does not release a journal still needed by its replacement.
Run a new inventory after plan compaction to review newly eligible journals.
Keep reviewing bounded batches until the intended backlog is gone. An empty
inventory does not prove all payloads are gone: age, holds and batch bounds may
exclude remaining records.

If inventory state changes, purge rejects the entire transaction. Inspect a new
inventory and explicitly review its new digest. Do not edit a saved inventory
or reuse its approval for a broader selection. Lock contention also fails within
the transaction budget without skipping entries.

## Lost responses and verification

If purge reports an unknown COMMIT outcome, run `retention get` with the saved
ID and scope. An `applied_at` receipt settles the result. Any retry must use that
same inventory ID and digest. Repeating a completed purge returns its original
actor, role and time without another mutation, including after expiry.

Use normal repair `get` to inspect the compact receipt. `payload_purged_at`
explains the empty items array; candidate/conflict counts and the original apply
receipt remain. Replaying an applied repair requires its original plan digest
and repair operation ID and returns the receipt without writing again. Preparing
a new repair from a purged operation returns HTTP 410 `repair_payload_purged`.

For a superseded parent, normal repair `get` retains its original approval,
check history, `apply_resolution` and `replacement_plan_id`. Replaying `resolve`
with the original identity returns the same closure; replaying `replace` with
the saved child UUID returns that child, including any later receipt. Other
child UUIDs and old/delayed parent applies remain blocked after compaction.
An expired replacement stays expired; prepare a further linked replacement if
needed, with its own review, rehearsal and approval.

For a committed repair, `data-repair verify PLAN_ID --digest PLAN_DIGEST` can
still observe repaired records after plan compaction while its inverse journal
remains. After journal purge it returns `available=false` and `journal_purged`,
with the historical restored/excluded counts intact and no current row counts.
The CLI prints that unavailable report and exits one. Use normal receipt
recovery to inspect the commit; verification does not rewrite it or reconcile
an unknown Operation outcome.

Purge never changes business rows, enrollment or monotonic versions. It never
deletes native task/producer/launch journals, alters task phases or releases
capacity. Preserve unresolved native recovery evidence under the existing
retirement protocol. Logical deletion does not erase MVCC copies, WAL, backups
or prior exports; manage those using their own storage policies.
