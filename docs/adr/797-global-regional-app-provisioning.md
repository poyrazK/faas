# ADR-797: Global regional app provisioning

## Context

ADR-730 names all selected regions as the global app routing policy. ADR-734
can reconcile an image release across verified targets, but requires those
regional apps to be created and attached separately. A response cache expiring
after 24 hours cannot establish which app a failed creation attempt committed.

## Decision

APID owns a bounded multi-target provisioning endpoint and the regional creation
endpoint. A global personal app can provision one regional app per selected
cell. The caller supplies distinct regional account-admin keys for this request
and explicit regional account UUIDs. An authenticated cell/account proof precedes
planning; identities are never inferred from email or slug.

Planning atomically retains each original cell, registry generation, HTTPS origin,
regional account, UUID and complete app creation request. Regional credentials
are neither persisted nor returned. Later requests can add regions but cannot
replace retained intent. All targets are planned before any creation is dispatched.
Dispatch has bounded concurrency, timeout and response sizes; redirects are
refused. A failed region leaves independently successful targets available to
the existing image release reconciler.

Each regional creation transaction holds the account quota lock and commits the
app, original provisioning receipt and organization activity outbox together.
Retries compare immutable intent and return the original app identity. Receipts
survive app deletion and cannot be updated, deleted or truncated. App collection
returns an expired result and never recreates an app or adopts a slug collision.
Current region admission and ordinary resource, app-count and service-capacity
validation remain authoritative for new creations. Original receipt replay does
not depend on later plan defaults or region admission closure.

The directory accepts only exact receipts followed by a fresh ownership proof.
Attachment and provisioning state update share the global app lock. Detaching
a target cancels its retained provisioning intent, preventing old request retries
from reattaching it. Registry changes block dispatch without redirecting retained
intent to a different origin. Cancellation does not delete regional resources.

Retained provisioning identity marks an app as globally managed immediately.
Before a quota stream exists, scheduler/native admission and resident SQL writes
already refuse an independent regional allowance. New deployment intent remains
available; executing resident instances or tasks requires future quota activation.
Unknown provisioning storage state fails closed. Independent apps must prove
absence of both quota and provisioning identity to retain regional admission.

Shared organization provisioning remains unavailable until the regional
organization billing/admission contract is implemented. This creates customer
intent only: it grants no runtime quota, placement, health or ingress authority.
Published ingress continues to use all selected healthy eligible regions.

## Validation

Memory and PostgreSQL contract tests exercise replay, conflicts, quota races,
receipt retention and transactional rollback. HTTP tests exercise two-cell
creation, uncertain response recovery, malformed proofs, ownership and origin
changes, redirects and cancellation. CLI tests check environment-only regional
credentials and deterministic target requests. Native VM acceptance remains a
separate requirement for enabling regional runtime admission.
