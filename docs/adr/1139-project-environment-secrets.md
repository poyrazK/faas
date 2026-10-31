# ADR-1139: Project environment secrets reach every service of the environment

- Status: Accepted
- Date: 2026-10-21
- Amends: ADR-1136 (§7 deferred project-level secrets to this decision)
- Related: ADR-020 (customer secrets), ADR-053 (deployment secret grants),
  ADR-092 (scoped secrets), ADR-178 (fleet seal domain), ADR-568 (Git-owned
  environment intent and secret references), ADR-590 (environment clones),
  ADR-20261019120021 (envelope rotation and key retirement)

## Context

ADR-1136 made a project environment's non-secret configuration reach every
service of that environment, and deliberately left credentials per app. A
four-service project that shares a database password, a payment API key or a
signing secret still sets it four times with `gregale secrets set`, again in
every environment, and again for every service it adds. Rotating it means one
write per service, and a missed service keeps the old value.

App secrets are age envelopes sealed to the fleet recipient (ADR-178). The
envelope holds only `{KEY: value}` and is not bound to an app or scope, which is
why environment clones can already copy ciphertext between scopes. vmmd opens
envelopes at boot and merges them in order, and an entry with a `SourceKey`
must hold exactly that one key. Sealing at project scope therefore needs no new
cryptography. The work is in keeping project values out of the structures that
assume every secret row belongs to the deploying app: delivery tracking,
runtime receipts, the per-app secret fence and the freshness SQL.

## Decision

### 1. Storage

A new table, `project_environment_secrets`, holds one row per
`(project_id, environment_slug, key)`: the age envelope sealed to the fleet
recipient with `secretbox.SealOne` (exactly one key), its `kid`, its
`value_hash` (the HMAC fingerprint app secrets already use), a `secret_version`
that increases on every value change, and timestamps. Keys follow the
environment-variable rule app secrets use and may not be platform identity
keys. Values are write-only: no API, log or audit event returns plaintext or
ciphertext.

The environment is named by slug, as the configuration document is. Deleting
the environment deletes its secrets in the same transaction, including clone
environment cleanup, so a later environment with the same slug never inherits
them. Deleting the project or account cascades.

### 2. Which deployments inherit them

A deployment inherits the secrets of the project environment named by its
scope, with `default` reading `production`: the ADR-1136 mapping. Apps outside
a project inherit nothing. The following inherit nothing either:

- deployments that declare an explicit secret selection (`env_secrets`,
  ADR-053), so a least-privilege grant stays exactly what it lists;
- GitOps-managed scheduled Jobs, which run only their reviewed definition
  (ADR-568), as with project configuration;
- sidecars, which have their own grants.

Release tasks inherit them like the main process: a migration usually needs the
shared database credential.

### 3. Precedence

Anything an app sets itself wins over anything it inherits. A project secret is
not delivered to an app that has a plain variable, a secret or a secret
reference with the same key in that scope. Overall, from lowest to highest:
manifest environment, project configuration, project secrets, the app's own
variables, the app's own secrets and references, platform identity. (The guest
places every secret above every plain variable; dropping a shadowed project
secret before delivery is what keeps an app's own plain variable above it.)

### 4. Delivery

`ReadRuntimeAppValuesForDeployment` reads the inherited secrets in the same
statement as the app's own values, so the runtime value snapshot carries them
and every boot path that uses it delivers them. schedd sends each one as a
sealed entry with `SourceKey` set to its key, ahead of the app's own entries.
vmmd and the guest are unchanged: vmmd already requires such an envelope to
hold exactly that key, and later entries already win.

### 5. Changes and freshness

A trigger on `project_environment_secrets` stamps a runtime-configuration change
for every app of the project in the environment's scope and marks those
snapshots stale, exactly as ADR-1136's configuration trigger does, including
the `default:project-config` boundary when the environment is `production`. It
fires on insert, delete and a value change. A master-key reseal, which changes
only `kid` and ciphertext, does not fire it.

