# ADR-20261112160000: Neon API keys are held by neond, which enforces what apid may ask

- **Status:** accepted (design; implementation lands in slices). Owner decisions 2026-11-12: "All about equally" (threats), "Yes, new daemon", "Daemon holds all, enforces policy", "Dedicated service user", off-Neon backups "Not needed".
- **Date:** 2026-11-12
- **Owner:** managed PostgreSQL and control-plane maintainers
- **Related:** ADR-20261109170000 (signd: a key held by its own daemon), ADR-20261022111812 (seal domains), ADR-20261023104734 (per-daemon database roles), ADR-20261110160000 (managed PostgreSQL egress and quotas)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

- **apid holds one Neon key for the whole organization.** The Neon backend
  reads it from the environment variable its backend config names
  (`pkg/managedpostgres/neon/config.go`, secret `api-key`), and every Neon
  request carries it as a bearer token (`doJSON` in
  `pkg/managedpostgres/neon/client.go`).
- **That key can do anything to every customer database.** It creates and
  deletes projects, deletes branches and snapshots, resets passwords and
  changes quotas. Three threats were weighed equally:
  - the key leaking from apid (environment, crash dump, a compromised
    control-plane host);
  - a bug or exploit in apid driving harmful Neon calls;
  - an operator mistake with the key.
- **Neon's keys** (neon.com/docs/manage/api-keys, read 2026-11-12):
  - A personal key covers the projects its user can reach; an organization
    key covers the organization; a **project-scoped** key covers one project
    with Editor access.
  - A project-scoped key cannot create or delete projects or mint keys. It
    can still delete branches and snapshots, manage roles and passwords, and
    change project settings, quotas included.
  - Minting an organization or project-scoped key needs the **personal key
    of an organization Admin** (`POST /organizations/{org_id}/api_keys` with
    `project_id`). Keys never expire; the secret is returned once;
    revocation is immediate. Neon keeps no per-call audit log, only
    `last_used_at` on each key.
  - Whether a project-scoped key can read consumption metrics is not
    documented.

## Decision

### 1. A daemon holds every Neon key

`neond` is a new control-plane daemon. It holds:

- **The admin key:** the personal API key of a dedicated Neon service user,
  an organization Admin that belongs to no person. It is the only key that
  creates and deletes projects, mints and revokes project keys, and reads
  organization-wide consumption.
- **A project key per Neon project:** minted by neond right after it creates
  the project, sealed with neond's own sealing identity, and stored in
  `neon_project_keys` (project ID, Neon key ID, sealed key, created and
  revoked times), which only neond's database role may read or write.

apid holds no Neon key. It talks to neond over gRPC on a unix socket,
`/run/faas/neond.sock`. The socket is owned by `faas-neond` and group
`faas-apid` with mode 0660, and neond also checks the peer's uid with
`SO_PEERCRED`.

### 2. apid forwards requests; neond decides

apid keeps all Neon logic. `doJSON` gains a broker transport: instead of
calling Neon with a key, it sends `Forward{method, path, query, body,
accepted}` and neond returns `{status, body}`. Nothing else in the Neon
backend changes, and the same `httptest` fixtures keep testing it.

neond, for each request:

1. **Matches an allow-list** of method and path templates: the calls the
   Neon backend makes today (projects, branches, endpoints, roles,
   databases, operations, snapshots, consumption history, quotas). A call
   outside it is refused with `PERMISSION_DENIED` and logged.
2. **Picks the key.** Calls under `/projects/{id}/…` use that project's key.
   `POST /projects`, `DELETE /projects/{id}`, `/consumption_history/…` and
   key management use the admin key. A project without a key yet (created
   before slice 2) uses the admin key and is logged until backfilled.
3. **Applies the destructive-call policy**, reading apid's rows through a
   read-only role:
   - `DELETE /projects/{id}` only when no managed PostgreSQL database in a
     live state (anything but `deleting` or `deleted`) has its provider
     resource in that project. Failed creates, copy targets and deleted
     databases pass; a live customer database cannot be deleted by apid.
   - `DELETE …/branches/{branch}` never for a branch a live database runs
     on, nor for a project's default branch while the project is live.
   - Password resets, role deletes and quota writes only on projects a
     database row names.
   - Destructive calls (project, branch and snapshot deletes) are rate
     limited per hour, fleet-wide per neond and per subject. Over the limit
     neond refuses and alerts, so a loop or an exploit stops early.
