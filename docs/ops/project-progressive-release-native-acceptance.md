# Native progressive project deploy acceptance

ADR-20261019120013 production admission remains disabled. The `project-progressive-release-only`
lane runs four independent scenarios through native builder microVMs, PostgreSQL,
APID, schedd, vmmd, gateways and real HTTP/worker guests. Adding or compiling this
suite does not qualify progressive deployment.

`TestProjectProgressivePromotionMetal` requires real job evidence and observed
5%, 25%, and 100% checkpoints. It stops APID during source builds, restarts APID
and schedd at the initial checkpoint, and checks durable recovery without new
source submissions. At 25% the candidate worker is armed to exit during its next
job, before acknowledging it. Queue delivery must retry that job against its
original candidate graph/deployment and complete it. The test checks ingress
responses for matching artifact/config generations and the existing app cap plus
rollout grant while traffic continues. Two real minute-spaced healthy rounds at
each stage must complete the release; test clocks and evidence rows are not edited.

`TestProjectProgressiveRollbackMetal` independently reaches 100%, then regresses
the candidate HTTP guest's critical probe. Actual route probes must restore the
retained graph and its runtime configuration. Both tests exercise baseline and
candidate ingress cohorts and clean up apps through APID. The guarded runner
reaps failed-run guests, runs leakcheck, and restores quiesced host services.

Run the `e2e-native.yml` workflow with lane `project-progressive-release-only`,
or use the guarded runner with an operator-staged exact source archive:

```bash
FAAS_E2E_PHASE=project-progressive-release-only \
FAAS_E2E_SOURCE_SHA="$qualified_revision" \
FAAS_E2E_GO=/path/to/pinned/go/bin/go \
bash scripts/ci/run-native-e2e.sh
```

Follow [native CI](e2e-native-ci.md) for designated-host markers, archive identity,
PostgreSQL credentials and signed base assets. Native x86_64 Linux, root and KVM
are required; virtualized or nested hosts are rejected. The test budget is 75
minutes, within an 80-minute systemd envelope. All four top-level tests must PASS;
a missing, skipped, or failed test blocks the lane. Both admission flags are
set only inside the private harness, never on production services.

Retain the exact source SHA, all `progressive native:` observation lines, PASS
lines and successful final leakcheck. An operator must review that evidence
before enabling `FAAS_PROJECT_CANARIES_ENABLED=1` through normal deployment.
The managed workspace lacks root and `/dev/kvm`; native qualification is pending.
Low-traffic timeout and operator replay scenarios are now required as described
below; their native qualification remains pending. Downstream managed service-call
behavior remains a separate check.

`TestProjectProgressiveStabilizationRollbackMetal` is also required by the lane.
It opts into stabilization and waits for full rollout success before regressing
the actual candidate guest's critical probe. Fresh native observations must then
restore the retained graph and paired configuration. Preserve its PASS and
`stabilization=true` evidence line along with the other scenarios. The fixture's
30-minute graph retention exceeds the 21-minute admission minimum.

## Independent HTTP qualification

Dispatch `e2e-native.yml` with `project-progressive-http-only` to qualify a
single HTTP workload without worker admission or queue traffic. The original three
native scenarios exercise promotion, rollback at full traffic, and stabilization
rollback. They observe all three traffic stages and matching artifact/configuration
generations, recover APID and schedd checkpoints, and assert no source resubmission.
The harness kills APID after acceptance during actual source builds; recovery reads
the original receipt. This is not a network-level lost-response injection test.

The workflow uploads `project-http-qualification.json` with the source SHA and
SHA256 of the collected log. Missing or skipped scenarios, missing observations,
failed final leakcheck, service restoration failure, or a mismatched runner SHA
block qualification. The combined verdict rechecks the collected log independently.
Reports are CI evidence, not signatures or admission tokens. Operator review and
normal production deployment still own admission; this HTTP report does not
qualify workers, schema cleanup, low-traffic timeout or operator control replay.
Native execution remains pending until the lane passes on the designated host.

### Bounded failure and operator control scenarios

The HTTP lane includes six single-workload scenarios. In addition to the three rollout
scenarios, it requires unavailable-probe timeout, early abort with response-loss
replay, and full-traffic abort followed by blocked recovery and successful operator
retry. Probe unavailability is a real connection failure against an allocated,
closed local port. The harness waits for the production ten-minute deadline;
it never rewrites checkpoint clocks or injects health evidence.

Control response loss is injected in the client transport after the real APID
returns success. The response is consumed and discarded, then APID and schedd
restart before the same action/request/generation is replayed. The audit decision
identity and timestamp must remain unchanged, and a changed generation must
conflict. Full-traffic recovery must reject new unassigned HTTP work while blocked,
then restore the retained graph and runtime configuration after fresh probes return.
Early abort must stay below 25% and preserve the baseline.

HTTP probes do not require customer request volume. The genuine low-traffic
scenario therefore belongs to `project-progressive-release-only`, which now
requires four mixed-release scenarios. It sends no queue jobs, requires the real
`insufficient_evidence` verdict, and waits for timeout without promotion or a
fabricated healthy baseline. Its terminal blocker requires operator action.

The HTTP test budget is 210 minutes inside a 215-minute runner envelope; the
mixed-release budget is 100 minutes inside a 105-minute envelope. These scenarios
are implemented and compile locally; native execution and qualification remain
pending on the designated KVM host.

### Managed downstream HTTP graph scenarios

The HTTP lane now requires nine scenarios, including independent frontend/backend
promotion, rollback, and early-abort runs. Both workloads are supported HTTP
members of the atomic project graph; this does not qualify stateful service-mode
workloads. `depends_on: [backend]` declares the managed binding, and the frontend
uses its platform-provided HTTPS endpoint and CA bundle. Calls originate inside
the frontend guest and traverse guest DNS, the managed service proxy, and the real
backend guest. No host proxy or fake backend supplies the native observations.

