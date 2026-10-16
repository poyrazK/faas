# ADR-202610160959 · Internal hosting custom domains and managed HTTPS

- **Status:** accepted for a default-off internal pilot; provider qualification is pending.
- **Date:** 2026-10-16
- **Amended by:** [standard-plan recovery](202610161106-standard-plan-hosting-domain-recovery.md), replacing the Enterprise metadata prerequisite below.
- **Amends:** [internal static hosting](20261015-internal-static-hosting.md) and [durable routing](202610151515-durable-static-hosting.md).

## Decision

Add account-owned custom-domain intent to durable static sites. Apid remains the
only intent writer and reconciles Cloudflare for SaaS hostnames, managed DNS-TXT
certificates, and individual self-hosted Access applications. No customer VM,
builder, daemon, or public listener is introduced. This pilot supports five
DNS-only CNAME subdomains per site and hostnames up to 64 ASCII bytes. Apex
domains, wildcards, public hosting, and automatic hostname reassignment are
excluded. Limits and controller budgets live in `pkg/api/limits.go`.

Admission has an independent default-false `frontend_hosting_domains_enabled`
runtime flag. The operations console and Worker have separate default-off
switches. Existing domain reconciliation continues when management is disabled.
MFA, operator allowlisting, scopes, project/account ownership, and the existing
hosting gate apply to every endpoint, including idempotency replay.

Normalize names through IDNA; exclude public suffixes, apex domains, IPs, and
every platform/hosting registrable domain. Reserve a hostname globally before
any provider write. Both a unique Gregale TXT challenge and an exact CNAME to
the isolated pilot target must resolve before provisioning. Keep hostname
reservations after removal so stale jobs cannot transfer a hostname between
sites. The provider verifies the actual zone account/name and active fallback before
provisioning. Provider ownership markers and pinned configuration identity fence
adoption, retries, and removal; unrelated provider resources are never adopted.

Cloudflare issues and renews certificates after its own hostname/certificate
validation. Return those DNS records in addition to the Gregale challenge.
Only an active hostname, active certificate, and owned Access application
using the internal reusable policy can become an enabled alias. The Worker
checks the domain-specific Access audience and internal email list on every
request, then checks the site's canonical durable routing head. Aliases never
fall back to leased KV routing. Immutable file/manifest caches are shared with
the canonical site after authentication; releases and routing intent are not
copied per hostname.

## Failure and removal contract

Domain rows form a persistent, bounded retry queue with processing leases.
Revision, generation, and lease-token comparisons fence late provider results,
publication acknowledgements, and retry schedules. Provider create responses
can be lost: exact ownership markers recover the already-created resources.
Ambiguous/foreign markers fail closed. Active provider checks run every 30
minutes; pending work retries every minute, ten claims per pass and two workers
within a 30-second dispatch budget. Provider errors are bounded codes, never
raw responses or credentials.

Each alias is a small generation-fenced R2 head outside collectible file
prefixes. Publish a terminal disabled head before provider removal. Preserve
its receipt through at least two metadata cache lifetimes plus storage timeout
(150 seconds). Then remove only owned hostname/Access resources and purge the
domain row. Provider failures retain cleanup metadata. Site deletion atomically
marks all domains deleting and cannot purge their catalog prematurely. Retain
R2 tombstones and SQL hostname reservations for the lifetime of the pilot.

Do not roll back to older apid writers while custom-domain rows exist. Older
site deletion code does not advance the domain queue. Downgrade rejects a
nonempty domain catalog or reservation table. A drained pilot's retained reservations and tombstones
must survive redeployment; schema recreation is not a hostname-transfer flow.

## Provider prerequisites and evidence

Use a dedicated SaaS zone on a separate registrable domain. Its zone-wide
`*/*` Worker route and non-serving fallback are explicit Terraform opt-ins.
Cloudflare for SaaS must already be enabled, including custom metadata support
for durable provider ownership markers. Provision a narrowly scoped domain
token separately from the existing KV token. The reusable Access policy stays
Terraform-owned; apid owns only its individual domain applications.

The [current provider plan matrix](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/plans/)
lists custom metadata as an Enterprise paid add-on. This is a material pilot
prerequisite, not an assumed free capability. Supporting ordinary SaaS plans
requires a separate recovery protocol; do not remove ownership checks merely
to work around missing provider entitlements.

Cloudflare documents that Access does not apply to custom hostname zones in
the SaaS provider's own account. Use external DNS or a separate account; the
Worker's independent audience/signature check still denies unprotected access.
Local SQL, provider fixtures, Worker tests, and browser checks do not establish
real account entitlements, routing, certificate issuance/renewal, or Access
propagation. Run the [domain qualification procedure](../internal-frontend-hosting-domains.md)
before using real internal sites.

Provider references: [Worker fallback routing](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/start/advanced-settings/worker-as-origin/),
[custom hostname creation](https://developers.cloudflare.com/api/resources/custom_hostnames/methods/create/),
[custom hostname Access](https://developers.cloudflare.com/cloudflare-for-platforms/cloudflare-for-saas/security/secure-with-access/),
[Access application creation](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/applications/methods/create/).
