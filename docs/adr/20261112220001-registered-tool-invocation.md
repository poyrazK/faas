# ADR-20261112220001: Authorized invocation of exact registered tool versions

Status: Accepted

## Context

ADR-20261112210001 retains private publication versions and runtime schemas.
Discovery alone must not authorize executing a customer tool. A lost reply can
hide completed side effects, and cancelling an HTTP client cannot prove that a
remote handler stopped. New sessions need durable invocation facts without
replaying uncertain calls.

## Decision

Add the opt-in local-account `invoke` management toolset:
`gregale_invoke_tool`, `gregale_get_tool_invocation` and
`gregale_cancel_tool_invocation`. Invocation requires connection writes,
explicit allow_tool_execution, an exact publication version and tool name,
declared access mode, and a caller-retained canonical nonzero UUID. Native
deploy-write authority and MFA policy protect state mutations. Reading runtime
results also requires deploy-write authority; registry read access does not
grant runtime-data access. Hosted, app/project, tenant and organization grants
remain outside this slice. readOnlyHint annotations never grant permission.

APID owns a durable, account-scoped invocation journal. Begin freezes argument
hash, timeout, access declaration and a hash of the optional local token-variable
name. It resolves the exact owned publication to app/deployment/source/catalog
identities. No argument values, token values or local paths enter the intent.
Only a new insertion permits one bounded local CLI worker to start. Duplicate
requests return the old receipt; changed intent under the same UUID conflicts.
There is no automatic recovery worker, claim takeover or latest-version fallback.

The trusted CLI worker reads bounded canonical JSON from stdin. It independently
checks publication/account/deployment identity, resolves the native exact-revision
preview origin, and appends the publication's reviewed endpoint path. No caller
URL is accepted. Publication now records endpoint_path; older records without it
remain discoverable but require republishing before invocation. Default-scope,
live, source-matching deployments with the original catalog-pin override are the
only eligible targets. Retired or inaccessible versions fail without substitution.

Before dispatch, the worker compares the full observed caller-visible canonical
catalog with the archived contract, verifies the deployment's runtime catalog
pin through existing exact-replica diagnostics, and validates argument JSON
against the frozen input schema. Schema compilation cannot fetch external HTTP
or filesystem references. Metadata discovery does not execute tools. Native
dispatch rechecks the deployment binding, then atomically changes queued to
running and returns dispatch_granted=true exactly once. Lost or duplicate grant
responses never authorize a call. No HTTP idempotency receipt caches that flag.

After a fresh dispatch grant the MCP client issues exactly one tools/call.
Transport errors, truncated replies, timeouts, process loss and unsupported
Tasks/input continuations never trigger retries or automatic resumption. External
OAuth uses a named MCP_* environment reference in the trusted local CLI. Native
Gregale credentials and management tokens are refused as application tokens.
Redirects are disabled. Customer code executes on the published app, never on
the management host. This is an external local management client; it does not
replace the proposed in-VM capability gateway or tenant-connection credential
model in ADR-20261027090016 and ADR-20261027090017.

## State and cancellation

| Fact | Meaning |
|---|---|
| queued | Journaled; no dispatch grant has been issued |
| running | Dispatch granted; side effects may have happened |
| completed | Worker received a complete successful MCP result |
| tool_error | Worker received isError=true; effects may still have happened |
| not_started | Preflight failed or an unclaimed request expired |
| cancelled | Native cancellation won before dispatch |
| outcome_unknown | A dispatched attempt has no reliable complete result |

Queued cancellation atomically prevents dispatch. Running cancellation records
the request and the worker polls that fact to stop the local wait. It neither
rolls back effects nor asserts successful remote cancellation. A valid result
received in a cancellation race can still complete the receipt. Otherwise the
result is outcome_unknown. Cancellation after a terminal fact does not overwrite
it. The management transport can disconnect while its worker continues; process
or grant lifecycle shutdown still cancels the worker. Requests abandoned before
dispatch expire as not_started; dispatched requests expire as outcome_unknown.

Terminal evidence is immutable and identical reports are idempotent. Results
are worker-reported application data, not independently attested platform proof
of business success. Only complete content, structuredContent and isError are
projected; transport exchanges, private input state and metadata are discarded.
Known credential reflections and credential-shaped strings are redacted before
retention. Numeric JSON values remain exact. Result bytes use base64 through
JSONB; the digest describes the sanitized bytes. Large results retain only digest
and length and explicitly report omission. Management returns bounded chunks,
not an arbitrarily large inline result or an executable agent instruction.

## Bounds and rollout

Limits live in pkg/api/limits.go: 32 KiB argument JSON, 64 KiB retained result,
128 KiB outcome requests, four active invocations per account, and 1024 retained
identities per account. Identities, including cancelled and uncertain attempts,
remain until account deletion; exhausting the preview bound refuses new work
instead of reopening a replay window. Worker timeouts default to 60 seconds and
are bounded to 600 seconds including preflight, with 30 seconds of journal-report
grace. Native workload admission, runtime billing and API rate limits remain
authoritative. Managed artifact retention, hosted delegation, independent native
execution attestations and interactive/Tasks invocation are later work.

Deploy the additive journal migration before APID, then update the local CLI.
An older backend refuses the flow before dispatch. This slice does not change
existing feature maturity or deploy production infrastructure automatically.

## Validation

Memory and PostgreSQL handlers exercise exact binding, immutable requests and
results, concurrent single-dispatch election, cancellation races and isolation.
Real HTTP MCP fixtures cover caller-visible catalog drift, pin drift, malformed
arguments, native-credential refusal, redirect refusal, tool errors, lost replies
and cancellation after side effects may have started. Both management protocol
versions exercise fresh-session receipt recovery without repeating a call.
Portable tests do not establish a live cloud deployment or native VM acceptance.
