# neond, the Neon key holder

`neond` holds Gregale's Neon API keys and forwards apid's Neon calls under an
allow-list and a delete policy (ADR-20261112160000). With the broker on, apid
holds no Neon key. This runbook covers its alerts, cutting apid over to
neond and rolling back.

## Signals

| Metric | Meaning |
| --- | --- |
| `up{job="neond"}` | Prometheus reaches neond's `/metrics` (`faas_neond_metrics_addr`, default `127.0.0.1:9114`). |
| `neond_requests_total{route,tier,decision}` | Every call apid asked neond to forward. `decision` is `forwarded`, `refused`, `denied` (off the allow-list), `rate_limited` or `error`; `tier` is the key used: `project`, `admin` or `admin_fallback`. |
| `neond_project_keys_total{op,outcome}` | Project key operations: `mint`, `revoke`, `backfill`, `rotate`, `orphan_revoke`. |
| `neond_projects_without_key` | Projects of the organization without a project key after the last sweep. |

neond logs every call as JSON (route, project, branch, key tier, decision,
Neon status, latency) and never a key or a body. Neon keeps no audit log of
API calls, so neond's journal is the record:

```sh
journalctl -u faas-neond --since -1h -o cat | grep -v '"forwarded"'
```

## FaasNeondDown

With the broker on, every managed PostgreSQL create, delete, resize and cap
write fails as `unavailable`; customer databases keep running on Neon.

1. `systemctl status faas-neond` and `journalctl -u faas-neond -n 100`.
2. A start failure names its cause: the admin key credential
   (`/etc/faas/secrets/neon-admin.key`, root, 0400), `FAAS_NEOND_*`
   settings, the `faas_neond` database login, or the neon seal domain.
3. If neond cannot be brought back and managed PostgreSQL control is needed
   now, roll back (below). That puts the key back in apid's environment; cut
   over again once neond is fixed.

## FaasNeondRateLimited

apid asked for more destructive calls (project, branch, endpoint, snapshot or
role deletes) than `FAAS_NEOND_DESTRUCTIVE_PER_HOUR` (fleet, default 300) or
`FAAS_NEOND_DESTRUCTIVE_PER_SUBJECT_PER_HOUR` (one account, default 60)
allow. neond refused the excess; apid retries later.

1. Find the calls: `journalctl -u faas-neond --since -1h -o cat | grep 'rate limit reached'`.
   The `reason` names the account when the account limit refused.
2. Decide whether the deletes are expected: an account deleting many
   databases, snapshot retention after a bulk restore, or the plan-excess
   sweep. Check the account's managed PostgreSQL rows and apid's audit log.
3. Unexpected deletes are an incident: a loop in apid, or a compromised apid.
   Stop apid's managed PostgreSQL reconcilers (or apid) before anything else,
   then investigate. Do not raise a limit to clear the alert.
4. Expected and larger than the limit: raise
   `faas_neond_destructive_per_subject_per_hour` (or the fleet limit) for
   the window, converge, and set it back afterwards.

## FaasNeondRefusing

neond refused a call apid made:

- `denied`: the call is not on the allow-list (`pkg/neond/routes.go`). After
  a release this is a Neon backend change that missed its route; otherwise an
  attempt. The Neon backend's tests fail on an unlisted call, so a release
  should never send one.
- `refused`: a delete of a live database's project or branch, a non-legacy
  role delete, or a quota write or role delete on a project no
  `managed_postgres_databases` row names.

Read the `reason` in the `call refused` log line. A refused delete of a live
database's project means apid's state disagrees with the database rows: find
which row is live and why apid wanted the project gone before retrying
anything by hand.

## FaasNeondProjectKeysFailing

Minting, revoking or rotating project keys fails. Calls on projects without a
usable key go out with the admin key (`tier="admin_fallback"`), so customers
are not affected, but the protection project keys give is lost.

1. `journalctl -u faas-neond -o cat | grep 'project key'`.
2. `401`/`403` from Neon: the admin key is not an organization Admin's
   personal key. Minting project keys needs the service user's key.
3. A seal error: the neon seal domain identity is missing
   (`/etc/faas/secrets/domains/neon.age`; the seal_domains role).
4. A database error: `neon_project_keys` and the `faas_neond` login.

## FaasNeondBackfillStalled

Projects have stayed without a key for over two hours. The sweep mints at
most 50 keys a pass, every 10 minutes, and does nothing when Neon's project
listing is incomplete. Check `FaasNeondProjectKeysFailing` and the
`key sweep` log lines.

## Cut apid over

Prerequisites: neond is on (`faas_neond_enabled`), `neond_requests_total` is
being scraped, and the admin key in `/etc/faas/secrets/neon-admin.key` is
the dedicated Neon service user's (an organization Admin that belongs to no
person), not a person's key.

1. Remove the Neon key line (`FAAS_NEON_API_KEY`, the variable the managed
   PostgreSQL config's `secret_env` names) from `/etc/faas/sealed.env`. Do
   not restart apid yet: it keeps the environment it started with.
2. Set `faas_neond_broker: true` and converge. The role refuses while
   `sealed.env` still sets the key; apid refuses to start with both.
3. Watch `neond_requests_total{decision="forwarded"}` rise and apid's managed
   PostgreSQL reconcile metrics stay healthy.
4. Revoke the key apid used to hold in the Neon console, unless it is the
   key neond now holds.

## Roll back

1. Put the Neon key back in `/etc/faas/sealed.env`.
2. Set `faas_neond_broker: false` and converge: the role removes apid's
   drop-in and restarts apid, which calls Neon with its own key.
3. Leave neond running unless it is the problem (`faas_neond_enabled: false`
   stops it).