The runtime configuration fence includes the inherited secrets' keys, versions
and value fingerprints when there are any, so a warm, paused or frozen instance
whose inherited secrets changed boots again instead of resuming with old
values. With none inherited, the fence is computed exactly as before. Inherited
secrets are not runtime-receipt inputs and get no per-app delivery tracking;
the scope stamp covers them, as it covers inherited configuration.

Running instances keep the values they booted with until they restart. A
rotation (`rotate`) is a new value with a new version; it reaches each service
at its next boot.

### 6. Master-key rotation and retirement

Project secrets join the reseal and retirement machinery of
ADR-20261019120021: the `release_secret_rekey_documents` view lists them as
kind `project`, the reseal worker swaps their envelopes with the same
compare-and-swap discipline as app secrets, key-usage counts include them as
live secrets, and the retirement guard rejects a write sealed to a retired or
unlabelled key. A fleet key can therefore not be retired while a project secret
still depends on it.

### 7. Environments, clones and promotion

An environment clone copies the source environment's project secrets as it
copies app secrets. A durable (checkpointed) clone cannot carry them yet, so
its capture refuses a source environment that has any rather than producing an
environment whose services boot without them. A promotion does not copy them:
the destination keeps its own, as it does for app secrets.

A qualification records a revision hash of the environment's project secrets
(keys and versions) beside the per-app hashes, under the key
`project:secrets`, which no workload slug can take. The server adds it when it
stores the qualification, so clients keep sending per-app hashes only, and it
is present only when the environment has project secrets, so qualifications
recorded earlier stay comparable. A promotion refuses when a source project
secret changed after qualification.

### 8. API, permissions and limits

- `GET /v1/projects/{slug}/environments/{environment}/secrets` lists keys,
  versions, `kid`, value fingerprints and timestamps.
- `PUT …/secrets/{key}` sets a value, `POST …/secrets/{key}/rotate` replaces it,
  and `DELETE …/secrets/{key}` removes it.
- Writes are MFA-gated, require the `secrets:write` (or `admin`) API scope and
  the new organization action `org.manage_project_secrets` (owner, admin and
  developer, matching `org.manage_app_secrets`).
- Audit events: `project.secret.set`, `project.secret.rotated` and
  `project.secret.deleted`, with key, environment, version and `kid`.
- `gregale projects environments secrets list|set|unset|rotate`.
- `ProjectSecretCountMax` per project environment and plan, in
  `pkg/api/limits.go`; a value's size limit is the plan's `SecretValueMaxBytes`.

### 9. Rollout beside release N-1

- **Data.** The table, its triggers and the rekey view change are additive. An
  empty table changes nothing for N-1 code.
- **Control plane.** apid serves the API. An N-1 schedd delivers no project
  secrets. The fence includes them only when present, so an instance booted by
  one version and resumed by the other boots again instead of resuming with a
  different secret set.
- **Node and runtime.** Nothing: entries use the existing `SourceKey` contract.

## Consequences

- One `gregale projects environments secrets set` configures a credential for
  every service of an environment, including services added later, and one
  `rotate` changes it everywhere at each service's next boot.
- A change to a project secret cold-boots member snapshots in that environment
  once. Snapshots are a cache (ADR-005); the cost is one boot.
- Deployments with an explicit `env_secrets` selection do not see project
  secrets. A future reference form (`project-secret:NAME`) can grant one by
  name; until then such deployments set the value as an app secret.
- Live in-process reload (ADR-222) does not cover inherited secrets yet; they
  apply on restart or the next wake.

## Rejected alternatives

- **Copying a project secret into every member app's `app_secrets`.** It reuses
  delivery as is, but a member added later misses it, every write fans out to N
  rows that can drift, and app-level overrides become indistinguishable from
  copies.
- **Secret-shaped keys in the configuration document.** The document is stored
  in plain JSON, versioned and diffable, and ADR-1136 rejects secret-shaped
  keys on purpose.
- **Recording inherited secrets in runtime receipts.** The freshness SQL compares
  receipts with `app_secrets` rows exactly; an inherited key would never
  match. The scope stamp already gives the same guarantee.
- **Inheriting into explicit `env_secrets` selections.** It would turn a
  least-privilege grant into "these plus everything shared".
