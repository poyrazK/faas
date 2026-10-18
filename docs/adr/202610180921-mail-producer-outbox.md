# ADR-202610180921: Durable producer handoff for internal account-deletion email

- Status: accepted for an internal experiment; activation remains disabled.
- Date: 2026-10-16
- Builds on: [internal mail service](202610180920-internal-mail-service.md), ADR-021 account lifecycle, ADR-982 apid ownership.

## Context

maild commits its transactional recipient outbox after accepting a request, but
apid previously committed account deletion and then called the mailer separately.
A process failure between those steps could lose the deletion-pending notice.
Restarting a producer after an ambiguous admission must not create a second maild
message or change the original message payload.

## Decision

Add `FAAS_INTERNAL_MAIL_PRODUCER_OUTBOX_ENABLED=0`, exact opt-in only. The first
writer is apid and covers self-service deletion-pending notices. apid requires
the existing master, producer, scheduling gates and scoped internal-producer
transport; it commits account lifecycle and one immutable versioned intent in
the same transaction. New domain code lives in `pkg/apid/mailoutbox`; server code
keeps wiring and the existing request/response shape. No customer mail API, new
listener, entitlement or automatic transport activation is introduced.

The producer intent stores immutable routing/recipient/timing/template inputs,
not rendered content or credentials. Its key is `account-delete:<intent UUID>`.
The exact versioned renderer and absolute expiry remain stable across retries.
Bounded workers use fenced leases and at-least-once HTTP admission. After an
ambiguous response/commit, maild's producer-scoped idempotency resolves a repeat.
Admission is distinct from relay acceptance and destination delivery.

Producer scheduling takes namespace admission (871740), account, then shared
recovery (871739) before saving intent. Claims take shared recovery before the
outbox row and release it at commit. Dispatch locks account before intent across
the bounded HTTP handoff, retaining no recovery advisory lock across services.
A generation header is validated inside maild's admission transaction under its
own shared recovery fence. This prevents a recovery that starts/releases between
producer preflight and HTTP admission from accepting a stale intent, while
avoiding a lock cycle between a waiting deletion, recovery and that HTTP request.
Account restoration cannot pass between lifecycle review and maild admission.
Stale leases cannot send. Claim/expiry maintenance is bounded. Restore, changed
address or rearmed deletion cancels stale work before handoff. Existing platform
and maild suppressions remain enforced. Deadlines and retry budgets are centralized.

The stored maild recovery generation prevents old producer requests from becoming
new admissions after a database rewind/global release. New atomic deletion
admission refuses a recovery hold. Old mismatched intent remains blocked until
expiry; this phase exposes no force replay or generation reset. Operators must
preserve the producer configuration and follow the external restore-ID procedure.

maild has metadata-only, bounded namespace reads on its existing private operator
socket, with the existing separate operations credential and new flag. It owns no
producer-intent writes. New metrics contain no identifying labels. Terminal
handoff states clear the recipient snapshot; account hard deletion cascades all
intent metadata. Rollback refuses any nonempty producer outbox. Default-off uses
the existing inline path and does not inspect outbox credentials or state.

## Validation

PostgreSQL tests exercise atomic save/rollback, concurrency, durable replay,
stale account/custody checks and erasure. A real maild Unix handler commits a
message before losing its response; a restarted producer repeats its saved key,
leaving one message and one fake SMTP handoff. apid tests verify that default-off
keeps its existing behavior, opt-in skips inline sends, and a failed intent save
does not start account deletion. All validation is offline; live qualification
and deployment are separate.
