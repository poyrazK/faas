# HTTP environment promotion health qualification

Run on the designated native x86_64 Linux KVM host as root, following the host
guards and exact source identity procedure in [native CI](e2e-native-ci.md).
Use an isolated project and harness; enable `FAAS_PROMOTION_HEALTH_ENABLED=1`
only in that harness. Do not enable production admission from workspace tests.

Use HTTP guests in two named environments with complete active release graphs,
at least 21 minutes of retention, pinned literal variables and distinct destination
secrets. Configure a read-only critical endpoint on every workload which performs
a real database read using the guest's destination configuration. Keep a separate
normal HTTP endpoint for observing the active release and runtime configuration.
Avoid generated service binding variables, workers, migrations and sidecars.

Collect evidence for all of these scenarios, using real minute-spaced probes
and the production deadlines. Do not edit checkpoint timestamps or evidence rows.

1. Promote with `--sync-config --verify --wait --progress`. Observe 202/running,
   unchanged baseline ingress during preflight, two healthy preflight rounds,
   atomic cutover, two new post-cutover rounds and final success. Prove unchanged
   rootfs/digests, source literal variables and destination secret identities.
2. Break only the candidate's destination dependency path before preflight.
   Require attributed probe 5xx, failed promotion and unchanged baseline ingress
   and configuration. A staging pass must not make this promotion succeed.
3. Qualify preflight and cut over, then break only the candidate's dependency
   path. Keep the retained baseline healthy. Require two real regression rounds,
   automatic rollback and previous graph/configuration/runtime variables restored.
4. Restart APID before preparation completes, after cutover and during rollback.
   Restart schedd/vmmd and prove cold wake still delivers the exact destination
   configuration. Deadlines, graph receipts and sample generations must persist;
   no artifact rebuild or second publication may occur.
5. Disable the probe sender or use a genuinely unreachable origin. Before cutover,
   require the real ten-minute timeout with no publication. After cutover, require
   unknown health and operator action when fresh baseline health is unavailable.
   401/403 and redirects must also remain unknown rather than pass.
6. Change destination variables/configuration or publish a newer graph during
   monitoring. Require a stopped/conflicted operation and preservation of the new
   intent, including on restart and retry. Run competing reconcilers and prove
   one candidate owner and no duplicated sample counts.

Retain the exact source SHA, phase/generation/deadline transitions, private-harness
logs with credentials redacted, active graph IDs, artifact digests and configured
probe results. Delete the test apps through APID, reap failed-run guests through
the guarded harness and require `make leakcheck` to pass. An operator must review
this evidence before enabling the flag through normal production deployment.

This workspace has no `/dev/kvm` and is not the designated native host. Native
qualification remains pending; the admission default stays disabled.

## HTTP frontend/API graph qualification (ADR-20261019120024)

Keep service graph admission disabled until the designated native KVM host passes
this case in addition to the checks above. Build frontend and API versions that
return their own immutable revision and the revision observed downstream from a
read-only frontend `/health/dependencies` endpoint. Include production-specific
secret evidence as a boolean comparison, never a secret value or body in receipts.

1. Deploy complete staging and production graphs with different artifact digests,
   literal variables, secrets and service-binding transport. Both use declared
   frontend-to-API bindings. Configure critical GET probes for every workload;
   the frontend probe must reject an unexpected downstream revision.
2. On the acceptance host only, enable both admission flags. Preview with
   `--sync-config --verify`, save the source/target document hashes and resolved
   destination hash, and obtain the normal protected-environment approval.
3. Promote without rebuilding. During preflight, verify the private frontend sees
   the candidate API, uses destination transport and secrets, and ordinary requests
   still reach the old complete graph. Park the candidate API and prove an exact
   cold wake; a warm baseline must not satisfy that call.
4. Restart APID, gateway, schedd and vmmd during preflight and again after cutover.
   Verify preserved deadlines and settings, fresh private challenges and unchanged
   artifact/rootfs digests. Confirm published document and runtime binding pins
   agree with the previewed destination hash after cold boot.
5. Make only the candidate API fail. Before cutover the old graph must stay active;
   after cutover require a healthy retained baseline and verify automatic restoration
   of both services, previous document, binding pins and destination secret versions.
6. Repeat with a destination edit between preview and publication, and with a later
   edit before rollback. Require conflict and preservation of the operator's values.
   Test unknown/forged release and deployment headers from ordinary traffic and
   another account: none may enter the candidate graph or bypass service grants.
7. Run the native test-metal and leakcheck gates; retain command output and bounded
   revision/status evidence. Keep admission disabled if any native gate is missing.

## Coordinated HTTP and push-worker qualification (ADR-20261019120027)

The executable [environment promotion acceptance lane](environment-promotion-native-acceptance.md)
covers success, candidate/retained worker recovery, cross-cutover retry pins and
automatic rollback. Its additional controls and load partitions cover the
operator and concurrency scenarios described below. Run all five jobs of
`project-environment-promotion-only` on the designated host and retain their
commit-bound aggregate report. Remaining failure variants and intent races still
apply before enablement.

