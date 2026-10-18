# ADR-1673: V2 Data API transaction generation

Status: Accepted

## Context

The note starter has versioned replay expiry, immutable original receipts and
read-only recovery (ADRs 1395–1398). The reusable order/inventory transaction
package still uses indefinite-retention business rows as receipts. Custom
operations need the same recoverable transaction pattern without copying the
note schema or silently changing existing generated projects.

## Decision

Add `gregale data-api transaction init --contract v2`. Keep the default and
explicit `v1` output byte-compatible with current template assets, and reject
unknown contracts before creating output. V2 uses separate embedded assets and
emits only a reviewable package, never executes a migration or overwrites an
existing directory. JSON reports include the selected contract and writer/
reader function names for v2; existing v1 reports stay unchanged.

For operation name `OP`, generate `api.OP_v2(uuid,text[],integer[])`,
`api.read_OP_receipt_v2` with the same arguments, separate `OP_v2_*` business
and receipt tables, and `OP_v2_result` composite. The order/inventory example
remains editable business code with its existing item/quantity/price bounds.
Tenant and actor come only from verified claims. Business reads are shared
within a tenant; private receipt access additionally requires the original actor
and a scoped reader/writer marker. Writer scope, forced business-table RLS,
PUBLIC execution revocation and approved invoker RPC grants follow the existing
transaction package. Receipt tables use scoped RLS without FORCE so the stable
owner can perform bounded maintenance.

Canonicalize paired SKUs/quantities by ASCII SKU order (SQL COLLATE "C") and
lock inventory in the same order. Store a complete immutable success snapshot
separately from business rows, including server prices and line items. Receipts
have no foreign key to orders, and their primary key is tenant/actor/request key.
A matching replay returns the original snapshot before reading current business
rows. Changed payloads return the SDK's exact `idempotency_payload_conflict`.
Stock, order, items and success receipt commit together; errors roll back all
changes and leave no negative reservation. Sequences and external effects remain
outside database rollback. Ordinary customer triggers/transactions may still
cause lock errors.

Reuse ADR-1670's fixed UUIDv7 timestamp policy and central limit parity tests:
24-hour window, five-minute future tolerance, immutable generated expiry,
writer expiry checks around operation/business locks and after work, and expired
writer rejection even after cleanup. Advisory keys include the generated
operation namespace, tenant, actor and UUID. Do not extend v2 windows in place.
The clock, history and owner-authority assumptions of ADR-1670 still apply.

The approved STABLE invoker reader runs in READ ONLY transactions, takes no
advisory/row locks and compares the full canonical request. It can confirm a
retained expired receipt. A missing live receipt returns zero rows; missing
expired receipts return the existing PT410 expiry contract. Absence, expiry or
conflicts never prove whether an earlier uncertain request committed. Restore
all temporary writer/receipt scopes on success and error.

Generate a typed client using schema-owned argument validators, a prepared
immutable operation with original API/schema/identity/contract context, explicit
write/lookup methods with retries disabled, and a complete snapshot type guard.
Validate business inputs before generating a UUIDv7. Preserve canonical paired
inputs and original key during recovery. Verify line quantities/prices and
recompute totals before narrowing generated JSON. Use existing SDK
`reconcileMutation` for completed responses; no polling, persistence, key
replacement, generic SQL endpoint or cross-request transaction is introduced.

Include a fixed-identifier owner cleanup script and pinned pg package manifest.
It requires the stable receipt owner, rejects FORCE RLS receipt tables/future
cutoffs, defaults to preview and commits bounded expiry-only batches using row
SKIP LOCKED and matching advisory try-locks. It never deletes business data.
Protect normal owner DML with an immutable/early-deletion trigger; acknowledge
that owners can change their own schema or truncate it. Users install and retain
the cleanup dependency lockfile when adopting the generated package.

Generated fixtures, integrity checks and time-independent access/refusal checks
remain reviewable. Live recovery/concurrency/expiry scenarios run in repository
acceptance against actual CLI output and PostgreSQL/PostgREST, avoiding fixed
UUIDv7 timestamps that would make saved access checks expire over time.

## Consequences

Customers can adapt bounded, recoverable business operations while preserving
the existing generator and applications. V2 is a separate opt-in package, not an
automatic data upgrade. Adoption requires owner migration, approved grants,
runtime refresh, generated validators/types and the paired SDK. The business
schema, membership model, receipt retention and external-effect handling still
require application review.

## Validation

CLI checks verify explicit/default legacy compatibility, generated files and
substitution, JSON metadata, unknown-contract refusal and preserved directories.
Native generated-package tests exercise schema generation, typed client behavior,
canonical replay, isolation by tenant and actor, overselling races, late rollback,
receipt failure, lost responses, immutable snapshots after business changes,
read-only scope restoration, hidden direct receipts, expiry around lock waits,
protected/locked cleanup, retained expired reads and late retries after purge.
