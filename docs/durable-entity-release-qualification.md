# Durable entity release qualification

Scope: the Node developer workflow, typed client generation, operational
visibility, reconnectable service sessions and session recovery/lifecycle. This is a release review and an acceptance handoff, not evidence that
native storage or runtime behavior has passed. Production preview gates remain
off until the operator's native qualification is recorded.

Use the authenticated [readiness report](durable-entity-readiness.md) to inspect
the serving node's app configuration and outstanding external acceptance checks.
It does not infer release acceptance from startup success or consume admission
budgets.

## CI evidence

The `durable-entity-developer-workflow` workflow runs on its development branch
without requiring a PR. It verifies the real packed SDK installed outside the
checkout, checked starter handlers and generated clients, negative type assertions,
local replay and commit faults, manual alarms, checkpoint restart, isolated restore
validation, HTTP route separation and generated-client replay identity.

Go checks cover declared artifacts, namespace/path validation, packaged SDK
integrity, current validator source, source-upload guards, metadata classification,
read-only scope routing, changed-only watch output and generated CLI documentation.
These checks establish development and presentation behavior, not native durability.
Record the workflow URL, tested commit SHA, job results and any skipped coverage
in the release evidence. A later source change requires fresh CI evidence.

## Recorded developer-workflow qualification