4. **Logs every call** as JSON (method, path template, project, key tier,
   decision, Neon status, latency), never a key or a body, and counts it in
   `neond_requests_total{route,tier,decision}`. Neon keeps no audit log, so
   this is the record.

### 3. Keys over a project's life

- **Create:** after `POST /projects` succeeds, neond mints the project key
  in the same forward and stores it before replying. If minting fails, the
  project still works on the admin-key fallback and a sweep retries.
- **Delete:** after `DELETE /projects/{id}` succeeds, neond revokes the
  project key and marks the row revoked.
- **Backfill:** a neond sweep mints keys for projects that have none, at
  most a few per second (Neon limits key creation to 10 per second).
- **Rotation:** `gregalectl neond rotate-project-keys` re-mints project keys;
  the admin key is rotated by an operator in the service user's Neon
  account and handed to neond as a new credential.

### 4. What this does and does not protect

- A leak of apid's environment or memory no longer reveals any Neon key.
- An exploited apid can still make the allowed calls, so it can disrupt
  live databases (suspend, resize) but cannot delete a live database's
  project or branch, and destructive calls stop at the rate limit.
- A leaked project key exposes one project. A leaked admin key exposes the
  organization, so it lives only in neond's systemd credential, never in an
  environment file.
- The owner chose no backups outside Neon: a compromised neond host or a
  Neon-side loss is not recoverable beyond Neon's own history window. That
  risk is accepted.

## Implementation

- **Slice 1, neond with the admin key only (off until configured):**
  `api/proto/onebox/faas/neond/v1` (`Forward`), `pkg/neond` (allow-list,
  policy, rate limits, client), `cmd/neond`, its database role
  `faas_neond` (read on `managed_postgres_databases`), and the broker
  transport in the Neon backend behind `FAAS_MANAGED_POSTGRES_NEON_BROKER`
  (socket path). With it unset, apid calls Neon directly as today.
  - Built as slice 1a (code, off until configured). As built:
    - The allow-list is `pkg/neond.Routes`: 24 method and path shapes. The
      Neon backend's tests record every request they send and fail when
      one does not match, so a new Neon call needs its route first.
    - Destructive calls are project, branch, endpoint, snapshot and role
      deletes. A role delete is allowed only for the legacy per-identity
      API roles (`gregale_` and 40 hex digits); the owner role never.
    - The hourly limit is fleet-wide per neond
      (`FAAS_NEOND_DESTRUCTIVE_PER_HOUR`, default 300). The per-subject
      limit and the "only on projects a database row names" rule for
      password resets and quota writes move to slice 2, when neond maps
      projects to subjects for their keys.
    - neond reads the admin key from `FAAS_NEOND_ADMIN_KEY_FILE` (a
      systemd credential) and admits only the OS users named in
      `FAAS_NEOND_CALLERS`, by `SO_PEERCRED`. Off Linux it admits none.
    - apid reads `FAAS_MANAGED_POSTGRES_NEON_BROKER` from its process
      environment; set, the Neon backend needs no key.
  - Slice 1b (deploy): the `faas-neond` user, unit, credential drop-in,
    the `faas_neond` database role and the release binary list. As built:
    - The `neond_service` role, off by default. `faas_neond_enabled`
      installs the drop-in, which hands `/etc/faas/secrets/neon-admin.key`
      (root, 0400 or 0600) to neond through `LoadCredential` and admits
      `faas-apid`. A separate `faas_neond_broker` writes apid's
      `FAAS_MANAGED_POSTGRES_NEON_BROKER` drop-in, so operators turn neond
      on, watch `neond_requests_total`, and only then move apid's calls
      over. The role refuses the broker without neond or before the active
      release ships the binary.
    - `faas_neond` is a login that is not a `faas_runtime` member; it has
      `SELECT` on `managed_postgres_databases` only (pg_ident
      `faas-neond` → `faas_neond`).
    - CD bundles the unit and restarts neond from each release when the
      drop-in exists, waiting for `/run/faas/neond.sock`.
