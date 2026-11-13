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

## Project keys

With `faas_neond_org_id` set to the Neon organization ID, neond mints a Neon
API key scoped to each project it creates (`POST
/organizations/{org}/api_keys` with `project_id`), seals it to its own
identity and stores it in `neon_project_keys`, which only `faas_neond` can
read. Calls on that project then go out with its key; admin routes (create,
list and delete projects, consumption) keep the admin key. When the admin
key mints keys it must be the personal key of an organization Admin (the
dedicated service user). A project without a key, or whose key Neon
refuses, falls back to the admin key (`neond_requests_total{tier="admin_fallback"}`).
Deleting a project revokes its keys (`neond_project_keys_total`).

A key sweep runs every 10 minutes. It mints keys for projects without one
(projects created before project keys, or whose mint failed), revokes the
keys of projects Neon no longer lists, and rotates keys older than
`faas_neond_project_key_max_age` (default 90 days), at most 50 key calls a
pass. `neond_projects_without_key` is the backfill still to do. A pass that
cannot list every project does nothing.

Keys are sealed to the `neon` seal domain (ADR 20261022111812), which only
neond reads. Before setting `faas_neond_org_id`, create the domain with
`gregalectl seal-domain init --domain neon`, set
`faas_seal_domain_neon_identity_src` (and `faas_seal_domains_managed`) and
converge: the seal_domains role stages `/etc/faas/secrets/domains/neon.age`,
hands it to neond and proves the domain probe. This role refuses project
keys without it. Losing the identity leaves projects on the admin key until
their keys are re-minted.

To roll back, set `faas_neond_broker: false` (apid calls Neon with its own key
again), then `faas_neond_enabled: false`.

neond logs in to Postgres as `faas_neond`, through peer authentication as
`faas-neond`. That role is not a `faas_runtime` member: it can read
`managed_postgres_databases` and nothing else.

Slice 3 removes the key from apid's own configuration; until then apid still
holds it, so the broker narrows what an apid bug can do but not what a leaked
apid environment can.
