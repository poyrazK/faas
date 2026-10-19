# Coordinated application promotion

Promote a tested frontend, API, one push worker and one backward-compatible
database expansion together:

```bash
gregale projects environments promote shop \
  --from staging --to production --sync-config --verify \
  --migrate --workers --yes --wait --progress
```

Preview/diff supports the same promotion options. The API
already carries both opt-ins in its reviewed promotion token; current SDKs can
use the existing migration-preview method with `workers=true`.

Declare exactly one migration command on an HTTP workload and keep it compatible
with the retained HTTP and worker versions. Ordinary application VMs receive
application credentials; only the existing migration task receives its migration
credentials. The candidate worker stays stopped until migration success, then
starts privately for configuration/startup verification. HTTP traffic and new
job admission switch in the existing graph publication transaction. Already
admitted jobs keep their original release, including on retry or restoration.

Failed migration leaves the baseline selected. A regression after cutover can
restore both services, worker admission and captured configuration when fresh
baseline evidence permits. The expanded schema remains in place. Unknown SQL
results never authorize automatic replay: inspect the database and use the
existing migration recovery `resume` action after scheduler-confirmed teardown.
Worker-containing replacement resolution is defined by ADR-20261019120038. To qualify it,
leave the migration uncertain, publish corrected frontend/API/worker artifacts with
`--sync-config --verify --workers` and no migration, and verify that repair succeeds.
Collect sufficient successful jobs for that selected replacement before requesting
`recover-migration --action resolve --replacement-promotion REPAIR_ID`. Confirm the
original executor is destroyed before acknowledgement. Send fresh jobs after the
request; check current startup/configuration proof, worker samples and window times
in the durable recovery record, unchanged uncertain SQL outcome, and no SQL replay.

Repeat with a stopped replacement worker, insufficient traffic, failed/retried
jobs, deliveries pinned to a different version, changed consumer settings, changed
destination secrets, and a newer selected graph. None may resolve. Restore missing
startup/traffic within the deadline and verify retry succeeds without a new SQL
task. Replay the same request after later intent changes and verify the immutable
result; altered attestations must conflict. Restart APID/schedd during verification
and check bounded cleanup with native leakcheck. Review this evidence under the
`worker-replacement-resolve` receipt; portable tests cannot supply that receipt.

## Native qualification and approval

The executable core qualification is the `project-environment-promotion-only`
workflow lane with `qualify_coordinated: true` (default false). It runs the existing
six partitions, the service-graph prerequisite, and the additional serialized
`promotion-coordinated` partition. Dispatch only through the designated native
workflow after source review. The coordinated partition runs fixture metadata,
success, real worker regression/rollback and uncertain SQL replacement resolution.
It uses real clocks and owned process faults, with no injected readiness or health.

Retain the `coordinated-qualification-SHA` artifact and its unedited
`native-e2e-SHA-promotion-coordinated/native-e2e.log`. The report scope must be
`project-environment-promotion-only/promotion-coordinated` and match the exact
source SHA, four-test inventory, log digest and successful final leakcheck.
Embed the report at `capability_reports.coordinated` and bind both
`coordinated-migration-workers` and `worker-replacement-resolve` receipts to this
log digest. Signing worker or coordinated capability requires the matching
`--coordinated-log`; a basic six-part report alone cannot authorize either.
`qualify_coordinated` also collects the separate service-graph report required
for bound graphs. CI never signs an approval or enables production.

The automated suite covers the core scenarios above; the additional negative
variants below still need their own reviewed native evidence.

Local unit/PostgreSQL checks do not authorize production. Use the designated
native x86_64 Linux KVM host and the isolation/source/cleanup guards from
[native promotion acceptance](environment-promotion-native-acceptance.md).
Retain the existing base report and applicable service-graph/worker/migration
evidence. Additionally run these coordinated scenarios with real builder VMs,
destination SQL and ordinary daemon-owned observations:

1. Build three different staging and production artifacts. Declare frontend-to-API
   bindings, destination HTTPS, distinct source/destination
   application credentials, and migration-only database credentials. Give every
   HTTP member a read-only database/dependency probe. Establish real baseline
   deliveries and enough retained release lifetime.
2. Submit one approved combined promotion. Before successful migration, ordinary
   traffic/jobs must use the baseline, the candidate worker must have no startup
   hold or queue deliveries, and candidate service bindings must stay closed.
   The destination alone receives the expansion and one SQL execution marker.
3. After task success, require destination configuration/startup proof on the
   worker, complete private frontend/API routing and two real minute-spaced
   healthy preflight rounds. Prove graph/configuration/new-job admission switch
   together. A pre-cutover job retried afterward must retain its original version.
4. Prove fresh post-cutover HTTP and worker delivery windows. Repeat with API and
   worker failures, low traffic, unavailable baseline and retained worker loss.
   Restoration requires fresh baseline proof and restores all members without
   replaying completed jobs or changing the expansion/one-execution SQL marker.
5. Return nonzero after partial expansion; separately lose a running command's
   result. Neither may publish or start the candidate worker. Restart owners and
   replay admission: the same task/outcome must remain. For uncertainty, require
   explicit database inspection, exact runtime teardown and resume. Preflight
   must restart with the worker policy intact, without another SQL execution.
6. Restart APID/schedd/vmmd/gateway before and after migration/cutover and during
   restoration. Preserve identities, original deadlines, artifact/rootfs hashes,
   destination binding/secret pins and job versions across cold wake. Race newer
   graph/configuration/consumer intent and require conflict without overwriting
   the new intent. Existing work must recover after admission flags are disabled.
7. Delete the test apps through APID and require the guarded final metal/leakcheck
   gates. Retain source SHA, task/lease/runtime IDs, phases and timestamps,
   graph/configuration IDs, SQL marker counts, job pins and redacted outcomes.

An operator must review actual combined evidence and issue an approval/policy
including `health`, `workers`, `migrations`, `coordinated`, and `service_graph`
when bindings are present. Add passed `coordinated-migration-workers` and
`worker-replacement-resolve` receipts
whose digest names that reviewed evidence, alongside all existing required
receipts. The existing worker and migration boot flags jointly determine whether
the coordinated capability is configured. Independent capability approvals are
insufficient; no native report or receipt is automatically fabricated here.

```bash
gregalectl promotion-rollout readiness \
  --policy policy.json --trust-key trust.pub --revision "$qualified_revision" \
  --account-id "$account_id" --project-id "$project_id" \
  --enabled-capabilities health,service_graph,workers,migrations \
  --require health,service_graph,workers,migrations,coordinated --json
```

Follow [controlled rollout](promotion-rollout.md) for signing, expiry and cohort
installation. Actual native execution remains pending; production flags stay off.

## Multiple-worker qualification

ADR-20261019120040 adds up to eight push workers with one owned consumer each. Repeat the
core cases with independent email, billing and webhook workers before approving
`worker_set`. Require startup/configuration and fresh delivery windows for every
worker. Remove the final worker's startup proof through an owned runtime fault,
leave only that queue idle, and regress only that worker's deliveries. These must
respectively block cutover, block success, and restore the entire graph after all
retained workers start. Exercise pre-cutover and candidate-pinned retries on every
queue, source/destination consumer changes, controller restarts, retention expiry,
operator abort/restoration, uncertain SQL resume and replacement resolution with
all worker proofs. Missing evidence from one worker must never be substituted by
another queue's deliveries. Run host cleanup/leakcheck after each scenario.

Review these results under `worker-set-cutover`, `worker-set-rollback` and
`worker-set-recovery`; the existing executable single-worker partition does not
supply these receipts. No such native evidence has been produced locally.
