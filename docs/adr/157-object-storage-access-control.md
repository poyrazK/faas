# ADR-157: Provider-neutral object-storage access grants

Status: accepted

Date: 2026-09-05

## Context

Gregale provisions logical buckets through a provider-neutral registry and
issues short-lived S3-compatible URLs. Reusing `apps:read` and `deploy:write`
made every key with either broad application permission capable of reaching
every bucket in its account. It also made application credentials difficult to
scope to one bucket and coupled storage authorization to unrelated compute
operations.

Provider-native tenant credentials are not portable: IAM users, access keys,
policies, revocation and billing differ across OVH, AWS, R2 and Ceph. Gregale
must keep that variation outside the customer contract.

## Decision

Add three Gregale API-key scopes:

- `storage:manage` creates/deletes logical buckets and manages grants.
- `storage:read` permits object listing and GET URL issuance.
- `storage:write` permits object deletion and PUT URL issuance.

Non-admin data-plane keys also require an explicit grant on the logical bucket.
A grant is `read`, `write`, or `read_write`; both the matching global scope and
matching bucket grant are required. `storage:manage` does not implicitly grant
data access. Dashboard sessions and `admin` keys retain full access without
grant rows.

Grants reference Gregale bucket and API-key IDs, never provider identities or
credentials. The API exposes list/upsert/delete operations below each bucket.
Data-plane bucket listing is grant-filtered; management principals see the full
catalog. Key rotation copies grants to the successor, and normal bucket
soft-deletion removes them. Revoking a grant blocks new API operations but
cannot retract a provider URL that was already signed; the URL remains valid
until its bounded expiry.

The first version does not issue provider-native credentials, inject an API key
into workloads, constrain grants by object prefix, or implement workload
identity. Applications can store a narrowly scoped Gregale API key using the
existing secret mechanism and call Gregale for signed URLs.

## Consequences

Authorization remains stable when the configured S3 provider changes. A leaked
read key cannot upload/delete, a leaked write key cannot read, and neither can
reach an ungranted bucket. Operators must create and rotate an application key
and grant it to each required bucket. Prefix policies and automatic workload
credential delivery remain later hardening milestones.

## Rollout

Apply the access-control migration before deploying API replicas. Deploy all
replicas before minting keys with the new scopes. Existing `admin` keys and
dashboard sessions continue working; old `apps:read`/`deploy:write` keys lose
object-storage access when the new API is deployed. Keep `s3_enabled=false`
during a mixed-version rollout.
