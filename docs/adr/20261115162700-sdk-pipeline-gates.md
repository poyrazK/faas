# ADR: Typed application decisions over native workflow event waits

Status: Accepted (SDK preview)

## Context

Durable typed job pipelines can validate an artifact, write data, and produce
a report. Applications also need human approval or typed external input before
performing later stages. Authentication belongs to the application, while the
native workflow engine owns durable suspension, event retention and replay.

## Decision

Add optional approval/input gates to the existing Node/Python pipeline context.
A generated contract validates the immutable review and typed reply. One
application-owned PostgreSQL row binds the scope, run, owner, root input,
contracts, review, role policy and timeout. A database-clock CAS decides once;
the same row records pending notification delivery. SDK services authenticate
current application membership and verify native root provenance. Native
events carry an invalidation with a stable key; the runtime always reads the
ledger for authority. Retries/replays cannot change the reviewed binding.

Use native event waits and existing workflow steps/budgets. Do not add a
second orchestrator, callback state machine, native lifecycle bypass or browser
platform credential. Keep public SDK entry points and current pipelines intact;
new APIs and browser view types remain preview-only. The CSV example opts into
a separate reviewed root contract, preserving the original workflow contract.

## Consequences

Applications install the gate schema, provide trusted auth/query adapters and
run bounded delivery recovery. Concurrent opposing choices conflict; identical
retries preserve their audit. Authoritative expiry/cancellation produce typed
outcomes, and pending/denied gates block later Job submissions. Immutable
artifact references and exact action inputs belong in the review. Accepted
Jobs retain their existing independent execution/cancellation semantics.
Application membership checks and platform cancellation cannot be one database
transaction; a parent that stops after decision persistence prevents future
orchestration while retaining the decision for audit. Native failed code
workflows retain their existing recovery limits. Local fixtures are not proof
of deployed event/timeout acceptance.
