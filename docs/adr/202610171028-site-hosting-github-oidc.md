# Site-scoped GitHub identity for internal frontend previews

Status: Accepted, internal and default off. Live GitHub/provider qualification deferred.

## Context

Site-scoped CI keys reduce authority but still need storage and rotation in GitHub
secrets. Existing account OIDC exchange can bootstrap application deployment
trust; its account/app authority is unsuitable for hosting previews. A CI retry
must preserve its original release, preview and expiration even after credentials
expire.

## Decision

Use a separate anonymous, authentication-rate-limited hosting exchange with a
fixed GitHub issuer, fixed JWKS endpoint, RS256 only, and site-specific audience
`gregale:hosting:SITE_UUID`. Never bootstrap trust from source labels or discover
it from a JWT. An authenticated, MFA-protected operator explicitly creates an
immutable site binding containing numeric repository owner/repository IDs, exact
repository name, branch ref, workflow ref, event and optional GitHub environment.
Only `push`, `workflow_dispatch` and `schedule` events are accepted; neither pull
request event is supported. Changing any rule requires revocation and a new ID.
Custom subject formats do not substitute for the required signed claims.

After signature and claim verification, require a nonempty subject and strictly
future expiry within ten minutes. Missing expiry and already-expired assertions
fail even within the generic verifier's clock skew. Issue a hashed `fp_host_`
bearer expiring at min(assertion expiry, now + five minutes), with only stage and
preview operations for that site. No promotion, retirement, retention, trust/key
administration, account or application access is granted.

Reuse the credential catalog with a nullable composite trust foreign key. Site
locking serializes issuance quotas; lock and compare the immutable trust again
inside issuance so revocation during JWKS verification cannot resurrect it.
Trust deletion cascades to all its sessions. Authentication performs an uncached
lookup on every request, so revocation affects the next request. In-flight
operations and already issued upload grants retain their existing semantics;
revocation does not remove staged releases or previews. Manual keys use their
existing quota of 16, independently of 32 live OIDC sessions/site. Expired rows
are pruned on issuance. At most eight trust records/site; site deletion cascades.

Asserted JWTs are bearer identities and may be exchanged again until their short
expiry, subject to rate/session quotas. They are not one-use receipts; neither
assertions nor returned plaintext Gregale credentials are persisted or put into
idempotency receipts. Failed responses require an ordinary fresh exchange;
credentials are never replayed from a stored response.

The CLI obtains a fresh GitHub identity when needed, exchanging and renewing
before subsequent API calls. It never retries a failed mutation or allocates a
new CI run to renew. The durable run ledger remains the authority for retries
and immutable preview expiration. Dedicated HTTP clients reject redirects;
GitHub's request credential is sent only to an HTTPS subdomain of
`actions.githubusercontent.com`, and the signed JWT goes only to the configured
HTTPS API origin. Gregale credentials stay on that origin and selected site's
hosting surface. Uploads keep their existing separate unauthenticated client.
Secrets remain in process memory/environment, never argv, files, outputs or
logs. OIDC mode ignores workstation login credentials and rejects `FAAS_TOKEN`.

API hot flag `frontend_hosting_oidc_enabled` (environment seed
`FAAS_FRONTEND_HOSTING_OIDC_ENABLED`), CLI workstation opt-in, Action
`oidc-enabled: true` and dashboard `NEXT_PUBLIC_FRONTEND_HOSTING_OIDC_ENABLED=true`
are separate and default off. Main hosting, durable routing and automation must
also be enabled. The API switch blocks already minted OIDC bearers while manual
keys retain their existing controls. Provider Terraform only emits `0`.

## Validation and limits

Hermetic signed-JWT tests cover repository/fork/owner/ref/workflow/event/environment,
audience, signature, expiry and hot gates. Memory and PostgreSQL tests cover
ownership, manual/session quota separation, quota concurrency and revoke/mint
races. Client virtual-time tests cover renewal, retained intent and failure
without replay; HTTP tests cover redirect and destination separation. Real route
and browser fixtures cover operator management and flag controls.

No GitHub installation, webhook, build execution, PR preview admission, production
auto-promotion, GitHub comments/checks or live provider changes are introduced.
Trusted workflows must isolate untrusted build output from identity-bearing jobs;
this binding authorizes a workflow identity, not the provenance of its artifacts.
