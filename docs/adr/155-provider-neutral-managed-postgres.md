# ADR-155 · Provider-neutral managed PostgreSQL foundation

- **Status:** accepted as a control-plane foundation with a dark-wired Neon adapter and recoverable credential binding; no public entitlement until billing guardrails, backups, live provider qualification, and a support runbook are complete.
- **Date:** 2026-09-05
- **Decision:** make managed PostgreSQL an account resource with app-scoped bindings, behind a provider-neutral lifecycle and metering boundary.

## Context

Gregale's stateless compute can already run applications that connect to an
external PostgreSQL database. Making PostgreSQL part of the Gregale product
adds provisioning, deletion, credentials, recovery expectations, usage, and
provider cost exposure. The first upstream may be Neon or another managed
operator, but changing providers later must not require a new customer API or
a rewrite of app configuration.

SQL compatibility alone is not enough. Provider APIs differ in their project
hierarchy, asynchronous operations, connection pooling, compute units, active
time, operations, branching, backups, regions, and credential lifecycle. The
control plane therefore cannot persist an upstream plan name or treat an
upstream connection URL as the database identity.

## Decision

Gregale owns the logical `Database` resource. Its portable specification uses
a Gregale region, PostgreSQL major version, the Gregale service classes
`development`, `burstable`, and `production`, an availability promise,
scale-to-zero preference, storage ceiling, and restore window. Each adapter
maps that promise to provider settings and rejects unsupported combinations
before an upstream resource is created.

The database is account-scoped. An app consumes it through a separate binding,
which carries app ID, environment scope, environment key (normally
`DATABASE_URL`), provider credential identity, and an opaque credential
reference. This permits one database to serve multiple apps without making an
app the owner of the database and permits credentials to be rotated or revoked
per binding.

`managedpostgres.Provider` is the vendor boundary. It covers capabilities,
provision/restore/inspect/update/delete, credential issuance and revocation, and usage. Resource IDs
are opaque. Provision and delete operations use stable idempotency keys and may
complete asynchronously. Provider errors are normalized and sanitized.
Adapters must never return a secret in an error.

New databases select a configured backend for their logical region once. The
catalog persists the backend ID and a fingerprint of its namespace and
non-secret placement settings. Existing databases always resolve that stored
placement. Removing or repurposing the backend fails closed instead of sending
an operation to the current default. Secret environment-variable names are
excluded from the fingerprint so key rotation does not alter placement.

Lifecycle operations use durable leases. A provider resource ID is recorded as
soon as the upstream accepts provisioning, allowing reconciliation to inspect
the same resource after a process crash. Ready means the observed provider spec
matches the desired Gregale spec and the observed generation has caught up.
Deletion leaves a tombstone so names can be safely reused without losing audit
history.

Credential issuance is not part of database provisioning. The binding service
asks the adapter for an independently revocable credential and passes the
returned material directly to `CredentialSink`, implemented using Gregale's
encrypted app-secret store. Catalog tables may store only an opaque credential
reference. They must never store a password or connection URL.

Provider usage maps to unit-bearing canonical meters: active seconds, compute-
unit seconds, storage byte-seconds, history byte-seconds, egress bytes, and
operations. An adapter reports only the meters it supports. Product
entitlements and billing translate those readings into Gregale allowances;
they do not expose upstream billing vocabulary in the customer API.

## Provider changes and data migration

Changing the default backend affects only new databases. Existing databases do
not move automatically. A migration is an explicit workflow: provision the
destination, copy or replicate, verify extensions/version/data, freeze writes
or establish a final replication position, rotate each binding to newly sealed
credentials, cut over, observe, and retire the old resource after the recovery
window. The old backend configuration remains available throughout rollback.

This boundary limits vendor-specific code to an adapter and migration worker,
but it cannot remove the real operational cost of moving state. Features that
cannot be reproduced across qualified providers—such as proprietary branching
semantics—must not enter the portable database specification.

## Initial implementation boundary

This change adds the provider contract, immutable backend registry, lifecycle
service, in-memory transactional reference store, durable database/binding
schema, and focused tests. It does not expose a public API, install a provider
adapter, issue app credentials, or include databases in existing plans.

Before preview: implement and qualify at least one adapter; add a PostgreSQL
store adapter and reconciler; implement binding-to-encrypted-app-secret flow;
test restore and deletion; ingest provider usage; enforce account entitlements
and spend ceilings; add audit events, dashboard/API surfaces, and account
deletion cleanup; document regional, backup, extension, and support promises.

## Durable lifecycle follow-up

The first follow-up adds the production PostgreSQL store, persisted retry
schedule and attempt counter, and a provider-neutral recovery loop. Account-row
locking serializes quota reservations across replicas. Lease-token fencing
prevents a stale worker from committing after its lease expires, while the
provider resource ID survives a process crash so recovery inspects the accepted
resource instead of provisioning another one. Provisioning discovery remains
disabled by default; deletion intents are still recovered while disabled so a
rollout switch cannot leak paid upstream resources.

## First provider adapter follow-up

The first adapter maps one Gregale database to one Neon project. Provider
projects use a deterministic hashed name so a retry can discover an accepted
project before issuing another create request; the adapter never transport-
retries Neon's non-idempotent create-project POST. An ambiguous discovery fails
closed. The returned Neon project ID is persisted before asynchronous readiness
polling. Delete requests also carry Gregale's stable resource ID, allowing the
adapter to discover and remove an accepted project when the create response was
lost before its opaque ID could be persisted.

