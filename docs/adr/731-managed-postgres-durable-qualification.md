# ADR-731: Durable PostgreSQL lifecycle qualification

- **Status:** accepted
- **Date:** 2026-10-08

## Context

Version 7 combines live provider probes with an in-memory catalog and an opaque
credential sink. It cannot prove that the production PostgreSQL catalog and
encrypted app-secret delivery recover together after losing an acknowledgement.
Passing provider SQL tests also cannot establish that an application receives
the persisted credential or reconnects after rotation.

## Decision

Qualification version 8 requires a `postgres_restart` lifecycle report with
every durable assertion passing. Older artifacts and the memory smoke remain
diagnostics and cannot authorize new staging provisioning. The report mode and
checks are included in the existing approval digest and placement binding.

The independent qualification command gains an explicitly enabled durable mode:

1. Before provider mutation, verify a disposable, already migrated SQL catalog,
   its account/app ownership, canonical run UUID, and private age/HMAC files.
   The catalog database must carry `faas.qualification_run=<run UUID>` in
   `pg_db_role_setting` with `setrole=0`. Session GUCs cannot supply this proof.
2. Use the actual `PostgresStore`, `state.PgStore`, service and binding saga.
   Share credential URL construction, age sealing, HMAC and deletion with apid.
   Cutover preparation retains its unpublished-envelope path.
3. Once each upstream/secret write succeeds, lose its acknowledgement at
   provisioning, credential issuance, secret publication and rotation. Disconnect
   the catalog before normal saga error persistence, independently require the
   old pool to be unavailable, and reconstruct the registry, adapter and services
   before replaying the unfinished lease after expiry.
   Require unchanged logical and physical database identities, binding identity
   and rotation generation. Never adopt or compensate a preexisting name.
4. Independently read committed app-secret ownership, generation, recipient and
   HMAC. Authenticate over TLS and inspect runtime/migration privileges. Apply a
   static disposable table through migration credentials; read and update its
   marker through runtime credentials. After rotation, reconnect and require
   the original table, marker and counter without reseeding missing data.
5. Close provisioning during independent cleanup. Reconstruct again to verify
   binding/database tombstones and absence of their encrypted app secrets.
   Cleanup failures are evidence failures even after an earlier test fails.

Qualification callbacks return only sentinel errors; connection URLs, passwords,
keys, fixture IDs and raw driver errors never enter lifecycle reports. Customer
SQL is static or sqlc generated; the command never migrates a catalog or creates
accounts/apps. Operators own and remove the disposable catalog and key files.

## Validation and limits

Local PostgreSQL acceptance uses isolated migrated catalogs and customer
databases, real TLS/SCRAM authentication, separate restricted roles, encrypted
credentials and fresh service/pool instances. Simulated provider management
retains only independently committed effects. Negative cases cover missing fault
proof, preexisting fixtures, data loss, cleanup failure, credential substitution
and session-setting attempts to bypass the disposable-catalog guard.
Authenticated HTTP tests separately verify idempotent retry, stored replay,
binding recovery, rotation and cleanup after rebuilding apid with fresh pools.

Fresh live-provider version-8 evidence is required before approving a placement.
This contract does not prove guest secret injection, native guest networking,
application deployment, snapshot invalidation or scheduler retirement of old
credentials. A disposable deployed-app canary on a supported native KVM host
remains a separate rollout requirement. No production enablement, account plan
change, new migration or VM lifecycle change is part of this decision.
