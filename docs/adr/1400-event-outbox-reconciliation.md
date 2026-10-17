# ADR-1400 · Reconciliation and guarded control for application event outboxes

Date: 2026-10-15
Status: Accepted

## Context

Application-owned publication intents stop after terminal rejection or their
fixed retry deadline. Lost acceptance responses require retained content evidence;
unavailable evidence does not establish failure. Operators also need to inspect
unresolved rows and correct credentials or a renamed dispatch slug safely.

## Decision

Add Go SDK helpers for read-only remote content verification followed by local
confirmation, explicit guarded resume of blocked rows within their original retry
window, and bounded live pagination of blocked/unconfirmed metadata.

Only owned matching acceptance within the original intent window can confirm a
row. Verification does not publish. Compare the original local snapshot during
settlement and reject live claims or concurrent changes. Unavailable, conflicting,
invalid, and failed observations never settle or automatically resume work.

Resume compares updated time and attempts, requires blocked state without a claim,
and preserves the request, UUID namespace, key, and deadline. It may change the
routing slug while retaining the expected-app-ID guard in the saved request.
No new platform endpoint or schema column is required. Extend the opt-in local DDL
with a partial unresolved-key index; local SQL follows the prior outbox ADR's
static SQLC validation exception to platform generated Store conventions.

## Consequences

Operators can reconcile lost acceptance without risking another publication and
explicitly resume repaired work within its existing policy. Clock skew, expired
retention, replacement acceptance, or races can keep work unresolved. Listing is
a live view with owner/filter-bound cursors, not a durable checkpoint. Applications
retain responsibility for business idempotency, permissions, payload retention,
worker lifetime, and intervention beyond the retry window.

See [the outbox guide](../event-outbox.md).
