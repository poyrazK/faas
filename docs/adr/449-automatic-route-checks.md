# ADR-449: Durable automatic route checks and freshness

- Status: Accepted
- Date: 2026-10-02
- Related: ADR-448 (saved intent), ADR-445 (coverage), ADR-438 (reviewed apply)

## Context

An explicit check can be omitted after capture. Saving intent does not retain a
verdict that CI or a dashboard can retrieve. A historical pass must not imply
that changed requirements, capture or current configuration still satisfies it.

## Decision

Keep one `automatic_route_checks` row per deployment, with a durable request
identifier, fencing lease, retry schedule and latest successful evaluation.
PostgreSQL triggers enqueue work inside capture/intent transactions; memory
stores mirror these handoffs under their mutex. Content/hash and truncation
changes enqueue; duplicate captures and unchanged normalized intent are no-ops.
Capture deletion queues incomplete evidence. Intent writes queue retained owned
captures and existing jobs; migration backfills saved intent with captures.

Every changed input request supersedes an older lease. Claim ready work using
SKIP LOCKED, an opaque lease token and an expiring claim. Publication/failure
requires the exact request and lease token, plus an unexpired lease. Old or
superseded workers cannot overwrite newer work. Attempts and exponential retry
backoff are capped; retries continue until recovery, supersession or deletion.
Expose only a stable failure code, never raw failure context. Preserve prior
evidence during pending/running/retrying work. Cascade rows on hard deletion.

Run a bounded sequential apid worker in PostgreSQL and local development modes.
Use the existing snapshot check/evaluator with current saved intent, capture and
configured policy; retain capture hash and truncation even when inventory is
unavailable. A compact result exceeding 16 MiB becomes a small unknown result
rather than a partial pass or endless deterministic retry. The database allows
32 MiB JSONB text to account for JSONB spacing; the compact artifact limit stays
16 MiB. All new runtime bounds live in pkg/api/limits.go; SQL uses sqlc.

GET `/v1/apps/{slug}/route-requirements/checks/{deployment}` returns the latest
result plus queue state and separately computed freshness. Read current intent,
policy, capture and result in one repeatable-read transaction or memory mutex.
Compare intent revision/hash, configuration hash and capture hash/truncation.
Keep historical verdicts intact and report stale reasons. Configuration/policy
changes require explicit refresh in this slice; they are detected on every read.
POST the same path with `/refresh` and an empty body to durably request current
evaluation; coalesce with pending work and reset a failed retry. These actions
use read scope and MFA. Recheck captured-contract entitlement in the snapshot
before returning stored evidence, including after a plan downgrade.

Expose `routes results --deployment ...` with optional revision pin, refresh,
wait, secure new-file export and requirements gate. A passing CI result must be
complete, current and satisfied. Print/export validated evidence before gate
failure or wait timeout. Preserve existing explicit `routes check` behavior.
Generate SDKs and CLI metadata from the documented API/manifest.

## Consequences

Captures and saved intent produce retrievable verdicts without a manual CLI
invocation. Missing/incomplete captures remain unknown. Only the latest result
per deployment is retained; this is not an immutable history. Read freshness is
current at its snapshot, not a runtime or promotion guarantee. There are no
application probes, route-policy mutations, external notifications or automatic
deployment gates. Policy drift can be refreshed explicitly by customers or CI.

## Validation

Memory and PostgreSQL tests cover backfill on intent save, exclusive claims,
lease expiration, retry scheduling, duplicate suppression, superseded workers,
missing capture, configuration drift, tenant isolation and plan downgrades.
API/worker tests exercise capture-triggered evaluation, new uncovered operations,
MFA, scopes, refresh and privacy. CLI tests cover freshness gates, response
binding, output before failure/timeout, waiting and protected export targets.
Run SQL generation, OpenAPI parity, SDK builds, docs checks and scoped lint.
