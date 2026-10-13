# ADR 996: Bounded durable registry cleanup continuation

Status: implemented behind a separate off-by-default worker flag; native
acceptance pending. Extends ADRs 978 and 979. ADR 997 replaces routine root
discovery with an active index and retains lower-frequency root reconciliation.

Manual cleanup checkpoints are durable but require repeated operator calls.
Continue explicitly started operations with one cancelable runtime goroutine.
Discover unfinished work through bounded quota-root listing rather than an
in-memory queue that disappears at restart. Scan at most four roots per sweep,
respect the durable-app allowlist and reader grace, and attempt at most one
cleanup page per eligible scope. Never create an operation or rotate an epoch.

Use one private CAS record per attempted scope for a 30-second advisory claim,
last observed revision, sanitized errors, progress timestamps and persisted
backoff. Work starts only after a known claim acknowledgement. Worker observation
updates compare the claim object's version, fencing delayed writers after
takeover. A crashed/unknown claim expires; root checkpoint and epoch fencing
remain the safety authority even if a provider request outlives that lease.
Concurrent manual stepping is safe and can make a worker's checkpoint stale.

Bound discovery to five seconds, each scope to fifteen seconds, cleanup stepping
to ten seconds and a sweep to forty-five seconds. Wait five seconds between
sweeps/successes; scope failure backoff starts at ten seconds and caps at five
minutes. A discovery failure resets only its disposable cursor and uses separate
process-local backoff. Restart retains scope backoff, claims and checkpoints.
Unknown outcomes require a fresh root read on the next eligible attempt, not an
immediate replay. Provider cursor failures do not authorize automatic checkpoint
reset; the operator retains the explicit revision-checked restart operation.

Extend existing authenticated cleanup inspection with advisory worker status and
age since the last acknowledged successful worker step. Never expose provider
error text. Unavailable worker metadata must not hide the root checkpoint or
prevent manual recovery. Startup probes quota-root listing and only its own
unique worker-prefix object for conditional writes/deletion; it steps no scopes.

The cost is repeated discovery across inactive roots, one small private record
per attempted scope, and potentially long revisit times in large fleets. Short
leases coordinate attempts but do not promise exactly-once provider deletion or
bounded completion time under contention. Epoch-qualified immutable keys make
delayed/repeated deletion safe. No automatic cleanup starts or production
enablement are introduced. Native acceptance must cover prefix permissions,
process loss, clock skew, multi-node takeover, observation loss, manual races,
shutdown cancellation and realistic discovery/metadata storage cost.
