# ADR-20261117174831: Explicit MCP application-response verification

Status: Accepted

## Context

`gregale release verify` checks an application GET against an exact accepted
deployment through the owner-authorized response-check API. Its evidence pins
the app, deployment, environment lifetime and running instance. It works for
private apps without forwarding account credentials to the workload.

MCP's `gregale_verify_plan` intentionally performs passive readiness checks.
Readiness does not establish that application code answered a request. Hosted
agents need the response check without installing or running a local CLI.

## Decision

Add `gregale_verify_application_response` to the `core` and `release` catalogs
on write-enabled local and hosted connections. It takes an app slug, exact
deployment UUID, app-relative GET path (default `/`), and project/environment
slugs for a named scope. The bridge resolves catalog identities under existing
app and environment write consent, checks installed protocol support, and calls
the same typed `api.Client.CheckDeploymentResponse` operation as the CLI.

This is an active, non-idempotent tool: it can wake an app, execute code and
incur usage. Inspection-only connections do not list it. Native `deploy:write`,
ownership, MFA and scope checks remain authoritative. No new approval or plan
is introduced, and the bridge never promotes or redeploys as part of a check.
App-bound agent keys may read the installation's response-check support metadata;
the existing capability route still requires deploy scope. This exception adds
no app data, account inventory or mutation access.

Return ephemeral, versioned evidence with exact runtime identities, path,
observation time, HTTP status, a credential-redacted body bounded to the native
4 KB limit, and an explicit truncation indicator. Only application 2xx sets
`verified`; redirects and application errors return evidence with
`verified: false`. `public_access_verified` is always false. App authentication
and public ingress are separate concerns, and bodies are untrusted data.

Validate native evidence using the existing request/result contract and recheck
consent and immutable app/environment identities before returning any body.
Never fall back to the app's public URL, another release, or synchronous
invocations. Do not replay a GET after cancellation or uncertain API failure;
structured recovery directs inspection of the same deployment. Passive plan
verification and the plan journal retain their existing semantics.
The shared client omits the generic mutation idempotency header for this
ephemeral endpoint, which has no replay receipt. This also prevents Go's HTTP
transport from replaying a POST after losing its response on a reused connection;
the CLI benefits from the same fix.

## Hosted upload approval

The native approval covers a specific resumable upload commit. Keep the CLI's
checkpoint directory alongside each staged deploy plan so applying the same
plan after approval can commit the original upload. The checkpoint stays
outside the source tree and expires with the staged files. Each CLI invocation
still gets a fresh home and git configuration.

Amendment 2026-11-19. A native approval of a body-bound resumable upload
commit extends the original 15-minute MCP plan to the expiry already shown on
the approval review page. The bridge reads the approval and the complete
upload using the same credential. It pins the app identity and reviewed
release state and retains the existing staging reservation. The approval
deadline remains fixed while the agent polls or retries. Other deployment
approval paths retain their current behavior.

Before applying, the bridge checks the original decision, upload and release
state. The CLI requires the same resumable upload checkpoint on this retry.
A missing or incompatible checkpoint stops the retry. A committed original
upload can recover its exact release after a lost reply.

Amendment 2026-11-22. Before returning the review link, the bridge commits the
reviewed plan, original approval and upload IDs, and staging reservation to its
encrypted journal. At startup it restores unexpired approval-bound source and
CLI checkpoints from the same private upload directory. The original grant
or agent key can retry the plan under current authorization. Plans without a
resumable commit approval and one-time upload URLs expire with the bridge process.

## Validation

A bridge regression test exercises an approval response and a retry of the
same plan through fresh CLI homes with a shared plan checkpoint. It also
checks cleanup and receipt reuse. The restart regression reopens the encrypted
journal and staging directory while the approval is pending, then verifies the
exact response from the one accepted deployment. It also covers a lost commit
reply, the approval deadline, changed source, and credential isolation.

MCP tests cover both supported protocol versions, private-app 2xx and non-2xx
evidence, named environment consent, late revocation and identity replacement,
malformed or unbound results, path bounds, UUID wire forms, credential redaction
and truncation, unsupported installations, cancellation and non-replay. A hosted
HTTP test uses an app-bound agent key without CLI or account-wide inventory.
The dropped-connection regression demonstrated two native attempts with the old
client before the replay hint was removed.
Existing catalog budgets, schema contracts and readiness tests continue to run.
CLI response-check and agent-facing documentation tests cover the shared native
operation and updated deploy skill. These are fixture-based tests, not deployed
microVM acceptance evidence.
