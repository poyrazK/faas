# Terminal operator cancellation of hosting CI runs

Date: 2026-10-17

Status: accepted for internal implementation; live qualification deferred.

An abandoned CI intent can occupy preview capacity for its entire retry window.
Introduce a separate default-off operator cancellation action for an exact owned
run. CI tokens gain no cancellation authority. Main hosting, operator admission,
automation and durable-site prerequisites remain authoritative. CLI and dashboard
require independent default-off opt-ins. Cancellation performs no provider calls.

Under the site lock, cancellation atomically marks the retained run
canceled, revokes its original preview at terminal generation two if present, and
retires its pending/ready release with the existing route publication generation.
Previously promoted releases (including inactive rollback targets), active releases
and other preview references reject the entire cancellation without side effects.
Repeated cancellation returns the original identity and cannot extend cleanup time.
Run identity, fingerprint, expiry and original reservation choice never change.
Cancellation only updates existing rows and never allocates capacity or obtains an
account lock after its site lock. Allocation keeps its account-before-site order.

Every upgraded writer blocks canceled preview creation even if the cancellation
flag is off. Release retirement fences manual previews, finalization, promotion and
new upload grants. Identical CI admission retries return the canceled identity;
CLI stops before uploads. Existing signed upload grants and in-flight provider work
can finish; cancellation cannot revoke them or a GitHub job. Existing publication,
preview tombstone CAS, acknowledgment, grace and pinned-release garbage collection
still gate physical deletion and storage accounting. An unused preview reservation
is freed immediately; preview rows remain counted until safe purge.

Upgrade all API writers and apply the migration before enabling. Old binaries do
not understand the terminal marker; do not roll back while canceled identities
exist. Downgrade refuses to erase any canceled marker. Legacy rows backfill false.
Stored diagnostics and activity expose canceled intent and cleanup separately from
serving health; activity eligibility is advisory and revalidated atomically.
