# ADR-427 · Exclusive operation policy retirement

- **Status:** accepted
- **Date:** 2026-10-01
- **Amends:** ADR-393
- **Issue:** #4068
- **Context:** An unused account policy cannot currently be retired by its owner.
- **Decision:** Add permanent idle retirement while retaining ownership history.
- **Why:** Customers need to stop policy admission and recover active quota slots.
- **Consequences:** Retired names and historical generations remain durable; active
  operations and configured producers prevent retirement.
- **Rejected alternatives:** Physical deletion or name reuse could erase history
  or restart fencing generations; disabling only producers leaves policy admission
  and quota consumption active.

Account owners can retire a named policy through the CLI and authenticated
deploy-write API. Retirement is a permanent, idempotent customer-intent
transition. It preserves the policy ID, accepted operation receipts, business
keys, submission identities and monotonically increasing generations. A retired
name cannot be recreated or receive new work. There is no physical deletion.

Pending/running operations and configured trigger bindings block retirement with
`operation_policy_in_use` (409). Customers first finish or cancel their work and
unbind producers. Existing dispatch and lease transitions retain their contracts.

Retirement serializes with submissions through the account lock and with binding
publication through the policy row lock. Check active operations and bindings
after acquiring that lock, then publish the retired revision in the same
transaction. Memory and PostgreSQL adapters implement the same transition.

The existing 64-policy account limit counts active policies. Retired policy
history remains inspectable and does not permanently consume an active slot.
Repeated retirement returns the original retired revision. This changes customer
intent only; it does not change VM lifecycle or scheduler instance state.

Acceptance covers account isolation, active-work/binding rejection, permanent
identity and generation retention, idempotent retirement, admission rejection,
quota reuse, and PostgreSQL races against admission and binding publication.
