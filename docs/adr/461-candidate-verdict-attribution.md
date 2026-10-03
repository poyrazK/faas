# ADR-461: Candidate verdict attribution and durable verification recovery

Status: accepted

Date: 2026-10-03

## Context

ADR-460 recovers temporary challenge-publication failures using the existing
snapshot-written notification outbox. After a successful publication, a
transport timeout or a public gateway refusal can still become a terminal
application smoke failure. An app-authored 503 and a gateway-authored 503 take
the same path because the verifier interprets error status before inspecting
ADR-433's authenticated response proof. Only connectivity success currently
requires the proof; explicit HTTP health checks accept a deployment ID alone.

## Decision

Every candidate check requires matching deployment identity and a proof bound
to the current challenge before interpreting a response as an app verdict.
The gateway already strips guest/inbound evidence headers and writes this
proof only after receiving candidate upstream headers, over both supported
forwarders. Selecting a target is insufficient evidence. Candidate redirects
are never followed for either contract; a redirect remains connectivity
evidence, while an explicit HTTP health path continues to require 2xx.

A proven candidate response retains ordinary health/status evaluation and its
existing verifier retry budget. An unproven error response, missing/invalid
proof, or different deployment yields a typed unavailable-verification error.
Transport errors, including a truncated otherwise acceptable response, yield a
separate typed transport reason. They remain unattributed: the response cannot
establish whether the app, gateway, network, or platform caused the failure.
A proven unhealthy HTTP status remains a negative app verdict even if its
diagnostic body cannot be read. Malformed/non-HTTP origins and absent verifier
configuration remain terminal configuration failures. The legacy unpinned
Verify wrapper retains its ordinary status-based behavior.

Use fixed safe error text and bounded reason codes. HTTP status is retained;
sanitized problem identifiers remain diagnostics and cannot establish origin.
Underlying transport causes are available through errors.Is but their text is
excluded from receipts and outbox logs because it may contain a challenge or
URL credential. No response body or token is stored in recovery progress.

Reuse ADR-460's existing outbox, attempt fence, five-minute persisted deadline,
retry eligibility floor and cancellation guard. Persist the actual bounded
last_error_code: smoke_authorization_unavailable, smoke_gateway_unavailable,
smoke_transport_unavailable, smoke_response_unproven, or
smoke_deployment_mismatch. Switching reasons cannot renew the window.

For publication-only recovery, bound publication while retaining the ordinary
candidate probe budget as in ADR-460. For gateway/transport/evidence recovery,
also cap requests lacking a proven candidate verdict by the persisted deadline.
Once a proven candidate response arrives, ordinary app-health retries apply;
the shorter unavailable-verification deadline cannot truncate those retries.
If the deadline expires during unavailable-response backoff, retain the last
observed reason rather than creating a new cancelled request.

On recovery-window exhaustion, commit the failed receipt, terminal deployment
state and failure outcome atomically using ADR-434. Keep error
deployment_verification_unavailable, with the last actual reason in stage
progress; the message directs the user to app and gateway diagnostics without
claiming platform blame for an unknown transport failure. The predecessor
continues serving and failure events follow the commit. Completion, consumer
interruption, concurrent cancellation and evidence-write failures keep their
existing semantics. No queue, migration, public deployment state, quota or VM
lifecycle changes.

## Consequences

A temporary gateway or transport outage can recover the same deployment across
an imaged restart. A proofless 200 cannot promote a candidate, and guest-shaped
problem JSON cannot turn an unproven response into an application failure.
Operators can distinguish retry reasons without reading application bodies.

All gateways must provide ADR-433 response proof before this imaged version is
enabled for explicit HTTP health checks. Older gateways leave verification
unavailable and cannot promote a candidate. Existing outbox attempt limits and
dead-letter policy still apply during prolonged database/consumer outages; no
independent timer guarantees a terminal transition exactly at the deadline.

TestCandidateProofControlsFailureAttribution pins both contracts across 2xx,
redirects, access errors, 429 and 5xx with/without authenticated evidence.
TestVerificationTransportFailureIsUnattributedAndSecretSafe pins safe causes.
TestProvenAppVerdictRetainsOrdinaryProbeBudgetDuringRecovery pins timeout scope.
TestHostingUnavailableVerificationRecoversAfterRestart and
TestHostingUnavailableDeadlineKeepsReasonAndAtomicVerdict pin handler recovery
and terminal persistence. TestPgHostingGatewayOutboxRecoversAfterRestart and
TestPgHostingTransportOutboxRecoversAfterRestart exercise the production outbox
against PostgreSQL. TestHostingVerificationRecoveryReasonsShareDeadline pins
PostgreSQL/memory parity across changing outage reasons.
