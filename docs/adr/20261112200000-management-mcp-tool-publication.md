# ADR-20261112200000: Agent tool authoring and verified publication over MCP

Status: accepted

## Decision

Add an opt-in local-account `build-tools` catalog with starter retrieval,
explicit behavioral candidate tests and plan/apply/status publication. Reuse
Gregale's MCP CLI and existing isolated source builds, catalog pinning, exact
replica/protocol checks, behavioral smoke suite and guarded traffic promotion.
No new VM owner, scheduler, execution engine or database schema is introduced.

Hosted grants, app-restricted management grants and organization connections
cannot delegate the local files or CLI. They advertise no build tools; handlers
also reject them. Read-only local connections can retrieve templates, plan and
inspect receipts. Candidate tests and apply require explicit connection writes.
Native authentication, MFA, ownership, entitlements and admission remain in force.

Starter retrieval materializes only the trusted CLI template in a temporary
directory. File transfer uses bounded base64 chunks and a content digest so
credential-shaped sample text is preserved without exposing a host file API.
No generated code runs on the management host.

Publication review requires explicit open/external-OAuth access, an explicit
tool allowlist and a validated behavioral fixture suite with tool execution
approved. Credentials use named MCP_* client-token variables, never plaintext
arguments or implicit account-key forwarding. This slice excludes durable Tasks.

Plans pin source contents/modes, fixtures, account, app and serving identities.
Apply refuses changed source or baseline and copies the reviewed files into a
private bounded snapshot before detaching work. Both candidate builds use that
snapshot. Four process-local workers run for at most 30 minutes, detached from
the requesting transport but bound to lifecycle cancellation. Native calls
continue to recheck the connection's credential.

The CLI adds optional reviewed app/serving expectations and a required-catalog-
pin gate. Publication supplies them. Baseline checks precede ingress changes
and tool fixtures; final promotion retains the native atomic serving guard.
Behavioral test calls are never inferred from protocol discovery alone.

Each publication plan starts once. All subsequent applies return its retained
status, including failed or uncertain outcomes. This prevents response loss or
polling from repeating potentially effectful fixtures. Known CLI receipts are
projected and checked against native deployment identity, catalog-pin override
and serving state. Unknown/unbound claims are withheld. Status reads never
rerun the CLI or infer missing verification evidence from serving traffic.

The durable journal and registry extension is specified in
[ADR-20261112210000](20261112210000-durable-tool-publications-and-registry.md).

## Recovery and scope

Unstarted plans expire in 15 minutes. Finished receipts remain for 24 hours,
bounded to 256 per connection. This local state survives request cancellation,
not process restart. Native deployment history is the recovery source after a
restart or missing receipt. Starting another reviewed attempt is a new action;
no automatic fixture retry or exactly-once side-effect claim is made.

The account tool registry and general tool discovery/invocation remain separate
work. Existing management MCP and MCP hosting maturity are not promoted.

## Validation

Both MCP protocol versions exercise template bytes, staging-only tests,
plan/apply/status and repeat-apply behavior. Tests cover private field projection,
failed verification preserving serving traffic, lost responses, source changes
with unchanged size/mtime, native serving changes, authority boundaries and
transport cancellation. CLI baseline tests reject changed app/serving identities
before test execution. Existing MCP release tests continue to cover verification
and atomic promotion. Portable fixtures do not establish deployed/native-host
acceptance.
