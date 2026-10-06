# ADR-429 · Handled internal service request evidence

- **Status:** accepted
- **Date:** 2026-10-02
- **Amends:** ADR-168, ADR-234 and ADR-423
- **Issue:** #4083
- **Context:** Internal service forwarding bypasses the public handler's request
  recorder. Minute aggregation also cannot prove that a worker handled a new
  request after a same-minute scenario baseline. Cleanup phase reports inherit
  earlier failures even when the actual resource teardown succeeds.
- **Decision:** Publish target-side debugger evidence on the first actual vmmd
  response, and preserve exact evidence for registered scenario targets.
- **Why:** An actual guest response establishes handling; a selected target,
  platform error or synthetic chaos response alone does not.
- **Consequences:** Internal observations use the existing bounded ring and
  publisher. They do not add financial usage. Scenario rows retain exact
  timestamps and instance identity; ordinary production rows still aggregate.
  Cleanup phases report their own outcome while preserving overall failure.
- **Rejected alternatives:** Accepting platform responses, dropping strict warm
  evidence, extending deadlines or treating successful cleanup as test success
  would conceal the failed behavior. Counting debugger observations as new
  financial facts would change ingress accounting and couple it to a lossy sink.

The trusted service resolver and authorizer supply target app and account
identity. Each forwarding attempt installs a fresh existing first-byte marker;
the HTTP/raw bridge stamps it only after an actual response-init frame from
vmmd. The first response-header observer records that attempt's selected node,
instance, deployment and provenance. A response status of 4xx/5xx still proves
that a guest handled the request. Transport failures, authorization/admission
errors, binding probes and synthetic chaos status faults do not. An open HTTP
stream or upgraded socket does not need to reach EOF to publish this evidence.
Existing response buffering, retry, duplex, hijacking and cancellation behavior
remain intact. Latency on these internal rows measures time to the first guest
response header; it does not claim complete stream duration.

Internal dependency observations are deliberately nonfinancial. They use the
existing `usage_outboxed` suppression bit, already used for nonbillable rejected
ingress admissions, to prevent the receiver's legacy usage fallback. This bit
does not itself attest an outbox append. No usage event, consumer identity or
platform-tenant identity is copied from the caller. Ingress durable accounting
and VM residency metering retain their existing owners and semantics. No wire,
schema, quota or price changes occur.

Only a nonempty scenario namespace identity supplied by the authoritative
resolver enables exact timestamp/instance preservation. Guest headers cannot
enable it. These rows retain generated event IDs across publisher retries.
Production traffic keeps minute/latency buckets. The existing 4096-row ring,
batch sizes, finite retries, receiver plan/rate gates and overwrite/drop counters
bound both modes; debugger evidence is not a durable financial ledger. Paths,
queries, bodies and credentials are not persisted; the route uses the bounded
other-route label. The debugger kill switch remains effective, and a missing
receipt must fail qualification.

The CLI retains its exact postbaseline timestamp cutoff, rejection of new wakes
or cold boots, and 15-second telemetry deadline. Fault scenarios must still
produce an actual guest response (for example, after their bounded lease expires)
to qualify a warm worker. Resource cleanup enters a separate phase on early
returns as well as normal returns. Its status derives from actual cleanup errors;
prior assertion/provision errors still keep the overall result failed. A failed
workload destroy still prevents releasing its namespace.

Acceptance includes actual bridge response frames versus transport errors,
per-attempt target identity, stream/upgrade observation before EOF, exact scenario
rows versus ordinary aggregation, privacy and financial suppression, same-minute
warm evidence and late-smoke exclusion, and real CLI/API teardown paths following
failure. Signed fleet rollout and live warm fault/expiry retests remain required.
This change does not qualify native VM lifecycle or physical leak acceptance.
