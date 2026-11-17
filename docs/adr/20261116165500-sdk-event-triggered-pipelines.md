# ADR: Typed event-triggered pipelines with durable start receipts

Status: Accepted (SDK preview)

## Context

Typed application pipelines compose Jobs, approvals and child workflows, but an
event consumer must still coordinate its external workflow start. Transactional
event-consumer callbacks intentionally cannot make external effects. A native
start can succeed while its response or the application's commit reply is lost;
retrying it after native key retention can start another run.

## Decision

Add matching Node/Python event payload contracts, type generators and opt-in
event-to-pipeline adapters. Authenticate each delivery with an application
verifier, validate its original CloudEvents bytes and canonical retained
acceptance/subscription identity, and check current application authority on
every start, duplicate, inspection and reconciliation. Pin configured source,
subscription, account and app. A payload owner claim is never authorization.

Before any native start, insert one immutable intent into an application-owned
PostgreSQL table. Only an acknowledged new insertion authorizes dispatch. The
identity includes endpoint/account/app, adapter name, subscription and the exact
decimal int64 acceptance ID. Bind exact original body bytes, generated contracts,
authorized owner and mapped input by digests; retain no payload or credential.
Conditional settlement can attach one matching checkpoint and never replace it.
Each query is an acknowledged autocommit statement on the same primary, outside
business/event-consumer transactions. Install DDL explicitly; no automatic worker,
lease takeover, pruning or second workflow engine is introduced.

New typed starts optionally carry a validated `trigger` identity in the workflow
envelope and checkpoint input digest. This prevents recovery adopting an unrelated
run with identical business input. Existing callers keep their envelope shape.
Event-enabled backends and readers use matching updated SDKs.

Any retained intent without a checkpoint is `start_unknown`. Replays never POST
again, including after native key retention. Authorized reconciliation only reads
an externally identified native run and adopts it after verifying scope, owner,
contract, exact input and trigger identity. Lost reserve acknowledgements may
leave an intent that never dispatched; lack of a run ID is not proof of failure.
Do not manufacture a replacement key or event to bypass uncertainty.

## Consequences

Applications own authentication, current business authorization, immutable input
records, receipt retention and reviewed recovery. Broker delivery and Job side
effects retain their existing semantics; this is not a global exactly-once claim.
Local cancellation/deadlines fence later SDK operations but cannot undo accepted
remote work. Mapping must be deterministic and free of side effects. Native
failed code workflows retain their existing continuation limits.

Shared fixtures exercise byte/digest compatibility, large decimal acceptance IDs, concurrent
starts, authority changes, uncertain responses and verified recovery. The CSV
example runs competing Node/Python consumers against PostgreSQL, recovers a lost
start response, then uses existing mixed drivers, real local workers, Chrome
approval and verified reporting. Native event/control-plane/realtime transports
remain protocol fixtures; local passes do not establish deployed acceptance.
