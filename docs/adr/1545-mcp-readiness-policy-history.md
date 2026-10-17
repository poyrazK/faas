# ADR 1545: MCP readiness policy history and restore

Status: Accepted

Policy history is owned by app and deployment environment. Readers can inspect
immutable revisions and traverse a newest-first keyset page (default 20, maximum
50). A recorded timestamp and revision tie-breaker index bound database page
reads. Existing history receives its original policy update timestamp; no policy
revisions or readiness evidence change during migration.

Configuration diffs compare enablement, endpoint, evidence maximum age and roles,
including expected catalogs, monitoring and OAuth binding references. Role order
and audit metadata are ignored. Added and removed roles have JSON null on the
missing side. No grant ciphertext, bearer tokens or provider calls are involved.

Restore requires admin scope, applicable MFA, a scoped historical revision, the
expected current revision and a fresh audit reason. It resolves the historical
configuration under the same owned-app lock and policy serialization used by
normal updates, validates current OAuth references, and saves a new revision with
new author/time/reason and `restored_from`. It never rewinds or edits old history.
The HTTP surface also applies current app/monitor applicability checks.

Fresh probes are required under every restored revision. Prior evidence,
in-flight probe attempts, monitor completions and unused emergency overrides are
bound to the previous revision and cannot qualify the restored policy. Other
environments retain their own revisions and evidence. A disabled historical
configuration explicitly restores the usual disabled-policy behavior.

The API exposes history, revision lookup, diff and restore. CLI history, diff and
restore accompany the existing current-policy get/set commands. Dashboard history
is read-only, environment-filtered and HTML-escaped. Customers use CLI/API for
pagination and mutations. Restoring an obsolete OAuth connection binding fails
until the operator reviews and saves a configuration referencing a current grant.