[CI run 38122189681](https://github.com/poyrazK/faas/actions/runs/38122189681)
passed both jobs for source commit
`d864945b1f51e2999a9192501ffda123337ef177`.

| Check | Result |
|---|---|
| Generated protocol contract drift | Passed |
| Node SDK/test compilation and SDK package build | Passed on Node 22.10.0 |
| SDK workflow and visibility behavior | 8 tests passed, zero failures |
| Packed SDK installation outside checkout | Passed |
| Starter type assertions, client generation/drift and source-only generation | Passed |
| Starter HTTP, replay, fault, checkpoint, alarm and validator cases | 6 nested tests passed through the packed-starter suite |
| Storage, ownership/replay, declared artifacts and visibility | All tests passed with race detection in the three durableentity packages |
| Focused CLI source guards, inspection/watch and Markdown reference | Passed with race detection |

The initial CI attempt exposed a widened protocol literal in an existing handler
test; preserving the `1 | 2` tuple fixed compilation, and the complete replacement
run passed. No local tests, builds or native acceptance were run. This evidence
covers the focused workflow, not every repository CI gate. Native KVM/GCS cases
below remain unverified. Later documentation-only commits do not change the
qualified source; later code or workflow changes need a fresh successful run.

## Recorded reconnectable-session qualification

[CI run 38124353085](https://github.com/poyrazK/faas/actions/runs/38124353085)
passed both jobs for source commit
`2422e3b765195342b234510ea9d69c8bbea3f6b2`.

The Node job passed compilation, package build, 11 SDK tests and the packed
starter suite with 7 nested cases, including room checkpoint/replay and isolated
restore validation. Go race checks passed for all four durableentity packages,
the focused CLI checks and five socket API tests covering reconnect replay,
changed app admission, disabled preview, revoked credentials, immutable-scope
fencing and the preview-flag prerequisite.

API integration uses fake execution and in-memory storage. It does not establish
native GCS publication, restart or multi-owner behavior. Socket admission remains
separately disabled by default. Local tests/builds were not run.

## Recorded session recovery/lifecycle qualification

[CI run 38127768064](https://github.com/poyrazK/faas/actions/runs/38127768064)
passed both jobs for source commit
`0fbfaf3116f18a0e8d8db7df3d0ae2cbb9fa557a`.

SDK compilation/build and 15 focused SDK tests passed. The packed-starter suite
passed with 8 nested cases, including generation-qualified typed clients and
isolated recoverable-room restore validation. Go race checks passed for all four
durableentity packages, the focused CLI checks and authenticated socket API tests.
Lifecycle coverage includes fresh versus replayed status snapshots, lost
acknowledgements, idle/retention boundaries, failed publication, generation fencing
after receipt removal, legacy/application migrations and invalid async migration
rejection before publication.

An earlier run exceeded the 20-minute Go job budget during the API step. The
replacement workflow allows 30 minutes, retains race detection/resource bounds,
and uses a workflow-specific Go cache; the successful run saved the complete cache.
No local tests/builds or native acceptance were run. Status is a committed snapshot,
expiry uses explicit authenticated service ticks, and reclamation frees a closed
cohort's slots without deleting platform receipts. Native release gates remain off.

## Acceptance environment

The acceptance agent should use the dedicated Gregale internal node and private
GCS bucket selected by the owner, with credentials held in ADC/environment/secret
storage. Record the host revision, application/deployment/environment/tenant IDs,
provider configuration, narrowly enabled test-app preview gates and test period.
Do not put credentials, customer state or checkpoint files into release evidence.
Use isolated test identities; do not inject failures into production workloads.

## Native acceptance cases

| Case | Procedure | Required evidence |
|---|---|---|
| Acknowledged commit | Invoke a counter with an explicit ID; after success, terminate its execution and restart the owning service | State retains the committed value; identical invocation returns the original result/version with replay=true |
| Lost acknowledgement | Interrupt the response after storage publication, then retry identical ID and payload after restart | One committed transition; returned receipt agrees with the published state |
| Failed publication | Fail a storage operation before publication, then restore access and retry | Unpublished state is not visible or replay authority; eventual successful transition commits once |
| Owner takeover | Pause an owner, let another owner take over, then resume the obsolete owner with its original fencing token | Obsolete publication is rejected; the replacement's acknowledged state remains authoritative |
| Same-key concurrency | Invoke independently through two routing/ownership paths using distinct IDs against one key | Committed transitions serialize without lost updates; distinct keys remain independent |
| Replay conflict | Reuse a committed ID with changed payload or scope | Changed payload conflicts; no additional state transition; scope isolation holds |
| Alarm restart | Commit a short deadline, terminate the worker before execution, restart the service | Alarm is discovered and consumed; obsolete/version-changed attempts cannot overwrite state |
| Alarm failure | Force a handler rejection through bounded attempts and backoff | State remains unchanged; inspection reports attempts/backoff/exhaustion; explicit fresh fenced recovery works |
| Outgoing intent restart | Commit a reservation and interrupt the relay around acceptance/acknowledgement | Pending intent survives restart; delivery identity remains stable; acknowledgement loss cannot create a new logical intent |
| Delivery semantics | Cause transient failure and eventual success at an isolated webhook sink | Record attempt and deduplication evidence; distinguish transport acceptance from entity acknowledgement; do not claim exactly-once HTTP side effects |
| Inspection isolation | Inspect/watch while invocations and relay work continue | Inspection does not acquire ownership, invoke handlers or mutate metadata; tenant/environment selectors remain isolated |
| Validator/release binding | Build/deploy the starter, validate a candidate, change/roll back the immutable release binding | Correct artifact digest/deployment binding is used; missing/stale/incompatible artifacts fail closed |
| Credential isolation | Examine the permitted guest protocol and packaged source | No host bucket credential or storage object locator crosses into customer execution |
| Socket reconnect and restart | Save a pending session frame; interrupt its acknowledgement after publication, restart services and reconnect through another owner | Identical frame replays the original receipt; sequence advances once; durable state survives |
| Socket admission changes | Revoke the service credential, disable app sockets or suspend the tenant between frames | Next frame cannot dispatch; no acknowledged state change |
| Socket scope replacement | Replace the selected environment or app while a connection remains open | Existing connection cannot dispatch into a new immutable scope; deliberate reconnect is required |
| Socket bounds | Fill the configured process/account/entity slots, send oversized or malformed frames, and leave connections idle | Caps hold, rejected frames do not dispatch, and closed connections release slots |
| Session progress recovery | Lose a committed message acknowledgement, restart, replay its exact frame and issue a fresh status query | Original result replays without another transition; fresh status matches its committed sequence; an old query remains an old snapshot |
| Session expiry and reclamation | Drive authenticated logical ticks around idle/retention boundaries, reclaim a closed cohort, then retry old-generation frames after receipt retirement | No early reclamation; business state survives; obsolete generations cannot reopen or mutate state |
| Session lifecycle owner takeover | Race a message, expiry and reclaim through two ownership paths, then resume the obsolete owner | One authoritative generation and serialized state; obsolete owner cannot publish pre-reclamation session state |
| Session application migration | Deploy a higher schema with application-only migration; interrupt publication and retry after restart | Business state migrates once with generation, clock and sequences preserved; invalid migration cannot publish |

A simulator test cannot replace any native case. Owner takeover needs an actual
second ownership path and provider CAS evidence, not just killing the local dev
server. Record fault-injection boundaries and results precisely; do not infer a
storage commit merely from a handler response or a successful webhook response.

## Release decision

CI success qualifies the developer workflow for review. Native acceptance is a
separate gate for enabling the object-storage-backed preview. The inspection API
still omits ownership/lease status, replay receipt history and application payloads.
Generated clients target checked Node JavaScript. The new WebSocket preview serves
owner-authenticated service clients; browser authorization, broadcast, presence and
hibernation remain outside this release. Alarms and delivery use bounded retry
contracts; HTTP delivery is not an exactly-once side-effect guarantee.

Record native acceptance evidence before enabling the production preview. Review
the concrete source, immutable deployment bindings and qualification report together
when deciding whether to release.
