# neond_service

Installs `neond`, the control plane's Neon key holder (ADR-20261112160000).

`neond` holds the Neon API key and forwards apid's Neon API calls. It forwards
only the routes Gregale's Neon backend uses (`pkg/neond/routes.go`), refuses
to delete the Neon project or branch of a live managed database, and caps
destructive calls per hour. apid connects on `/run/faas/neond.sock`; neond
admits only the `faas-apid` OS user.

Off by default. To turn it on (control plane or single box):

1. Write the Neon key apid uses today to `/etc/faas/secrets/neon-admin.key`
   (owner root, mode 0400).
2. Set `faas_neond_enabled: true` and converge. The role:
   - creates the `faas-neond` user;
   - installs the unit and its drop-in (the key goes in through
     `LoadCredential`);
   - starts neond and waits for its socket.

   CD restarts neond from each new release.
3. Check `neond_requests_total` and the journal, then set
   `faas_neond_broker: true` and converge. apid gets a drop-in setting
   `FAAS_MANAGED_POSTGRES_NEON_BROKER=unix:///run/faas/neond.sock` and
   restarts; from then on every Neon call goes through neond.

To roll back, set `faas_neond_broker: false` (apid calls Neon with its own key
again), then `faas_neond_enabled: false`.

neond logs in to Postgres as `faas_neond`, through peer authentication as
`faas-neond`. That role is not a `faas_runtime` member: it can read
`managed_postgres_databases` and nothing else.

Slice 3 removes the key from apid's own configuration; until then apid still
holds it, so the broker narrows what an apid bug can do but not what a leaked
apid environment can.
