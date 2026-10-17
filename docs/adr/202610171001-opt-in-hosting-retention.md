# ADR-202610171001: Opt-in retention of abandoned uploads and expired CI releases

Date: 2026-10-17
Status: Accepted for the internal, default-off pilot

## Context

CI can create private immutable previews safely, but repeated or interrupted jobs
consume the existing four pending upload slots/account and ten releases/site.
Physical cleanup already has provider receipts, grace periods and retries. It
only handles releases explicitly retired; no component chooses abandoned intent.

## Decision

Add an independent default-off API/runtime retention flag and a default-disabled
per-site policy. Keep normal operator, account, MFA and scope enforcement. CI
credentials gain no cleanup or policy endpoints. Require durable active sites,
matching configured origin, active accounts and current operator authorization
for the background worker. Save policy with its own revision CAS, outside generic
idempotency replay. No new serving, provider or billing behavior is introduced.

Provide a read-only cleanup plan, including proposed age rules, per-release
eligibility/protection reasons, eligible reservation bytes and reservations
already retiring. A plan is an observation, not a deletion receipt or execution
authorization. Re-read policy, releases, preview references and CI intent under
the site lock immediately before retirement. Concurrent policy changes,
promotions, uploads, finalization and preview creation share that lock. PostgreSQL
retires the eligible batch and updates the existing publication outbox in one
transaction; memory does so under one mutex.

Record a monotonic last-activity timestamp before issuing upload grants or
starting verification. New writers record this metadata even while retention is
off, making a later hot opt-in safe for their in-flight work. Apply the migration
with a fresh activity window for every existing release, never a retroactive
expiry. All writers must be upgraded and old in-flight work drained before opt-in.
Age rules range from one hour to 30 days, default 24 hours for both classes. The
minimum exceeds bounded CI execution, grant lifetime and verification work.

Retire idle pending uploads, including manual uploads, only after their pending
age threshold. Retire ready releases only if a retained CI intent identifies
them, their staged idle age has passed, and the original CI preview/retry window
has ended. Never automatically retire active or previously promoted releases,
manual ready staging, unknown activity, or any remaining preview reference.
Expired/revoked preview rows remain holds until their existing tombstone
publication and removal grace finish. Retain the CI run ledger after catalog GC;
late retries cannot allocate replacements. Its 1,000-run/site cap is unchanged.

Run bounded keyset passes over at most 100 opted-in active durable sites with a
30-second pass budget. Keep the in-process cursor across passes, advance past
failed/foreign/unauthorized sites and restart from the beginning after a full
scan. Duplicate workers are safe because retirement rechecks current state under
the site lock. Retention only accepts retirement intent; existing independent
publication and artifact cleanup controllers complete the work. Disabling the
new flag or site policy stops further automatic retirement at admission, while
already accepted retirement and normal cleanup continue.

## Consequences

The dashboard needs a separate default-off build flag and API capability. It
shows the plan before saving a policy, keeps the reviewed policy revision and
rejects edited/stale reviews. Eligibility and reserved bytes are catalog figures,
not measured R2 storage or immediate savings. Policies authorize future eligible
releases too; each pass rechecks dependencies. Published rollback targets and
retained asset fallback remain under manual control in this version.

Migrate and upgrade every writer before enabling. Downgrade refuses while a
policy is enabled; disable policies and drain workers/writers before rollback.
No Cloudflare credentials, Enterprise features, edge changes or live qualification
are needed for local verification. See the
[retention guide](../internal-frontend-hosting-retention.md).
