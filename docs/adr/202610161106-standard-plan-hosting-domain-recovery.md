# ADR-202610161106 · Standard-plan hosting domain recovery

- **Status:** accepted for the default-off internal pilot; live qualification pending.
- **Date:** 2026-10-16
- **Amends:** [custom domains](202610160959-internal-hosting-custom-domains.md).

## Decision

Remove the Enterprise custom-metadata prerequisite. New Cloudflare for SaaS
hostname requests contain only the hostname and DNS-TXT DV certificate settings.
Persist the returned provider UUID as the ownership binding; check that exact ID,
hostname, certificate mode, and absence of custom origin on subsequent operations.
Legacy objects with metadata must still match their original claim/provider markers.
Standard provider pricing and account allowances still apply.

Before provider create I/O, apid persists a create-start timestamp and DNS proof
under the processing lease, revision and generation fences. Only that invocation
may issue a create. A crash, lost response, or failed receipt never causes another
create or adoption by hostname. This favors conservative operator intervention
over unattended orphan creation. Existing matches are conflicts, not ownership.

## Operator recovery

An independently gated, operator/MFA/scope/account-owned recovery route accepts
an explicit provider UUID, expected hostname, and reviewed revision. It reads the
exact resource from the configured zone/account and verifies its hostname and
certificate/origin mode before binding. Operators must inspect provider activity
and explicitly authorize management/deletion of the reviewed resource. If no
resource exists once the request is settled, they may create it manually and bind
its ID. There is no automatic ownership inference and no reset/retry-create API.

Recovery waits at least the processing lease plus dispatch and provider timeouts
(160 seconds), increments the intent revision, invalidates processing leases,
and records an audit event. The store repeats ownership/revision/unknown-binding
checks atomically. Recovery does not alter routing generation or deletion intent,
and remains possible while a site/domain is deleting. Unknown create results
block provider cleanup/catalog purge until recovered. Known missing IDs are
never automatically recreated. All existing feature flags remain default-off.

Local state, provider-boundary and API tests must cover lost responses, stale
workers/revisions, foreign ownership and recovery during removal. Provider plan,
DNS, issuance/renewal, Access and regional evidence remain live qualification work.
