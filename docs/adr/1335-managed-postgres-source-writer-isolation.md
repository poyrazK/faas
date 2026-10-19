# ADR-1335: source writer inventory and provider isolation evidence

Status: accepted for internal recovery plumbing; customer activation disabled.

## Context

ADR-1334 closes admission for a frozen database selection. SQL observations can
detect writers and catalog drift but cannot establish that provider-admin APIs,
alternate endpoints or future background work cannot write. The current Neon
adapter has no qualified mechanism for enforcing those broader guarantees.

## Decision

Read a bounded, complete native database catalog and aggregate client,
background, replication and unknown backend counts with the existing closure
observation. Hidden backend types are unknown. The observer's own SQL session is
the only session excluded. Recognized server housekeeping is distinguished from
logical writers; neither its classification nor zero counts attest isolation.

Persist the first complete closed catalog fingerprint in its own immutable
column. Subsequent observations compare against that original baseline and must
never adopt a changed catalog. Persist a one-way invalidation timestamp after
catalog drift, including omissions before the first complete baseline. Restoring
the original settings cannot erase that history or revive the same operation.
Both columns survive writes from older workers;
their missing diagnostics/evidence fail closed. The existing private closure
JSON retains writer diagnostics and provider evidence atomically with SQL
findings. New reads do not install provider guards or choose a new selection.

Add an optional, read-only provider evidence contract. A supporting provider must
install a durable operation-owned catalog/background guard as part of its
existing close dispatch. The guard covers all dataset endpoints, replication,
background dispatch, provider administration and database creation/replacement.
Its evidence binds the exact backend, provider resource, dataset, generation,
maintenance owner, close timestamp and original catalog fingerprint. Evidence
expires within the existing five-minute verification window; expiry never
releases the underlying guard. Existing authenticated abandonment must release
the guard and permanently tombstone delayed close/guard requests before its
terminal acknowledgement. A worker lease and repeated observations cannot
implement this contract.

Verify the provider response, preserve specific failure findings and invalidate
positive findings after expiry, drift, authority loss, unsupported capability or
failed refresh. Preflight reads the journal only, reports safe aggregate findings
and recomputes staleness; fingerprints, owner tokens and provider details remain
private. Provider evidence also bounds report expiry. Cancellation continues
through the existing source-fence recovery path with provisioning disabled.

Neon remains unsupported for the stronger provider contract. Its real SQL
inventory is diagnostic evidence, not a fabricated catalog lock. No capability
or production gate is promoted, and every activation blocker remains in place.

## Verification and boundaries

Exercise catalog creation/settings drift, reconnect rejection, hidden backend
visibility, prepared transactions, malformed/mismatched/expired evidence,
observation failures, restart, cancellation and delayed requests. A test provider
can exercise accepted evidence without qualifying any deployed provider.
Native VM behavior is unchanged. Live provider and native qualification, complete
runtime coverage, routes, snapshot invalidation and publication/resume remain
separate requirements. Downgrade refuses active catalog baselines and drift history.
