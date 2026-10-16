# ADR 1215: Native PostgreSQL COMMIT deadline qualification

Date: 2026-10-12
Status: Accepted

## Context

Native lost-acknowledgement qualification proves ambiguous write recovery, but
instant response loss does not prove application behavior when protocol work
outlives a caller deadline. Holding the outbound COMMIT and holding its successful
reply must yield different database outcomes before replay.

## Decision

Add two native cases using the existing write caller, ledger and loopback verified
TLS proxy. Fix a five-second COMMIT stall in sealed provider configuration and a
1.5-second caller deadline. Keep bearer controls namespace-bound, with bounded
plans and disposable database opt-in. Preserve the ordinary drop case.

The caller reports an unknown outcome once its HTTP deadline expires. Existing
query/connection and transaction-idle deadlines subsequently retire PostgreSQL
work. Join that work before observing or replaying; do not claim immediate SQL
cancellation from the caller's HTTP abort.

Require positive canceled hold evidence, direction and timing, one initial provider
attempt, stable provider/ledger identities, and an independent database observation.
An outbound request hold must show zero charges and no forwarded COMMIT before
replay; a successful reply hold must show one confirmed matching charge. Explicit
same-key replay ends with one confirmed charge and changed-payload reuse returns
409 in both cases. Cleanup independently proves exact namespace deletion and
backend retirement with no residual pool, connection or timer resources.

Raise the native gate to seventeen cases and verdict version 8. Portable real
PostgreSQL tests execute the exact fixture/driver and native proof verifier. They
do not replace qualification on the designated native x86_64 KVM fleet.

## Consequences

The gate distinguishes pre-commit cancellation from lost knowledge of a completed
commit. It retains a conservative unknown application outcome until independent
observation/replay establishes the result, and requires existing dedicated migrated
runtime-only PostgreSQL prerequisites and native fleet access.
