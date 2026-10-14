# ADR-1250: MCP observer OAuth connection lifecycle

## Context

Disconnect previously discarded the sealed grant locally without retiring it at
its provider. Operators could not distinguish local deletion from provider
acknowledgement, or inspect connection events without viewing runtime secrets.

## Decision

A role-owned lifecycle record survives observer pause, disable and configuration
changes. Disconnect atomically clears local OAuth state, fences in-flight work,
and creates one durable revocation job containing the frozen provider binding and
sealed retired grant. Confidential client credentials are copied as sealed data
when available, allowing retirement after the app secret is removed. No token
value, sealed envelope or lease token appears in public role or summary views.

Revocation endpoints are explicit reviewed HTTPS configuration, never inferred.
RFC 7009 POST requests use the configured client authentication, defaulting to the
token method except Google none. Guarded egress, disabled environment proxies,
redirect refusal and bounded deadlines match token renewal. Refresh and access
token acknowledgements persist separately under a 45-second lease. Lost replies
or process crashes permit idempotent retry. HTTP 200 means provider acknowledgement,
including already-invalid tokens; it does not independently prove provider state.

The existing apid domain loop processes bounded batches independently of observer
intent, workload state and plan. Retry delay grows to one hour, with eight attempts.
Exhaustion or missing revocation configuration becomes manual_required; terminal
jobs discard sealed grants. A pending job blocks a new authorization flow for the
same role to avoid retiring a newly issued connection. Repeated disconnect does
not restart a retired job. App ownership remains enforced on reads and writes.

The public lifecycle separates local connection state from provider retirement.
A bounded 64-event audit records connection, renewal, reauthorization, disconnect
and retirement outcomes as metadata only. CLI status displays this history and
flags reauthorization after invalid_grant or credential failures within fifteen
minutes of expiry. Renewal begins early enough to include the next observer
interval plus clock skew, reducing avoidable expiry between checks. These status
reminders do not create notification rules or send messages automatically.

## Consequences

Queue state persists in the existing monitor JSON transaction, with an append-only
constraint migration. No scheduler or VM owner changes are required. Down migration
refuses while lifecycle or revocation configuration remains present.

App deletion cascades its monitor and cancels remaining retirement work; operators
must wait for acknowledgement or revoke manually before deleting an app. Provider
revocation can invalidate broader grants, depending on the provider. Configuration
replacement discards the prior connection using existing semantics and does not
revoke it automatically. Disconnect before replacement when provider retirement
is required. Manual secret-based connections retain the existing secret rotation
workflow; guided disconnect controls govern browser connections.
