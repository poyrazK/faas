# ADR-20261019120048: Saved deploy sessions

- **Status:** Accepted
- **Date:** 2026-10-12
- **Decision:** Persist individual CLI deploy sessions and recover them with `gregale deploy resume SESSION_ID` or `gregale deploy resume --app SLUG`.

Each single-app deploy records its app identity, selected connection, submission
outcome, deployment ID, upload identity when available, readiness/rollout mode,
and unfinished lifecycle/scaling settings. Settings are captured before submission,
so recovery does not reload a changed manifest or require local source. Records
exclude credentials, environment variables, secret values, source bytes and
submission payloads. Connection isolation uses a digest of endpoint, profile and
credential; credential rotation requires the original credential to select an
existing session. Records use private files, atomic replacement, and a per-session
lock. A newer deploy creates a separate record. Records remain local until removed
by the user; project apply retains its existing recovery workflow.

Resume never creates an app, uploads source or submits a deployment. It verifies
the saved app and deployment identity, reconnects to readiness and any required
safe rollout, then completes saved settings. An uncertain upload is read using
its saved identity and geometry; only a matching committed receipt can establish
acceptance. Other uncertain transports remain uncertain and direct callers to
inspect history. Missing, expired or open upload sessions cannot prove rejection.

Each pending settings step stores the targeted fields' prior and desired values.
Resume treats fields already at the desired value as complete, including a lost
PATCH response. Other fields must still match the prior value. It also requires
the exact deployment to remain the newest app deployment before writing pending
settings. Conflicts stop recovery without overwriting the changed fields. The
existing GET/PATCH API has no atomic compare-and-set; edits racing between the
check and PATCH are outside this initial guarantee. Server-side conditional app
updates are a future extension, not implied by this client-side check.

ADR-20261019120047 adds server-side preconditions to pending recovery writes, closing this
initial race. Older servers reject guarded recovery updates without an unchecked
fallback. Saved session records remain compatible with the guarded client.

Receipts and error recovery include a session ID and copyable resume command.
Waited JSON success is emitted after unfinished settings have completed. Resume
uses the original readiness, rollout and dark-deploy semantics and does not
promote a dark deploy. Terminal deployment or rollout failure cannot complete
pending settings. Existing staged trigger/profiling/tracing acceptance handling
from ADR-20261019120046 remains in force.

ADR-20261019120049 adds durable server acceptance receipts for image, multipart and source-ref
attempts, closing the unknown-acceptance limitation for newly saved attempts.
