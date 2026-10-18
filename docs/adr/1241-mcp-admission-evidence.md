# ADR-1241: Retain MCP traffic admission evidence in the deployment timeline

Status: Accepted

## Context

The traffic API checks an audited policy fingerprint and optional account replay
revision, but live gateway reports change over time. Operators need a retained
record of the exact observations used to admit or reject promotion and recovery.

## Decision

Use the existing account-authorized deployment audit API and retention mechanism
with a new `deploy.mcp_admission` event. Version 1 data contains an attempt UUID,
target deployment and traffic percentage, expected and observed policy digests,
expected and observed account revisions, and an ordered snapshot of serving
node names, boot UUIDs, replay positions and observation times. Missing gateway
reports retain null values. The SQL statement samples one time for its freshness
checks and evidence timestamp. Fingerprints are evaluated at that sampled time.
No resource URL, private JWT action, token, claim value, or raw error is retained.

Accepted evidence is inserted inside the traffic transaction after all traffic
conditions succeed. Any insertion failure rolls back traffic. A rejected decision
is saved only after the traffic transaction rolls back, using the captured
observations rather than querying the now-changed fleet. Recording uses a bounded
five-second context even when the original request has been cancelled. A unique
attempt index prevents a commit ambiguity from replacing committed acceptance
with rejection. An evidence-storage failure is explicit and cannot be treated by
the CLI as a lost successful promotion response.

The shared traffic path handles promotion and recovery. Only requests that reach
the authorized MCP policy check generate evidence; invalid bodies and earlier
non-MCP lifecycle rejection are outside this timeline. Legacy fingerprint-only
requests record observations but do not enforce account convergence. In-memory
stores record no fabricated gateway or account-revision observations.

Expose retained evidence through `GET /v1/deployments/{id}/audit`, the SDK audit
readers, `gregale mcp access history --deployment ID`, MCP status, and operations
overview. History reports a recent window of up to 50 audit rows. Overview inspects
up to five recent web deployments, including candidates receiving no traffic.
Unavailable or malformed evidence is explicitly unknown, not an empty complete
history. Audit availability does not change the live protocol-health verdict.

## Consequences

Evidence follows the existing deployment audit retention and deletion behavior.
It explains a past traffic decision, not live token enforcement or future fleet
health. Requests failing before policy observation have no evidence record.
Rejected audit persistence is a separate transaction, so storage unavailability
can prevent retaining a rejection; the caller receives an explicit error.
