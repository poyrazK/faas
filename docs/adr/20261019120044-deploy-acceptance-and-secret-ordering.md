# ADR-20261019120044: Deploy acceptance and secret ordering

- **Status:** Accepted
- **Decision:** Submission acceptance ends CLI configuration compensation. Project apply installs sealed secret bundles before admitting any workload build.

A wait timeout or interruption can occur while an accepted deployment is still
building. Compensating manifest triggers, profiling, or tracing at that point
removes configuration the remote operation depends on. The CLI now commits these
staged settings when submission succeeds, consistently for waited and queued
source/image deploys. Rejected submissions still compensate. This supersedes
ADR-090's compensation on failure to reach `live`; terminal build failure keeps
accepted configuration, matching the existing no-wait behavior. Lifecycle/scaling
reconciliation remains at its existing readiness boundary.

`POST /v1/projects` accepts an optional write-only `secrets` multipart field,
encoded as a JSON object of names to values. After workload reconciliation and
manifest binding creation, apid uses the secret PUT pipeline for each selected
workload in the requested environment. All secret writes finish before any build
is admitted. Existing secret scopes, plan quotas, sealing/key fingerprints,
managed-secret ownership checks, snapshot invalidation, runtime notifications,
and audit events apply. Secret-only applies also rebuild selected workloads.

Secret bundles require secrets write permission as well as deploy permission.
Scan/preview does not accept bundles. Values never enter source archives, plan
receipts, responses, logs, or audits. The multipart bound derives from existing
secret count/value limits. App reconciliation and secret writes across workloads
are not one transaction: a failed write admits no builds, but leaves durable app
rows and already written configuration available for retry. No plaintext is
persisted to stage a retry. Protected environment approval continues to authorize
the source plan; secret writes retain their separate existing authorization.

The CLI always excludes the selected secrets file from source it automatically
packs, including the original file's path in committed HEAD. Mandatory exclusions
cannot be reversed by ignore negation or disabling secret scanning. Caller-supplied
archives remain the caller's source responsibility.

Default retry identity is per invocation for mutable Git refs (branches, tags,
short SHAs) and secret bundles. This avoids replaying an older live deployment
when a ref or secret changes, without exposing secret value digests. Transport
retries within the invocation reuse the same identity. Content-derived identity
remains for archives/images/full commit SHAs without secrets. Explicit logical
keys retain their existing semantics across invocations. Resolving refs in a new
CLI preflight endpoint was rejected for this change: it adds an authorization
round trip and duplicates apid's existing authoritative resolution. Customers
needing retries across process restarts can use full SHAs or explicit keys.
