# ADR 20261117150000 — Durable Run artifact delivery

Status: implementation behind the existing disabled Run v2 writer gate.

## Decision

Promote a selected, successful, contract-valid Run output into an existing
application-owned object upload route. Delivery is an explicit control-plane
operation after execution. It never reruns code or extends a Run's deadline.
The destination bucket, prefix, content types, byte limit and encryption remain
owned by the existing upload route. No guest receives storage credentials.

Expose POST `/v1/apps/{slug}/run-artifact-deliveries/{route}` with the Run ID,
artifact name, expected bytes/SHA-256, content type and explicit stable delivery
idempotency key. Accept account deploy
credentials with current app/bucket authority, or a production app's live
`gregale:agents` workload identity. Workloads reach their own app and outputs
delegated to its endpoints. New deliveries require live Run receipt authority.
API-key callers retain the existing Run ownership boundary.

Use the existing tracked object upload journal, provider mutation receipt,
capacity reservation, encryption/protection capture, request metering and
recovery worker. Namespaced subject and idempotency identities distinguish Run
deliveries from other uploads. Each delivery key has one identity per app/route;
changed source or metadata conflicts. Concurrent requests perform at most one
provider write. An uncertain provider outcome remains pending and is resolved by
the existing exact receipt probe. Recovery never uploads again or guesses failure
from absence. A definitive failed delivery stays failed under that identity;
an explicit new delivery key requests another copy without rerunning code.

Validate the complete source length and checksum before any provider dispatch.
Stage it in a mode-0600 temporary file unlinked immediately after creation, with
bounded per-process concurrency, bytes and deadline. Stream from that descriptor;
never buffer an artifact in an API response or Agent transcript. Process death
reclaims staging automatically. No new persistent artifact copy is introduced
outside the existing customer object storage journal.

GET the delivery receipt under the same app/route, and POST its `/download-url`
subresource to obtain an ordinary short-lived, revocable object GET capability.
Completed receipts reference the app-owned bucket/key and public version when
available. Destination ownership and current bucket grants authorize reads and
URL issuance independently of the original Agent thread grant or Run-file TTL.
References are not bearer credentials. The application authenticates its own
customers before exposing a download capability. Unversioned objects retain
normal app-owned mutation semantics; delivery does not override bucket lifecycle,
retention, deletion or versioning policy.

Exact delivery replay and receipt reads survive disabled Run writers and expired
source files. New transfers remain gated by Run v2 and object storage availability.
Destination revocation still prevents access. Existing object workers must remain
until pending write receipts settle during rollback. No VM protocol, lifecycle,
new storage ledger, or background execution engine is added.

## Acceptance

Cover real encrypted source reads and tracked destination writes; duplicate and
concurrent completion processing; lost provider and settlement acknowledgements;
restart recovery; checksum/length corruption before dispatch; expired/revoked
source data; disabled writers; rejected contracts; app/key/bucket isolation;
private HTML delivery; and download capability issuance after thread revocation.
Exercise CLI/SDK contracts and the report Agent's persisted final turn before
revocation. Functional acceptance does not widen native runtime eligibility.