The frontend propagates the trusted inbound `X-Gregale-Release` to the backend.
The backend rejects a different wire generation with 409. Successful frontend
responses must report matching frontend/backend artifact and configuration
generations and the same nonempty release ID. Critical frontend probes also call
the backend, so promotion requires compatible downstream behavior. The harness
checks these responses while exercising both canary cohorts, restarting APID and
schedd, promoting, restoring the baseline after a real guest regression, and
aborting at the initial stage with control response-loss replay.

The report rejects any missing scenario PASS or downstream observation. The HTTP
budget is now 210 minutes within a 215-minute native envelope; worker lane budgets
are unchanged. Local fixture admission, verified-TLS protocol rejection, and report
checks do not qualify guest routing. All nine native scenarios must pass on the
designated host before this lane provides qualification evidence. Admission
remains unchanged and disabled pending native review.

## Concurrent production intent qualification

Dispatch `project-concurrent-release-only` independently of the HTTP lane. Its
three required native scenarios start an atomic verified release B while A is
progressive at 5% or 100%, and append a production configuration version while A
is at 5%. B uses a separate real source build and paired configuration. Its
publication must supersede A; native ingress must serve B's artifact with B's
configuration. The configuration scenario writes identical bytes through APID
and requires a higher version, exercising ABA ownership protection rather than
merely detecting a different hash.

After newer intent is committed, APID and schedd restart. A must report
`conflicted` with a reason, and stale abort/recover requests must return the
specific 409 ownership conflict. For a full controller observation interval,
the winning graph ID and configuration version/hash/values must remain unchanged.
The overlapping-release scenarios also check live B responses and deployment
counts; neither controller may resubmit source. No graph, checkpoint, health or
instance rows are edited by the qualification fixture.

The workflow uploads `project-concurrent-qualification.json`, bound to the exact
source SHA and collected log hash. Every scenario PASS, its matching stage and
winner-preservation observation, final leakcheck and successful runner completion
are required. The combined verdict rechecks evidence with the concurrent scope;
a passing HTTP report cannot substitute for ownership-race qualification. The
lane has a 180-minute test budget inside a 185-minute native envelope.

Compilation and CI-safe checks do not prove these races on real VMs. Native
execution is pending; this lane does not change production admission or authorize
an older attempt to undo a newer deployment.

## Rollback retention qualification

Dispatch `project-rollback-retention-only` for two required native scenarios.
The parked-baseline scenario uses the minimum supported idle timeout, stops APID
probe activity and observes the retained deployment parked with zero live
instances. The existing fresh-restart API invalidates cached snapshots. Actual
guest regression and controller restarts must then restore the baseline graph
and paired variables; a scheduler `wake.boot_started` observation must identify
that retained deployment and `method=cold_boot` after snapshot invalidation.
No instance or snapshot evidence is inserted by the test.

The expiry scenario uses the supported fifteen-minute retention policy, blocks
probe evidence, and lets the operator abort window reach its real deadline. It
waits for the database's actual baseline retention timestamp to pass without
rewriting clocks. A fresh recovery request must then return HTTP 410 with
`project_release_baseline_expired`, preserve the existing control request and
checkpoint generation, and leave the production graph/configuration unchanged.
New unassigned work remains blocked until a compatible replacement is deployed.
Historical replay of an already accepted decision remains supported.

The source-bound `project-retention-qualification.json` report requires both
scenario PASS lines, cold-boot/restoration or real-clock/expiry-rejection evidence,
final leakcheck and successful runner completion. The lane has a 120-minute test
budget inside a 125-minute native envelope. This qualifies retention eligibility
and runnable retained code, not physical artifact garbage collection. Native
execution remains pending; local checks and the admission change do not enable
progressive deployment in production. See [ADR-20261019120019](../adr/20261019120019-project-rollback-retention-admission.md).

## Atomic secret release qualification

Dispatch `project-release-secrets-only` independently. The fixture creates an
existing application-owned production secret through APID, stages its encrypted
replacement twice with the same request ID, and checks stable version references.
Both required native scenarios observe 5/25/100 traffic and APID/schedd restart
recovery. The actual guests' critical probes validate that the credential matches
the artifact generation; responses report only that boolean, never credential
contents. Promotion must keep old/new cohorts consistent, and a candidate regression
must restore the baseline graph, variables and earlier secret snapshot.

The private harness enables `FAAS_PROJECT_RELEASE_SECRETS_ENABLED=1` only for its
APID. The source-bound `project-secret-qualification.json` requires both scenario
PASS lines, encrypted/secret-configuration observations, final leakcheck and successful
runner completion. Test budget is 90 minutes within a 95-minute native envelope.
Native execution is pending; this lane does not qualify managed credentials,
secret reload hooks, host-key rotation or provider credential revocation.

### Secret encryption-key lifecycle

Both secret-release scenarios now start with a current identity and an old
recipient overlap. At 25% the real APID background worker rekeys every persisted
copy. Retirement must initially refuse the referenced key; after migration the
suite verifies current-only decryption and rejection by the old identity,
records retirement, removes the old identity file and restarts vmmd as well as
APID/schedd. Fresh-restart receipts and real `wake.boot_started` events must prove
cold boots for the baseline and candidate. The rollback scenario cold-boots the
restored baseline again and checks its original artifact/configuration/secret.

The report requires `rekey=true old_key_removed=true cold_boot=true`; a missing
or false observation blocks qualification. The lane has a 90-minute test budget
and a 95-minute systemd envelope. Native execution and final leakcheck remain
pending in this managed workspace.