The adapter advertises PostgreSQL 14–18, pooled connections, scale-to-zero,
single-zone availability, and operator-configured storage and restore ceilings.
It deliberately does not translate Neon's internal redundancy into Gregale's
portable high-availability promise. Service classes map only inside the adapter
to 0.25–1 CU (`development`), 0.25–2 CU (`burstable`), and 1–4 CU
(`production`). Updates remain unsupported until the neutral lifecycle has a
durable multi-step update state machine.

Binding credentials map to deterministic Neon roles. The neutral provider
contract includes explicit revocation so deleting a binding can remove its
upstream login rather than only deleting Gregale's sealed secret. Usage maps
compute-unit seconds and public plus private network transfer. Neon storage
line items are reported as byte-hours under billing-oriented `*_bytes_month`
names; the adapter converts root/child storage to `storage_byte_seconds` and
instant-restore/snapshot storage to `history_byte_seconds` after summing each
complete provider window. The conversion is overflow-checked and remains an
internal canonical meter until commercial rates and caps are approved.

`apid` loads the registry and recovery worker when
`FAAS_MANAGED_POSTGRES_CONFIG` is present. The config-level
`provisioning_enabled` switch defaults false; deletion recovery runs regardless.
The lifecycle service enforces the same switch, preventing a future direct API
call from bypassing the reconciler gate. There is still no customer route or
plan entitlement in this follow-up.

## Durable binding-store follow-up

Bindings now use the same persisted lease, retry, attempt, and tombstone model
as databases. `access` remains a Gregale `read_write` or `read_only` promise;
provider role names do not enter the catalog. A binding reservation is valid
only for a ready database and a live app owned by the same account.

One active binding exclusively claims `(app_id, scope, environment_key)`. The
partial unique index intentionally omits `database_id`: permitting two
databases to target the same `DATABASE_URL` would make runtime behavior depend
on last-writer order. Binding reservations and customer app-secret mutations
also take the same transaction-scoped advisory lock. This closes the
cross-table race in which both transactions could inspect an empty opposite
table before committing.

Managed credentials remain encrypted `app_secrets` rows so the existing wake
path can inject them without a scheduler or guest protocol change. The row
records its binding ID, opaque credential reference, and generation; a trigger
checks that its account, app, scope, key, and generation match the active
binding. Customer secret mutations cannot replace or delete an owned row. The
host-key replayer has a distinct maintenance update that changes only the
sealed envelope metadata and preserves managed ownership.

The production store will not mark provisioning complete until the matching
owned secret exists, and will not tombstone a binding while that secret
remains. This makes the future provider-call → sealed-secret-write → catalog
transition a recoverable saga rather than pretending the external provider and
Gregale PostgreSQL share a transaction. This follow-up still makes no provider
credential call and exposes no customer route.

## Restore safety follow-up

Restore is a provider-neutral operation that creates a new logical database
from a ready source at a point in time inside the source restore window. The
target catalog row persists the source logical ID, source provider resource,
and timestamp before the adapter is called. The normal lease/retry loop then
invokes the same deterministic restore intent after a crash, records the new
opaque provider resource before readiness polling, and never mutates the
source or its bindings. Source deletion is fenced while an active restore
descendant exists.

Neon maps this operation to a named point-in-time branch with a read-write
endpoint. Restored Neon resource IDs are adapter-owned `project/branch` values;
the catalog and public API treat them as opaque. Credential issuance, usage,
inspection, and deletion all resolve the branch, while binding cutover remains
explicit. This is a restore-to-new-database primitive, not an automatic
failover or application cutover promise. Neon consumption metrics are
project-scoped, so the adapter leaves `RestoreUsageIsolated` false and the
service rejects these restores while usage guardrails are enabled; no
unallocated restore branch can silently bypass COGS ceilings.

## Binding reconciler follow-up

The binding service completes the provider-call → encrypted-secret → catalog
saga. Each binding generation derives one stable provider identity key. The
adapter must treat repeated issuance for that key as the same credential, and
the app-secret sink derives one opaque reference from the same binding and
generation. A crash after issuing the role or writing the secret therefore
replays the same effects instead of creating parallel identities.

Provisioning claims a persisted lease, verifies that the parent database is
still ready and resolves its stored backend fingerprint, issues credentials,
validates the returned material, and seals one PostgreSQL connection URL into
the binding-owned `app_secrets` row. Only then may the fenced store transition
the binding to `ready`. Plaintext credentials never enter the managed-Postgres
catalog or logs and their in-process lifetime is bounded to this operation.

Deletion uses the reverse safety order: revoke the deterministic provider
identity, remove the owned secret through its deterministic reference, then
write the deleted tombstone. Both external steps are idempotent. The sink can
derive the reference even when a crash happened after the secret write but
before `credential_ref` reached the binding row.

The background binding reconciler obeys `provisioning_enabled` for provisioning
and failed rows but always discovers deletion rows. Provider, secret-store, and
catalog failures persist a sanitized stage-specific code and a bounded retry
schedule. Read-only bindings require a provider-returned read-only endpoint;
provider CA material fails closed until a portable second-secret/file contract
exists rather than being silently discarded.

## Consequences

Gregale can add Neon, Xata, Prisma Postgres, a traditional managed PostgreSQL
operator, or a future in-house provisioner without changing the logical API or
catalog shape. Provider capabilities remain explicit, and unsupported product
promises fail before creating cost. The design deliberately postpones public
availability until credentials, billing, recovery, and operations are complete.
