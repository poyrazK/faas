# ADR-202610171907: Organization-owned object storage

Status: accepted
Date: 2026-10-17

## Context

Company applications, projects and PostgreSQL survive creator erasure, but their
object buckets and storage credentials still depend on a human account. Removing
a person must revoke their storage authority without disabling application keys
or deleting company objects.

## Decision

Persist each bucket's immutable organization from its application. Preserve the
app/environment placement, storage namespace, bucket UUID, physical location,
object keys, versions, provider evidence and sealed application credentials.
Object-storage catalogues and mutation-event outbox records reference resource
namespaces rather than identities. Personal erasure removes personal event
records while retaining verified company bucket events. Personal exports filter
the nested storage-event envelope as well as direct bucket references.
Adopted company data and accounting survive creator erasure; personal resources
still require confirmed provider cleanup. Organization deletion requires bucket
cleanup as well as database cleanup.

Review bucket inventory, creation/deletion, object listing, user S3 credentials
and app/environment compute bindings explicitly. Owners/admins manage buckets
and user credentials; developers can manage bindings and read/write through
authorized storage credentials; viewers inspect metadata. Billing seats can
read company usage. Other storage control-plane operations remain closed to
shared access until reviewed. Existing application capabilities continue under
their original durable authority. Source manifests retain existing restrictions
on unreviewed data-service creation and cloning.

Human S3 keys record the actual issuer and membership revision. Any role change
or departure revokes these keys, including requests signed before revocation.
Rejoining never revives a key. Managed application bindings and their rotation
stages are company authority and survive creator departure. Existing public
upload routes remain tied to their receipt and app; human-issued branded URLs
retain their issuer/key checks. Check membership before cached responses and
serialize storage intents against offboarding using the existing state fence.

Company quota admission and fresh provider usage use the permanent organization
billing subject after adoption, across legacy namespaces. Preserve historical
reports, finalized billing records, delivery receipts, provider customer handles
and rate cards. Invalidate mutable current-period usage heads on ownership
cutover so an old mixed personal/company aggregate cannot be billed twice.
Retain the exact immutable cutover timestamp, reject report imports observed
before it, and allocate historical export periods using the UTC cutover month.
Require fresh, correctly allocated evidence before admitting further usage or
finalizing charges. Personal inventories and exports exclude company resources.

## Acceptance and rollout

Prove role/scope and cross-organization isolation, offboarding races, quota
serialization, retained accounting and creator erasure. Through the S3 gateway,
prove that a teammate and existing application binding can use the same bucket
after erasure, while the former member's user key is rejected. Database and
provider identities, object data and sealed binding material must stay stable.
Remediate inconsistent roots and finalized current-period billing conflicts
before migration. Deploy compatible API writers, S3 gateways and workers before
enabling creator erasure. Native deployment acceptance remains a release gate.
