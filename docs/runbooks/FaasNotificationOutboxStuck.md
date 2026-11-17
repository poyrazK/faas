# Durable notification undelivered

`FaasNotificationOutboxStuck` fires when the oldest `pending` or `processing`
row in `notification_outbox` on one channel is more than 15 minutes old.
New rows open to replay within seconds and the replay retry backoff tops out
at five minutes, so a row this old is not being delivered.

## Symptoms

- `runtime_config_restart`: an environment, secret or managed PostgreSQL
  binding change is not applied; `gregale postgres bindings rotate --wait`
  hangs and later resizes or restores answer `managed_postgres_conflict`.
- `app_wake`: an explicit wake (CLI, API) waits and times out.
- Other channels: the snapshot, deployment or image handoff the channel names
  has not run.

## Triage

1. Find the rows (control-plane database, read-only):

   ```sql
   SELECT id, channel, state, attempts, created_at, available_at, claimed_by,
          left(last_error, 200) AS last_error
   FROM notification_outbox
   WHERE state IN ('pending', 'processing')
   ORDER BY created_at
   LIMIT 20;
   ```

2. Read `last_error` and `available_at`:
   - `app owned by another node` with `attempts` 0 and `available_at` moving
     forward: peers keep deferring the row and the owning schedd is not
     claiming it. Check the owner (`apps.node_id`) is up and its schedd
     replay worker runs (`db: durable notification outbox drain failed` in its
     log).
   - `attempts` rising with a handler error: the handler fails; fix the cause
     the error names. The row dead-letters after 12 attempts.
   - `processing` with an old `lease_until`: the claimer died; replay reclaims
     it after the lease.
3. Check the owning schedd's journal for the row's wake ID.

## Recovery

Do not delete rows to clear the alert: each carries an intent nothing else
retries. Fix the cause and the replay worker delivers the row within one poll
(2 s). The alert clears when `faas_notification_outbox_oldest_undelivered_seconds`
for the channel falls below 900.