Use the designated native x86_64 KVM host and an isolated destination queue.
Enable promotion health and worker admission only for this qualification.

1. Prepare complete pinned source and production graphs with HTTP services and
   one existing named-production push worker. Use different source/destination
   variables and credentials. Retain enough production job evidence for baseline
   health. Preview with `--sync-config --verify --workers`; verify that binding,
   consumer, artifact and runtime changes invalidate the approved identity.
2. Admit promotion. Confirm the candidate worker receives production variables
   and credentials but no production queue deliveries before graph cutover.
   Kill its VM and restart schedd/APID; it must recover from the durable hold
   without exposing candidate jobs. Unavailable startup proof prevents cutover.
3. Complete HTTP preflight. Verify graph, variables and new-job admission switch
   together. A job admitted before cutover and retried afterward must stay on
   its original worker/artifact/configuration. Stop its old worker and verify
   retained-pool recovery rather than retargeting the job.
4. Complete two disjoint healthy delivery windows and HTTP rounds. Repeat with
   no jobs, missing results, repeated retries, failed jobs and a failing baseline.
   Unknown evidence must not authorize success or unsafe restoration.
5. Drain the old pool, regress the candidate, and verify rollback first recovers
   a worker with the retained configuration proof. Block startup: the current
   graph must remain selected while restoration waits. Restore access and verify
   graph/configuration/job admission restoration with no job payload replay by
   the promotion controller.
6. Race binding edits, consumer retirement, explicit releases, worker drain and
   graph/configuration changes with cutover and rollback. Stale intent must not
   overwrite a newer release or admit a retired worker. Exercise expired holds
   and admission disablement after acceptance. Run metal/leak checks and retain
   redacted graph/task/runtime evidence before production enablement.

## Operator abort and guarded recovery (ADR-20261019120028)

The automated `promotion-controls` partition now covers pre-cutover abort,
post-cutover restoration, retained worker VM loss, and blocked-then-repaired
recovery with the full real deadline. Native execution remains pending; automated
success/rollback results alone do not establish control acceptance. The intent
races, publication-boundary interruption, baseline failure/low-traffic variants,
and retention exhaustion below remain additional qualification requirements. Use the current
status generation for each new command and preserve request/receipt identities.

1. Abort while preparation is in flight and immediately before cutover. Require
   unchanged production graph/configuration, an `aborted` receipt, retired unused
   candidates and eventual guest teardown. Late preparation/probe replies and an
   APID restart must not publish or revive them.
2. Abort after cutover while candidate HTTP health is failing. Require fresh
   baseline-only rounds with production spacing, atomic restoration and the same
   terminal receipt after APID restart. Restart APID after graph publication and
   before rollback checkpoint completion; require completion of that publication.
3. Repeat with a push worker. Kill the retained VM while checking restoration;
   schedd must recover its bounded hold and produce current startup/configuration
   proof. Exercise healthy real pinned delivery windows, low traffic and baseline
   job failures. Unknown evidence must leave the candidate selected. Jobs already
   admitted to the candidate must retain their version after restoration.
4. Leave the baseline unavailable for the full ten-minute decision deadline. Require
   `blocked`, release of its temporary startup hold, no publication and no retention
   extension. Repair the dependency, issue `recover` with a new request/generation,
   restart APID, and require fresh evidence and restoration without another build.
   Replaying the earlier abort must still return its earlier blocked receipt.
5. Race commands from two operators at the same generation. Require one accepted
   decision. Change destination graph/configuration/binding intent and expire
   baseline retention during restoration; publication must refuse each case. An
   older controller must be unable to drop the durable control payload.

Retain commit-bound logs, receipt IDs, graph/configuration IDs, artifact identities,
job version pins and native cleanup evidence alongside the existing lane report.

## Concurrent reconciliation (ADR-20261019120029)

Updated APID dispatchers share four live promotion health leases, with at most two
per account. One slot prefers due rollback/restoration and migration recovery;
other slots take the oldest due work. Bookkeeping survives APID restarts and
neither extends release retention nor shortens required health sample intervals.

On the designated host, qualify more than 20 accepted promotions across isolated
projects/accounts. Keep early promotions active while observing health checkpoints
for later ones. Delay one actual gateway probe and require unrelated promotions
to advance. Request abort/recovery under that load and observe the preference
without losing ordinary work. Record live lease/account counts, minute-spaced
sample timestamps, original deadlines, graph decisions and native cleanup evidence.
Restart APID while jobs are claimed; require expiry/reclaim of unfinished leases,
rejection of late observations and no duplicate cutover or rollback. Capacity
exhaustion may still consume real deadline time; scheduling does not fabricate
healthy evidence or authorize extra VMs beyond existing scheduler quotas.

The automated `promotion-load` partition exercises 21 promotions across three
isolated accounts/projects and seven destination environments per project. It
checks slow-probe isolation, owner loss with live leases, expiry/reclaim, sampled
fleet/account bounds, ordinary progress during operator restoration, and durable
graph/candidate identities. See [the automated lane](environment-promotion-native-acceptance.md)
for the five required native job reports. Portable checks and tagged compilation
do not satisfy this native gate.
