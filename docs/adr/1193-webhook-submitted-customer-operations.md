# ADR-1193 · Webhook-submitted customer Operations

- **Status:** accepted for implementation; admission stays behind the ADR-521 preview gate
- **Date:** 2026-10-13
- **Owner:** apid and Operations maintainers
- **Related:** [ADR-212](212-durable-inbound-webhook-ingress.md),
  [ADR-521](521-customer-operations.md), [ADR-393](393-managed-exclusive-operations.md)

## Context

Operations accept work from an authenticated customer: the browser/API start
route or a declared gateway route. Much durable work is instead started by a
provider: a Stripe invoice, a Git push or a partner's signed event. ADR-212
already accepts such webhooks durably while the app sleeps, but it only enqueues
a plain invocation. The developer then rebuilds idempotency, progress, recovery
and completion around it — exactly what Operations exist to provide.

Two existing binding families route an endpoint elsewhere: webhook automations
start native workflows, and exclusive trigger bindings admit managed exclusive
operations. Neither creates a customer Operation with typed input, a customer
owner, progress, results and completion delivery.

## Decision

An inbound webhook endpoint can carry one **customer operation binding**:

```text
endpoint → (environment scope, operation name, platform tenant, event types)
```

The account sets the binding through an MFA-protected, versioned
`PUT /v1/apps/{slug}/inbound-webhooks/{id}/operation-binding`. The binding names
an operation, not a definition revision: each event resolves the definition of
that name on the scope's live deployment (or project release) at admission, and
admission pins it exactly as for any other submission.

**Identity.** The owning platform tenant is fixed by the authenticated binding.
It is never derived from the provider payload. Binding requires an active tenant
linked to the app through an active tenant surface, the same check as exclusive
trigger bindings. Per-event tenant mapping is out of scope.

**Ingress.** Verification is unchanged from ADR-212: endpoint token digest,
provider signature over the exact body, provider event ID and type. A verified
event whose type is outside a non-empty `event_types` list is acknowledged as
`ignored` and creates no work. Otherwise apid builds a deterministic input
envelope:

```json
{"webhook": {"endpoint": "<name>", "provider": "stripe",
             "event_id": "evt_…", "event_type": "invoice.paid"},
 "payload": { …verified provider JSON… }}
```

The envelope contains no receipt time, so a provider retry produces an identical
canonical input. The definition's input schema validates the whole envelope,
and its subject pointer may select from it (for example
`/payload/data/object/customer`). The same input reaches HTTP, workflow and Job
adapters; no adapter-specific header carries webhook metadata.

**Exactly one operation per provider event.** One transaction locks the binding,
reads the webhook receipt for `(endpoint, provider event ID)`, admits the
operation through the shared admission path and inserts the receipt. A retry
returns the original operation with `duplicate: true`, even if the binding has
since changed tenant or operation. A retry whose body digest differs conflicts
(409), as in ADR-212. Receipts expire with the plan's operation idempotency
retention, which exceeds provider retry windows; bounded cleanup runs inside
admission for the same endpoint, and receipts cascade with their endpoint.
Receipts reference operations by ID only, so operation retention never deletes
the record that prevents a second execution.

**Gates.** Every ADR-521 admission rule applies unchanged: the preview policy's
tenant and execution-kind allowlist, plan Operation allowance, pending quota,
input schema, code pins and tenant status. Closed admission, an unavailable
definition or a capacity error return a non-2xx response, so the provider keeps
the event and retries; Gregale never acknowledges an event it has not durably
admitted.

**One delivery mode per endpoint.** The existing routing guard trigger extends
to the new table: an operation binding conflicts with a webhook automation and
with an exclusive trigger binding on the same endpoint. Workflow callback
bindings stay compatible; they correlate individual provider objects with
waiting runs and are matched first.

## Consequences

- A sleeping app's Operation starts from a provider event with the same receipt,
  progress, recovery and completion contract as a customer submission.
- Rebinding cannot cause a second operation for an already accepted event.
- Deleting the binding returns new events to plain invocation delivery, but a
  retry of an already accepted event is still answered from its receipt, which
  is checked before the binding; deleting the endpoint removes the binding and
  its receipts, never operations.
- Disabling preview admission makes providers retry rather than silently drop.

## Rejected alternatives

- **Use the operation idempotency key alone.** Its scope includes tenant and
  operation name, so a rebinding between provider retries would start new work.
- **Forward webhook metadata as headers.** Workflow and Job adapters have no
  request headers; an input envelope is adapter-neutral and schema-validated.
- **Take the tenant from the payload.** ADR-521 derives identity from
  authenticated ingress only; a signed provider payload authenticates the
  provider, not a customer of the app.
- **Bind a definition revision.** Every deploy would silently strand the binding
  on old code; admission-time pinning already protects accepted work.
