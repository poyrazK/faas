# ADR-433: Candidate connectivity and HTTP health verification

Status: accepted

Date: 2026-10-02

## Context

Direct OCI images without an HTTP health endpoint have a TCP readiness
contract. Their later public smoke nevertheless requires GET / to return 2xx,
rejecting ordinary APIs whose root route returns 404 or application-owned auth
responses. The selected deployment header is emitted before forwarding, so it
alone cannot establish that an upstream application actually responded.

## Decision

Preserve strict 2xx health verification for source/function deployments and
explicit HTTP health paths, including an explicit root path. Direct OCI images
with no effective HTTP health path use candidate route connectivity at /.
Accept 2xx, 3xx, 401, 403, and 404 only with matching selected deployment and
gateway-authored upstream-response proof. Continue rejecting 429 and 5xx.

Only an authorized smoke request installs the candidate-response context.
The VMMD HTTP stream forwarder stamps proof after receiving response headers;
the legacy HTTP proxy stamps it after receiving an upstream response. Both
strip guest-authored proof. Proof is an HMAC-SHA256 of the candidate identity
under the short-lived smoke token; that token is stripped before guest forwarding.
Target selection, customer headers, cached route
state, and gateway errors cannot create this evidence. Connectivity probes do
not follow redirects or send their challenge to another origin.
Candidate probes bypass the gateway's generic retry picker, which could select
the serving predecessor; retries remain owned by the candidate verifier.

Extend schema-v1 smoke evidence additively with verification and authentication
fields. Empty verification on historical receipts retains the HTTP health
interpretation. Platform-challenge access is explicitly distinct from customer
or anonymous access. Connectivity is explicitly distinct from endpoint health.
Preserve promotion sequencing: proof must pass before the live pointer changes,
and failure leaves the predecessor serving.

## Consequences

Healthy stateless APIs need no synthetic root health endpoint. Explicit health
checks remain the production health contract. Gateways must receive proof
support before imaged enables connectivity verification; a mixed rollout fails
closed and retains the previous release. No VM lifecycle, quotas, database
schema, or public smoke authorization boundary changes.

Acceptance is pinned by TestRouteVerificationContract,
TestDeploymentRouteSmokeAcceptsGuestRoot404,
TestBridgeSmokeProofRequiresUpstreamHeadersAndAuthorization, and
TestHostingVerificationContractControlsCutover.

## Amendment (2026-10-05): gRPC apps

`app_protocol=grpc` apps, and any deployment whose startup readiness is the
standard gRPC health check (`deploy --healthcheck-grpc` or a manifest
`healthcheck.grpc`), also use candidate route connectivity. Their HTTP health
smoke sent `GET /healthz` to a gRPC server. The server answers that with 415,
as the gRPC-over-HTTP/2 spec requires, so on production-us every gRPC deploy
passed in-VM gRPC readiness and then failed "health probe returned HTTP 415".
Readiness is already proven by the gRPC health check inside the VM.
Connectivity verification adds 415 to the accepted set, under the same
matching-deployment and upstream-proof requirement. An unproven 415 remains
unavailable evidence. Pinned by TestRouteVerificationContract and
TestRouteConnectivitySmokeForGRPCApps.
