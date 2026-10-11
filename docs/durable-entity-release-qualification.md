# Durable entity release qualification

Scope: the Node developer workflow, typed client generation and operational
visibility. This is a release review and an acceptance handoff, not evidence that
native storage or runtime behavior has passed. Production preview gates remain
off until the operator's native qualification is recorded.

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

A simulator test cannot replace any native case. Owner takeover needs an actual
second ownership path and provider CAS evidence, not just killing the local dev
server. Record fault-injection boundaries and results precisely; do not infer a
storage commit merely from a handler response or a successful webhook response.

## Release decision

CI success qualifies the developer workflow for review. Native acceptance is a
separate gate for enabling the object-storage-backed preview. The inspection API
still omits ownership/lease status, replay receipt history and application payloads.
Generated clients target checked Node JavaScript; browser clients, WebSockets and
hibernation remain outside this release. Alarms and delivery use bounded retry
contracts; HTTP delivery is not an exactly-once side-effect guarantee.

This work does not authorize opening a PR, merging or enabling production gates
under the existing no-PR instruction. After acceptance evidence is available, the
owner can review the concrete source and qualification report for release.
