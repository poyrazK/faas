# Break-glass: app owner authority writes

ADR-20261110210000 §4. `app_owner_authority.mode` decides whether Postgres
admits an app owner change from a login outside `faas_owner_authority`. Only
the schema owner `faas` can write it: no daemon login can, by design, so
there is no typed interface. These are the only owner writes the
`FaasAppOwnerUnauthorizedWrite` runbook calls for.

## Entry conditions

1. Use the owner on the control plane (`sudo -u postgres psql faas` or an
   individually attributable owner session). Never a daemon login.
2. Record the change in the rollout or incident ticket, with the
   before state:
   `select mode, updated_at from app_owner_authority;`
3. Change one thing per transaction.

## Reviewed recipes

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';

-- Rollout step 2 (observe) or 5 (enforce); back out with 'observe' or 'any'.
UPDATE app_owner_authority
   SET mode = '<any|observe|enforce>', updated_at = now();

COMMIT;
```

```sql
BEGIN;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';

-- After an unauthorized owner change: clear one app's owner so the control
-- plane's claim re-places it. Re-run the app_owner_writes SELECT first.
UPDATE apps
   SET node_id = NULL
 WHERE id = '<verified-app-uuid>';

COMMIT;
```
