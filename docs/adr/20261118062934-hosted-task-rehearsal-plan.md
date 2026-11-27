# ADR-20261118062934 · Owner-approved hosted Task rehearsal plans

- **Status:** Accepted for preview planning
- **Related:** [Hosted Task binding qualification](20261117135916-hosted-task-binding-qualification.md), [Contained release commands](20261020150000-rehearsal-contained-release-commands.md), [Isolated PostgreSQL fixtures](20261017201009-rehearsal-postgres-fixtures.md)

Hosted Task source review approves retained bytes. Binding qualification is a short-lived observation of the reviewed native setup. A rehearsal needs its own approval, scoped to an isolated run and independent of release authority.

## Decision

With rehearsal planning, source retention and binding verification enabled, an unscoped personal administrator API key can prepare one immutable plan per qualified source identity. The plan pins the intent, review and binding digests, the observation time and original evidence deadline, and a server-assigned disposable target identity with its own queue namespace. The target identity is reserved metadata. A future executor must allocate fresh contained resources under that identity, including a disposable database. The plan accepts no caller-selected database, namespace, app or secret reference. It has zero traffic authority.

An owner approves or revokes the exact plan digest in a separate request. Approval pins the native administrator key privately so its authority can be rechecked later. Approval and revoke replay the original receipt. Metadata inspection and revocation remain accessible while admission is disabled. The plan never outlives the passing observation, which expires within one minute and cannot extend the retained source lifetime.

A private store operation consumes the approval once. The caller must present the approving personal key and exact plan digest. PostgreSQL checks retained source, binding selection and completed evidence, app and credential pins, and native key authority under the same transaction as the one-time claim. It checks the real clock after lock waits and in the write predicate. Expiry, drift and revocation permanently invalidate unconsumed plans. The native claim transaction also inserts one private run reservation keyed by the retained intent. It pins the plan digest, target identity, claim time and original approval deadline. A reservation failure rolls back the claim. Replays return the same reservation without acquiring execution ownership. Historical claims made before reservations can return their plan status without creating a run. The API and CLI expose planning, inspection and owner decisions. They return closed metadata without source, credentials or binding references.

The plan and run journals follow the binding identity lifetime and are kept outside environment clones. Keeping them separate preserves reservations during rolling upgrades with older plan writers. Plan creation, approval and private claim do not provision capacity, open retained source, run probes or migrations, deploy workloads, or alter traffic. Connecting the claim to a contained runner requires a separate change with execution and cleanup evidence.

## Validation

Native memory and PostgreSQL tests exercise owner authorization, immutable replays, credential or authority drift, and concurrent claims. The OpenAPI and CLI contracts expose the preview owner operations.

## Candidate handoff

The private launch entry checks that the authenticated key holds the exact
unconsumed approval. It opens the account-encrypted source under the original
namespace and verifies both reviewed trees before packing deterministic build
archives in memory. The native claim rechecks the original authority, evidence
and deadline. Only the call that acquires the reservation receives the archives.
Retries receive the same run identity. An unavailable or changed source cannot
spend the approval.

The archive handoff is private to the control plane. Contained workload
provisioning and execution evidence are separate from this handoff.

## Contained resource preparation

The private provisioner takes the winning claim and derives workload IDs,
scenario membership and the queue namespace from the reserved target. It
creates four internal-only preview workloads for migration, web, worker and
observation. The workloads enter the existing contained-network and webhook
capture policy before the PostgreSQL fixture is created. Only the migration
workload receives a credential during initial preparation. The native fixture
starts with a platform-owned capture marker. The release runner must install
the Task schema in containment before it can request separate restricted
web, worker and observer logins.

Preparation must complete within the original approval window. The existing
preview and PostgreSQL cleanup services expire these resources fifteen minutes
from reservation. The PostgreSQL fixture journal records the backend, database,
migration owner and membership needed to remove the database and logins.
Failure cleanup also removes the app-local credentials and tombstones the
workloads. A restarted executor can clean the same account and run without
recovering archives or credentials. Claim replay never allocates resources
or takes ownership of an existing run.

## Candidate builds and guest migration

