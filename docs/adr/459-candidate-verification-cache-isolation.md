# ADR-459: Candidate verification cache isolation

Status: accepted

Date: 2026-10-03

## Context

The ADR-093 deployment challenge pins post-readiness probes to an unpromoted
candidate. The response-cache path runs before target selection, however, and
only bypasses ordinary Authorization and session credentials. A valid challenge
can therefore receive a serving deployment's cached body instead of reaching
the candidate. On a miss, the cache writer can store the candidate's body under
the customer route before promotion. ADR-433's upstream-response proof prevents
false verification but does not prevent cache pollution or false failures.

## Decision

Validated candidate probes bypass response-cache lookup and capture, including
fresh, stale-while-revalidate, stale-while-waking and stale-if-error paths. They
cannot launch a customer cache refresh or replace the headers retained for
synthetic customer HEAD responses. The foreground cache admission gate uses the
challenge verdict already established for the request, rather than rechecking
it after a potentially slow policy evaluation.

After validating the challenge, arm the existing response recorder to enforce
Cache-Control: no-store when headers are committed. This applies to candidate
responses and subsequent gateway failures, over both HTTP and VMMD forwarding.
Guest headers and customer edge-header rules cannot override this policy.

Cache bypass is granted only to an app-and-deployment-bound, unexpired challenge.
A public smoke marker or invalid token retains normal authentication and cache
behavior. Customer traffic continues to use the serving deployment and its
existing cache. The candidate-response proof and promotion contract remain
unchanged; gateway errors and cached replies cannot prove candidate readiness.

## Consequences

Verification exercises the candidate's upstream response even when a customer
cache rule matches the health or root path. Candidate bodies and headers cannot
escape through the platform response cache before cutover. Downstream caches
that honor Cache-Control will not retain verification replies; this does not
override independently configured external cache policies.

No database, receipt schema, quota, deployment-state or VM lifecycle changes.
TestDeploymentSmokeCacheIsolation pins fresh/stale reads and miss capture;
TestDeploymentSmokeCannotServeStaleOnWakeFailure preserves failure evidence;
TestDeploymentSmokeInvalidChallengeRetainsCacheAndAuth pins authorization;
TestDeploymentSmokeNoStoreOverridesGuestAndEdgeHeaders covers both forwarders.
