# Break-glass database repair

Direct production database writes are an emergency recovery mechanism, not a
normal operator interface. Use this page only when a typed API/intent cannot
make progress and delaying action would increase customer harm.

## Entry conditions

1. Open an incident and record the affected resource IDs.
2. Use an individually attributable, time-limited database role. Never use a
   shared daemon credential.
3. Obtain a second operator's approval when one is reachable.
4. Confirm the latest backup/PITR point and capture the read-only before state.
5. Set a short statement and lock timeout. Change one identified resource in
   one transaction; never use an unbounded predicate.

## Reviewed recipes

These statements are templates. Replace every placeholder and re-run the
matching `SELECT` before committing.

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';

-- Reactivate one node only after transport health is independently proven.
UPDATE compute_nodes
   SET lifecycle = 'active'::compute_node_lifecycle
 WHERE id = '<verified-node-uuid>'
   AND lifecycle = 'unavailable'
RETURNING id, name, lifecycle;

COMMIT;
```

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';

-- Extend one preview while a typed preview-extension operation is absent.
UPDATE apps
   SET preview_expires_at = now() + interval '24 hours'
 WHERE id = '<verified-preview-app-uuid>'
   AND preview_of_slug <> ''
   AND preview_pr_state IN ('open', 'closed')
RETURNING id, slug, preview_expires_at;

COMMIT;
```

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';

-- Last resort after the migration watchdog and node intent both failed.
DELETE FROM instances
 WHERE id = '<verified-instance-uuid>'
   AND state = 'migrating'
RETURNING id, app_id, deployment_id, node_id, state;

COMMIT;
```

Migration-ledger repair requires verifying the exact migration file checksum
against the release artifact before recording it as applied:

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
INSERT INTO goose_db_version (version_id, is_applied)
VALUES (<verified-version>, true)
ON CONFLICT DO NOTHING;
COMMIT;
```

Emergency index rollback is similarly one named object only:

```sql
DROP INDEX CONCURRENTLY IF EXISTS events_wake_id_idx;
```

## Exit conditions

- Capture the returned row and the read-only after state in the incident.
- Verify customer recovery and controller convergence.
- Revoke the temporary role/session.
- File a follow-up typed operation for any recipe used. A repeated
  break-glass action is an operator-product bug.