The private runner checks for the reviewed Node starter's migration entry
before spending the approval. It submits the worker archive to the normal
isolated builder under the migration workload's identity and pins
`node tasks-migrate.js apply` as its contained release command. That workload
can run the release command and is ineligible for priming an ordinary app VM.
The starter receives the reserved queue namespace, the disposable schema
owner URL and newly generated Task keys. The exact archive and all three
build identities are tied to the original run, and every deployment sets
traffic explicitly to zero.

The runner verifies the successful, retired guest command receipt against
the exact run, deployment, command and build artifact. It then retires the
migration deployment and removes its credentials. Once PostgreSQL grants
the restricted web, worker and observer logins, it submits the reviewed web
and worker archives with independent runtime credentials. The two runtime
builds must succeed before the runner returns their deployment identities.
Build and migration completion leave runtime protocol and Task smoke
evidence to the next stage.

The reserved workload lease bounds the operation. Abort cleanup and the
existing preview teardown recover the owned spool and source objects and
app-local ephemeral credentials. The existing fixture teardown drops the
disposable database and roles. An approval replay never resumes an
interrupted build or migration.

Roll out the imaged and scheduler prime fences before connecting a caller to
the private runner. These owners must agree that the migration workload may
execute its release task without starting an ordinary app VM.

## Native worker recovery

The enrolled acceptance caller pins the independently prepared web and
worker fixtures before claiming the owner-approved run. The worker fixture
pauses attempt one at its logged checkpoint before entering the report
handler. The observer matches that log to a live PostgreSQL lease without
exporting its token, then rechecks the lease before requesting interruption.

The local scheduler socket accepts only a fresh observation for the exact
reserved run, contained worker, zero-traffic deployment, node, instance
and wake. The scheduler checks that vmmd observes the resident worker and
moves the instance out of RUNNING before requesting hard teardown. A lost
or failed teardown suspends the proof and leaves the run to cleanup.
A confirmed teardown retires the instance and uses the ordinary worker
reconciler to restore capacity.

Recovery requires another live lease for the same Task on a distinct
worker VM, the fixed report through the pinned private MCP connection,
and one completed result in the reserved PostgreSQL namespace. The
observer reads counts and the attempt number through its restricted login.
The report is private to the harness. Native acceptance additionally
requires running the journey on the enrolled KVM host.

### Operator acceptance command

A dedicated single-box acceptance host enrolls its compute node UUID as
`node_id` in the existing root-owned
`/etc/faas/rehearsal-acceptance-host.json` alongside the dedicated account and
API origin. The enrolled node must resolve to the local schedd and vmmd
Unix sockets. The platform stamp and clean checkout must match.

Prepare the trusted fixture from that checkout with
`scripts/ci/rehearsal-native.py --journey hosted-task --prepare-only --out DIR`.
Use those sources in the normal hosted source review and binding checks,
then approve a fresh rehearsal plan through the existing owner workflow.
On the enrolled host, use the same personal administrator in `FAAS_TOKEN`
and the dedicated HTTPS API origin in `FAAS_API`. Supply the deployed APID
credential and fixture environment. Paths to the account-domain identity,
credential lookup policy, runtime recipient and value-fingerprint key
must be readable by the `faas-apid` service identity. The child uses the
existing rehearsal admission switches and dedicated PostgreSQL fixture
configuration. Use a new evidence directory outside the checkout.

```sh
GREGALE_REHEARSAL_NATIVE_ACCEPTANCE=1 scripts/ci/rehearsal-native.py \
  --journey hosted-task --out "$evidence_dir" \
  --hosted-task-intent "$intent_id" --hosted-task-plan "$plan_sha256"
```

The root wrapper takes the shared native acceptance lock and runs host
leak checks. It invokes a tagged one-shot child under the APID service
identity with the four prepared SHA-256 pins. The child authenticates the
same owner, opens the configured stores, and calls the existing private
runner. It reads the PostgreSQL catalog, workload and secret rows, and
source storage after cleanup. The wrapper validates the receipt while it
is pending, repeats the host identity check and publishes success only
after the final leak check. A partial run leaves private evidence and
logs. Its consumed approval cannot be replayed.
