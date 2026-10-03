# ADR-487: Pre-promotion API route checks

Status: accepted

Date: 2026-10-03

## Context

The post-readiness hosting check proves one health route or candidate
connectivity. That can promote an API whose startup endpoint works while a
customer-critical read route returns 404 or 5xx.

## Decision

Allow an imported OpenAPI operation to opt into a hosting route check with
`x-gregale-hosting-check: true`. Only GET operations are supported, with at
most ten selected static paths. Reject path templates, query strings, fragments,
authority overrides, traversal, and encoded separators. Do not send a body,
customer credentials, or follow redirects.

After the existing readiness smoke passes and before promotion, probe each
selected operation through the public gateway with the existing app-and-candidate
challenge. Require the gateway-authenticated response proof for every verdict.
Treat 2xx, 3xx, 401, and 403 as successful route responses; a 404, 429, or 5xx
is a candidate failure. A protected route may correctly return 401 or 403 when
the app itself owns customer authentication. A gateway, transport, challenge,
or proof failure remains unavailable evidence and retries through the existing
durable hosting-verification window.

Extend the schema-v1 hosting receipt additively with the selected route results
and the SHA-256 of the imported OpenAPI document. Persist the receipt through
the existing success or atomic failure path before changing the live pointer.
An absent imported document or a document with no marked operation retains the
current health/connectivity behavior.

## Consequences

Customers can opt in without a new endpoint, migration, credential surface, or
deployment state. A failed required route keeps the predecessor serving. The
check proves candidate route response and status, not response-body/schema
conformance or customer-authenticated behavior. OpenAPI `GET` semantics are
expected to be read-only.

Acceptance is pinned by TestVerifyDeploymentAPIRoutesRequiresCandidateProofAndAcceptsReadOnlyResponses,
TestSelectedHostingContractRoutesUsesOnlyExplicitStaticGETs, and
TestHostingContractChecksArePersistedBeforePromotion.
