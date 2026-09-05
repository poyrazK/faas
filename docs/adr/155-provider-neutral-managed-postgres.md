# ADR-155 · Provider-neutral managed PostgreSQL foundation

- **Status:** accepted as a control-plane foundation; no public entitlement until a provider adapter, billing guardrails, backups, and support runbook are qualified.
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
provision/inspect/update/delete, credential issuance, and usage. Resource IDs
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

Credential issuance is not part of database provisioning. A future binding
service will ask the adapter for an independently revocable credential and pass
the returned material directly to `CredentialSink`, implemented using
Gregale's encrypted app-secret store. Catalog tables may store only an opaque
credential reference. They must never store a password or connection URL.

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

## Consequences

Gregale can add Neon, Xata, Prisma Postgres, a traditional managed PostgreSQL
operator, or a future in-house provisioner without changing the logical API or
catalog shape. Provider capabilities remain explicit, and unsupported product
promises fail before creating cost. The design deliberately postpones public
availability until credentials, billing, recovery, and operations are complete.
