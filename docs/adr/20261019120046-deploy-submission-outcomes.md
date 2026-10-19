# ADR-20261019120046: Preserve configuration when deployment acceptance is uncertain

- **Status:** Accepted
- **Date:** 2026-10-11
- **Decision:** Classify CLI submission outcomes as accepted, rejected, or unknown. Compensate staged triggers, profiling, and tracing only after definite rejection.

A deployment can be accepted even when its reply is lost or a subsequent status
read fails. A confirmed deployment ID or an already-committed upload response
ends configuration compensation independently of status retrieval. Unknown
outcomes preserve configuration and recovery metadata, report a nonzero exit,
and direct the caller to inspect or retry the same intent. Definite request
rejection and failures before submission retain compensation.

After an uncertain resumable commit, Gregale makes one bounded upload-session
read using a fresh context. A matching committed session confirms acceptance;
an open, missing, expired, or unavailable session does not prove rejection. The
original error and cancellation exit remain visible even when this read recovers
a deployment ID. Accepted IDs reach the queued callback so guided sessions can
save them before reporting a confirmation error.

Checkpoints record commit attempts before sending the request. This optional
version-3 field carries uncertainty across processes and prevents replacing an
upload whose commit may already have admitted a build. A definite rejection
clears the attempt marker; a transient response followed by a rejection remains
unknown. Existing safe replacement of sessions that never attempted commit is
unchanged. Unknown outcomes do not schedule background compensation; callers
inspect, retry the same intent, or explicitly change the retained settings.
If updating an existing checkpoint before commit fails, submission stops rather
than leaving a stale record that a later process could treat as uncommitted.

Treating every submission error as rejection was rejected because it can undo
configuration needed by a running deployment. Treating negative session reads as
authoritative rejection was rejected because session state can lag admission.
This extends ADR-20261019120044 and ADR-20261019120045 without adding a server endpoint or changing
build admission, rollout, or readiness semantics.

The Go SDK prevents implicit HTTP transport replay for image submission and
upload commit. Otherwise a lost response followed by a transport-internal retry
can hide acceptance behind a later rejection before Gregale sees the error.
Upload commit sends an ignored empty JSON object so a keyed, bodyless POST is
not automatically replayed. Explicit CLI commit retries retain their existing
wire key and carry uncertainty across attempts. Read requests retain normal
transport recovery. Compensation uses a fresh bounded context after a definite
pre-submission cancellation, and JSON errors keep one Problem envelope.
