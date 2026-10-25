# Approvals with structured input

- **Status:** accepted for implementation
- **Date:** 2026-10-19
- **Owner:** Operations maintainers
- **Related:** [ADR-1194](1194-operation-waits-and-approvals.md),
  [ADR-20261017100853](20261017100853-customer-self-service-operation-signals.md),
  [ADR-20261018210500](20261018210500-per-customer-operation-concurrency.md)

## Context

An approval (ADR-1194) records `approved` or `rejected` and an optional free-text
comment. Real approvals carry data the next step needs: the refund amount a
manager agreed to, the shipping option a customer chose, the reason code for a
rejection. Today the approver's application stores that data itself and the
workflow step reads it back from the application's own database, outside the
Operation's record and idempotency.

## Decision

An approval may carry **input**: a JSON object the approver submits with the
decision, validated against an **approval form** and delivered to the workflow
with the decision.

**Forms.** The account declares a form for an approval step of an Operation
name in an app environment: `step`, `input_schema` (JSON Schema, the same
dialect and bounds as Operation input schemas) and `required` (an approval must
carry input). Forms are managed at runtime with
`PUT`/`GET`/`DELETE /v1/apps/{slug}/operation-approval-forms` (MFA; deploy write
scope to change). A form applies to every deployment's definition of that name,
so changing it needs no deploy; the account owns keeping it compatible with
the step that reads the input.

**Reading a pending approval's form.** The account and the customer read the
form for one Operation's approval at
`GET /v1/apps/{slug}/operations/{id}/approvals/{step}/form` and
`GET /v1/platform-tenant-self/customer-operations/{id}/approvals/{step}/form`,
so a UI can render it. Once decided, the response also carries the recorded
decision with its input.

**Deciding.** The existing decision routes accept an `input` object. Inside the
decision transaction the store checks it against the current form: input
without a form is refused, an approval without input is refused when the form
requires it, and any input must match the schema. A rejection may carry input
(a reason code) and it is validated the same way.

**Delivery and idempotency.** The input is part of the platform-written
callback payload, `{"approval": {..., "input": {...}}}`, which the step after
the approval receives. Repeating a decision with the same input returns the
original; any other input conflicts, as a different decision does today.
Operation events and the timeline keep their current fields, so the input is
visible only to the workflow and to readers of the form route.

**Gated.** `FAAS_OPERATION_APPROVAL_INPUTS_ACCOUNTS` (apid) names the accounts
that may use forms; empty, the default, admits none. For any other account the
form routes are indistinguishable from absent ones, an `input` field on a
decision is refused as an unknown field, and existing forms are ignored. The
public approval request and payload types are unchanged; the gated types extend
them, and none of it enters the OpenAPI document or SDKs until launch.

**Limits** (`pkg/api/limits.go`): forms per app by plan (Hobby 10, Pro 100,
Scale 1,000); input at most 16 KiB.

## Consequences

- Approval data travels with the decision, is validated once, and is replayed
  exactly on retry.
- A form change applies to approvals already waiting; the step reading the
  input must accept every form version it may meet.

## Rejected alternatives

- **A field on the approval declaration in the definition.** It would change
  the public definition contract before launch, and a form fix would need a
  deploy.
- **Free-form input without a form.** Nothing would validate it before the
  workflow resumes, and a UI could not render it.
