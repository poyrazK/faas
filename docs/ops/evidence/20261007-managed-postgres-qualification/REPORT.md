# Neon provider qualification — 2026-10-07

## Result

The complete version-7 provider contract and database/binding lifecycle smoke
passed against disposable Neon PostgreSQL 18 projects. Native snapshot capture
and copy remain unqualified: the live snapshot responses omit the capture
point and expiry needed by the adapter. Production provisioning was not enabled.

The tested code is merged commit
`5d43e0707684beeefbb54ac519745939420172ff` (ADR-677). The provider run lasted
from `2026-10-07T20:02:53.672669Z` to `20:17:44.723263Z`; lifecycle smoke
completed afterward. Tests used the development class, single-zone availability,
1 GiB storage ceiling, a one-day restore window, and scale-to-zero in `us-east-2`.
Projects were disposable resources within the Gregale organization. The original
project and Launch plan were unchanged.

| Acceptance area | Result |
| --- | --- |
| Configuration preflight | 14/14 checks passed |
| Version-7 live provider contract | 42/42 checks passed |
| Database and binding lifecycle | 11/11 checks passed |
| Artifact verification | Genuine artifact accepted; all 6 invalid variants rejected |
| Snapshot custody and compensation | 16/16 checks passed |
| Native snapshot capture, retention, and copy | Blocked by missing independently observed snapshot metadata |

The [sanitized acceptance summary](acceptance-summary.json) records checks,
boolean evidence, usage readings, and the SHA-256 of the private original
artifact. It deliberately is not a rollout approval document. The original
approval envelope remains in the private operator test directory and was not
applied to any service.

## Proven behavior

- Provisioning, inspection, same-identity replay, asynchronous deletion, and
  recovery deletion completed successfully.
- The completed `19:00–20:00 UTC` usage window included 10 compute-unit seconds.
  Storage, history, and egress returned zero quantities for that short window.
  The same completed window remained readable after project deletion.
- Compute suspended and woke through an actual PostgreSQL connection; the
  measured first wake latency was 2,368 ms. This observation is not an SLO.
- Runtime and migration privileges were separated, runtime DDL and privileged
  administration were denied, and rotation preserved data. Read-only credentials
  passed existing/future-object access, mutation and RLS restrictions, password
  recovery, rotation, and session/login revocation probes.
- Class resize and both directions of the idle-policy update preserved dataset
  identity, data, credentials, and request replay. The original class and policy
  were restored. The isolated always-on probe does not grant that entitlement
  to customer plans.
- PITR verified the exact source and requested point, target readiness, replay
  of the same target, earlier committed data, rejection of source credentials
  on the target, and completed target cleanup.
- The service smoke created a database and binding, waited for readiness, then
  deleted the binding and database. It used an in-memory catalog and a sink that
  retained only opaque secret references; it did not exercise a deployed app.

The saved artifact verifier accepted the genuine report and rejected older
version 6, changed report digest, absent lifecycle evidence, changed backend
fingerprint, changed canary accounts, and an expired approval. These checks
made no provider calls and did not apply gate values.

## Snapshot gap and compensation

A capture requested at `2026-10-07T20:01:18Z` was accepted, but its creation and
repeated list responses provided only identity, owner name, source branch,
creation time, and the manual flag. They omitted both `timestamp` and
`expires_at`; the bounded observation period did not establish a verified
capture or retention state. Gregale returned `unavailable` rather than deriving
the capture point from the request or treating absent expiry as indefinite
retention. The API accepts a requested timestamp and expiry, but accepting the
request alone is not independent proof of the resulting snapshot.
See Neon's [create](https://api-docs.neon.tech/reference/createsnapshot),
[list](https://api-docs.neon.tech/reference/listsnapshots), and
[restore](https://api-docs.neon.tech/reference/restoresnapshot) contracts.

A separate project exercised the durable creation acknowledgement before
verification failed. A fresh provider instance remained pinned to that accepted
identity, refused to adopt the unverified capture, and blocked native restore
before mutation. Compensation used the creation receipt, independently observed
snapshot absence, accepted cleanup replay, and deleted the source project.
All 16 checks passed. This establishes safe compensation, not snapshot
restorability or complete clone support.

The next snapshot implementation needs independently verifiable capture
position and retention evidence while preserving custody, replay, and cleanup
rules. Changing timestamp equality or assuming that omitted fields are valid
would not close this gap.

## Cleanup and limits

Complete final project inventory contained only the original Gregale project.
All disposable projects, branches, and snapshots were removed. The temporary
org-wide key was revoked; subsequent authentication returned 401 and its local
secret file was removed. No billing-plan change or production rollout occurred.

This qualifies the current core contract for the tested PostgreSQL 18 placement
and spec. It does not establish every advertised PostgreSQL major or region,
nonzero storage/history/egress conversion, provider correction timing, invoice
reconciliation, a production workload canary, or production failover behavior.
Native snapshot copy, end-to-end environment clones, and cutover activation and
rollback still need separate acceptance. The qualification artifact does not
cover those optional protocols.

Local race regression checks for qualification and snapshots passed in
`pkg/managedpostgres`, `pkg/managedpostgres/neon`, and
`cmd/managed-postgres-qualify`. The runbook SQL gate and whitespace validation
passed. These changes add evidence and correct documentation; they do not alter
runtime behavior or weaken qualification gates.
