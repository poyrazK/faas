# ADR-1179: Read-only capacity-command evidence

Status: Accepted
Date: 2026-10-13
Related: ADR-1174, ADR-1177, ADR-1178

## Context

ADR-1178 retains successful capacity commands for thirty days. Resolving an uncertain response by repeating the PUT still requires write permission and can apply a command again after its receipt expires. Operators need to inspect retained acknowledgments with read authorization.

## Decision

Expose GET `/v1/apps/{slug}/event-subscriptions/{subscriptionID}/delivery-control/capacity/commands/{commandID}` with the existing read scope, MFA, timeout and no-store policy. Return the immutable original receipt, including requested value, optional reviewed expectations, previous setting, original plan and effective ceiling, outcome, recording time and fixed retention deadline.

Postgres reads target ownership and receipt in a read-only repeatable-read transaction without row locks. MemStore reads under its mutex and returns a decoded independent value. Both enforce app/account/subscription ownership and the same strict nonzero command UUID validation as mutation. Expired receipts are unavailable even before physical pruning. Lookup never deletes records, changes controls, reserves commands, consumes a receipt slot or extends retention.

The Go, Node and Python SDKs expose the lookup. `events subscription-capacity-command APP SUB COMMAND` uses only GETs, validates response scope and receipt consistency, supports JSON and a bounded read deadline, and requires no mutation confirmation.

## Consequences

The original outcome is evidence of a successful saved control change or successful no-op, not current configuration or delivery execution. Missing evidence is not proof that a command never ran. No actor audit or new ordering, reservation, exactly-once or deduplication guarantee is introduced. Existing receipt retention and quota remain unchanged; no migration is needed for this lookup.