- **Slice 2, project keys:** `neon_project_keys` (expand migration), the
  service user's key as neond's admin credential, mint on create, revoke on
  delete, the backfill sweep, rotation.
  - Built as slice 2a (mint, use, revoke; off until `faas_neond_org_id` is
    set). As built:
    - `neon_project_keys` is keyed by Neon's key ID, with one live key per
      project (partial unique index) and revoked rows kept as the record of
      what was issued. `faas_runtime` has no access; `faas_neond` may
      select, insert and update. Queries live in `pkg/neond/neondpg`.
    - Keys are sealed with `secretbox.SealBytes` (namespace
      `neon-project-key`) to a new seal domain, `neon` (ADR
      20261022111812), whose only reader is neond on the control plane. The
      seal_domains role stages `/etc/faas/secrets/domains/neon.age`, hands
      it to neond through `LoadCredential`
      (`FAAS_SEAL_DOMAIN_NEON_IDENTITY_PATH`) and proves the domain probe;
      neond loads it with `sealdomain.LoadReaderIdentities`, so a rotation's
      previous identity keeps old keys readable.
    - Minting runs after a successful `POST /projects`, detached from apid's
      deadline, before the reply. A key minted but not stored is revoked at
      once. A failed mint leaves the project on the admin key.
    - A project route goes out with the project key. With no key, a key
      that will not open, or a 401/403 from Neon (the key is forgotten and
      the call retried once), it goes out with the admin key, counted as
      `tier="admin_fallback"`.
    - A successful (or 404) `DELETE /projects/{id}` revokes the project's
      live keys and marks them revoked; `neond_project_keys_total{op,outcome}`
      counts mints and revocations.
  - Built as slice 2b (the key sweep). As built:
    - Every 10 minutes neond lists the organization's projects with the
      admin key (`GET /projects?org_id=…`, all pages). A listing with
      unavailable projects or more than 25 pages stops the pass, so a
      partial view never revokes a key.
    - It mints a key for each project without one, revokes the keys of
      projects Neon no longer lists, and rotates keys older than
      `FAAS_NEOND_PROJECT_KEY_MAX_AGE` (default 90 days): at most 50 key
      calls a pass, 250 ms apart (Neon admits 10 key creations a second).
      The backfill covers every project of the organization, not only those
      a database row names, because the rows do not record the backend.
    - Rotation mints the new key, revokes the old one at Neon, then retires
      the old row and inserts the new one in one statement
      (`ReplaceProjectKey`). A failure before the swap revokes the new key;
      a failed swap leaves the old row naming a revoked key, whose calls
      fall back to the admin key until the next pass rotates it again.
    - Deviation: rotation is the sweep's, by age, not a
      `gregalectl neond rotate-project-keys` command. neond alone holds the
      admin key, the seal identity and the table, so an operator command
      would need its own path into neond. To rotate every key now, lower
      `faas_neond_project_key_max_age` and converge.
    - `neond_projects_without_key` is the backfill still to do;
      `neond_project_keys_total{op}` gains `backfill`, `rotate` and
      `orphan_revoke`.
  - Slice 2c: the per-subject destructive limit and the "only on projects a
    database row names" rule for password resets, role deletes and quota
    writes.
- **Slice 3, apid without the key:** apid refuses to start with both the
  broker and a Neon key configured; deploy drops the key from apid's
  credentials; alerts on refusals, rate limits and neond being down; a
  runbook. Before this slice ships, the owner creates the Neon service user.

## Consequences

- One more control-plane daemon: if neond is down, managed PostgreSQL
  requests fail (`unavailable`) and the cap sweep skips that pass; Neon
  keeps running the databases. systemd restarts it; a probe and an alert
  cover it.
- Every Neon call gains a local unix-socket hop, well under a millisecond.
- New Neon calls need an allow-list entry, which a test keeps equal to the
  calls the Neon backend makes.
- Rejected: per-database keys held by apid (apid could still delete every
  branch it can unseal); a single organization key in neond without project
  keys (one bug in a routine path could reach any project); a person's own
  Admin key (tied to a login and its access).
