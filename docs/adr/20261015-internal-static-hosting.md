# ADR-20261015 · Internal static frontend hosting

- **Status:** accepted for an internal, default-off pilot; external launch is not qualified.
- **Date:** 2026-10-15
- **Amends:** implementation spec §4.1 and §4.2 for a separate static-site lifecycle; customer apps retain their existing VM invariants.

The separately gated durable mode in
[ADR-202610151515](202610151515-durable-static-hosting.md) amends routing and
cleanup for newly opted-in sites. The leased mode below remains the default.

## Decision

Add static sites under existing projects and registered environments. `apid`
owns site/release intent and publication. Static files never enter `instances`,
`schedd`, `imaged`, or `vmmd`. No new daemon or public listener is introduced.

The `frontend_hosting_enabled` runtime setting defaults to false and bootstraps
from `FAAS_FRONTEND_HOSTING_ENABLED`. All internal hosting routes require MFA,
the appropriate API scope, and the existing operator email allowlist. Handlers
repeat the feature/operator gates. A customer key carrying `admin` is not an
operator credential. Every resource lookup also enforces account ownership.

The first artifact backend is an operator-provisioned private R2 bucket. The
CLI hashes a prebuilt directory, requests short-lived staging upload grants,
and sends bytes directly to storage without Gregale credentials. Publication
verifies every declared size/hash before conditionally creating immutable
published objects. The published manifest is written last. Customer upload
grants never target published objects. Retries can only reuse identical bytes.
Sites pin the endpoint/bucket fingerprint; changing placement fails closed and
never silently reads or deletes from a replacement backend.

The internal operations dashboard reuses this lifecycle behind an additional
default-off build flag. A guarded capability read supplies limits and the
private upload base URL without credentials. An explicitly configured, exact
HTTPS origin allowlist permits only browser PUT/content-type CORS. Staging
signatures remain the authorization boundary; browser requests omit Gregale
credentials, reject redirects, and bind each grant to its site/release/path.
The dashboard hashes bounded files, renews grants, and verifies before promoting
with the reviewed revision. It distinguishes publication receipts from global
serving health and retains uncertain/cancelled release work for operator review.

Promotion checks the expected site revision and requires a ready release owned
by that site. The active pointer and revision commit together in PostgreSQL.
Rollback promotes a retained ready release through the same operation. Durable
site intent is reconciled in bounded pages into Cloudflare KV; a full scan
repairs missed notifications and process restarts. KV is a serving read model,
not an atomic global promotion authority. Edge locations may briefly serve
different complete releases during propagation.

An independently default-off Cloudflare Worker serves files directly from R2.
Every request must pass its deployment flag, a cryptographically verified
Cloudflare Access JWT (issuer, audience, expiry, and internal email), and an
unexpired, enabled hostname/site/origin/release route **before cache lookup**.
Cache keys include site, release, path, and digest. API/Worker flags and allowlists
are configured explicitly; neither a forwarded email header nor an admin scope
alone grants access. Customer content uses a separate registrable domain from
the platform application/dashboard domain.

## Resource and failure contract

- All limits are centralized in `pkg/api/limits.go`: 25 sites/account, ten
  retained releases/site, 1,000 files/release, 32 MiB/file, 100 MiB/release,
  and a 512 KiB manifest. Publication buffers one bounded file at a time and
  has one concurrent publication slot per API process.
- Routing leases last 120 seconds and reconcile every 30 seconds. A disabled
  API immediately refuses new operations; existing enabled edge read models
  cannot outlive their lease. The Worker deployment switch is another direct
  stop. Existing staging upload grants can last five minutes but cannot
  publish or activate a release.
- The finite lease deliberately sacrifices serving availability if the
  control plane/provider cannot renew it. This is an internal kill-switch
  tradeoff, not a production control-plane independence claim.
- Each configured site costs one KV write per reconciliation pass (about
  86,400 writes per month at a 30-second interval), plus request-path KV reads,
  Worker/Access usage, R2 requests, and stored bytes. These are external costs;
  no customer billing or cost-free hosting claim is introduced.
- Retiring an active release is refused. Site deletion clears the active
  pointer and retires its releases atomically. Cleanup waits past both route
  leases and upload grants, deletes only catalogued objects from the pinned
  backend, then removes their metadata. Failed cleanup is retried even if
  admission is disabled. Account/project deletion cannot cascade away the
  artifact catalog before cleanup.
- Configure a storage lifecycle rule to expire the private staging prefix;
  do not apply an unconditional expiration policy to published releases.

## Qualification and follow-ups

Portable tests cover ownership, concurrent quota enforcement, promotion CAS,
rollback, immutable publication, operator/flag gates, JWT validation, and
guarded cache hits. A disposable, Access-protected trial must produce live
Cloudflare/R2/Access qualification before real internal applications use the
pilot; repository tests do not prove provider configuration.

The pilot includes pinned Terraform provisioning with default-off Worker and
DNS/route controls, disabled alternate public endpoints, and staging-only
expiration. The CLI-driven live runner records provider inspection, observed
file-cache hits, upload recovery, promotion/rollback, and independent object
cleanup. Its optional Worker-switch exercise is restricted to an isolated
pilot. Infrastructure tests use a mocked provider and do not qualify actual
Cloudflare permissions or serving; live evidence retains that distinction.

This pilot does not onboard arbitrary customer domains, run SSR, replicate
artifacts into US/EU origin pools, integrate site membership into project-wide
release promotion, or bill customers for CDN bytes. Those capabilities remain
explicit follow-ups. R2's signing region `auto` does not establish an EU compute
region or guarantee a particular data residency policy. External launch also
needs durable CDN usage accounting, independent serving-policy availability,
and measured resource/traffic qualification.
