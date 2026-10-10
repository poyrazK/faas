
Isolated restore validation (ADR-944, default off) returns `bundle_sha256` and
`isolation: "networkless"` from application validation. Copy the digest into
`validation_bundle_sha256` on restore alongside `validation_deployment_id`.
Operators must register a separate deployment-bound, secret-free validator bundle
and enable disposable executions and restore isolation. Missing bundles or failed
executions fail closed; ordinary application validation remains cooperative when
the isolation gate is disabled. See the ADR for deployment and qualification limits.
