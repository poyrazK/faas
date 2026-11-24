# ADR-20261122220000: Verification contracts (gregale.verify.yaml)

- Status: Proposed (dark behind the `verification-contracts` capability,
  preview, off by default; the owner decides which accounts to release it to)
- Date: 2026-11-22
- Related: ADR-093 (deployment smoke), ADR-847 (route probes),
  ADR-20261117215858 (agent read-only queries), ADR-20261026145832 (CLI tree)

## Context

Before a deployment takes traffic, imaged proves it through the gateway's
deployment-pinned smoke: one health request, plus up to ten static GET routes
an imported OpenAPI document marks with `x-gregale-hosting-check`. MCP
servers can be exercised with `gregale-mcp.tests.json` fixtures, but only
from the CLI and never as a gate. Everything else an app must do (an API
answers with the right shape, an MCP tool returns the expected result, a
migration left the data consistent) is checked, if at all, by throwaway
scripts a coding agent writes for each deploy, after the deploy is already
live.

## Decision

1. **One declarative file.** `gregale.verify.yaml`, a separate file at the
   project root (owner decision 2026-11-22, over a `verify:` block in
   gregale.yaml), holds up to 32 named checks with a total budget (default
   30 s, at most 120 s). v1 check kinds (owner decision: HTTP, MCP and
   read-only SQL):
   - `http`: method, path, headers, body or JSON body; assertions on status,
     latency, response headers, body substrings and JSON pointers.
   - `mcp`: one tool call, resource read or prompt against the candidate's
     MCP endpoint, with the existing fixture semantics.
   - `sql`: one statement against a managed Postgres database of the app's
     account, asserting the row count, the first value or NULL.
   `pkg/verifycontract` owns the schema, validation, assertions and the
   report; the CLI, apid and imaged share it.
2. **Stored per app.** `PUT /v1/apps/{slug}/verification-contract` stores
   the validated JSON form with a revision (optimistic `expected_revision`),
   MFA and deploy scope; `gregale deploy` uploads the file before it creates
   the deployment. `gregale release check validate|set|get|delete` manage it.
   The contract is app configuration, not versioned with each deployment: a
   deployment runs the contract stored when it is verified. Its SHA-256 is
   in the report.
3. **Runs before promotion and blocks it.** After the candidate's own health
   and OpenAPI route checks pass, imaged opens one candidate session
   (`apihostingreceipt.CandidateSession`): one platform challenge for the
   whole run, a routing probe that waits until the gateway proves it routes
   the challenge to the candidate, and a transport that rejects any response
   without the candidate's proof. Checks run one at a time. A required check
   that fails or cannot produce a verdict fails the deployment with
   `deployment_verification_contract_failed` (owner decision: block); the
   live pointer never moves. `advisory: true` checks are reported only. A
   contract that cannot be read or a session that cannot be opened is
   unavailable evidence and retries through the existing hosting
   verification recovery window.
4. **No state changes in production** (owner decision). A production-scope
   deployment of an app that is not a PR preview skips HTTP checks with
   methods other than GET, HEAD and OPTIONS, and MCP tools whose annotations
   do not declare `readOnlyHint: true`; the report lists them as skipped.
   Environment branches, previews and other scopes run them. SQL checks are
   always read-only.
5. **A smoke token the gateway does not know is refused.** Before this, a
   request carrying a smoke token whose challenge had not reached the
   gateway was served by the live deployment. That was harmless for a GET
   health probe the verifier retries, but would send a contract's POST to
   the predecessor. The gateway now answers it 503 like an unknown route
   probe; the verifier already reads that as gateway-unavailable and
   retries.
6. **SQL runs in apid.** Only apid holds managed Postgres credentials.
   imaged queues each SQL check in `deployment_verification_queries`; an
   apid worker runs it with `managedpostgres.Service.QueryReadOnly` (the
   agent query login, READ ONLY transaction, statement and lock timeouts,
   capped rows), audits it by SQL hash like the agent query API, and writes
   back only the row count and the first value (at most 1 KiB). Rows are
   deleted an hour after they expire.
7. **One report.** The report (status, environment, contract SHA-256 and,
   per check, status, stable code, HTTP status and failure messages) is
   stored on the deployment's hosting receipt as
   `smoke.verification_contract` and printed by `gregale deployments get`.
   Failure messages quote status codes, numbers and booleans, never response
   text or row values.

## Consequences

- Coding agents declare checks once instead of writing a harness per deploy,
  and a failed check leaves the previous deployment serving.
- Verification adds the contract's run time to every deploy of an app that
  has one; the budget is capped at 120 s.
- The contract lives in plaintext on the app. Header values are not secret
  references in v1; the docs say not to put credentials in the file.
  Secret references are a follow-up.
- Not in v1: queue round trips, DLQ checks and Realtime echo; public OpenAPI
  and SDK clients for the new routes. Environment promotions and branch
  previews run the app's stored contract.

## Amendment 1 (2026-11-23): GitHub pushes apply the contract

A GitHub push now applies the app's `gregale.verify.yaml` like
`gregale deploy`: githubd reads it from the app's own source root (never the
repository root for an app in a subdirectory, so one app's checks never land
on another) and stores it, when its SHA-256 differs from the stored one,
before it enqueues the build. A push can already deploy any code, so letting
it set its own checks grants nothing a deploy did not. An invalid file fails
the app's GitHub check and the app is not deployed; a store failure is
retried with the delivery. A missing file keeps the stored contract. PR
preview apps (`pr-N-*`) apply the PR's file to themselves. Branch previews
share the parent app's ID, so they keep the parent's stored contract: an
unmerged pull request never changes production's checks.

## Amendment 2 (2026-11-24): secret references in check headers

Checks of apps behind authentication need a credential, and the contract is
stored as plain configuration. An HTTP check's header value may now reference
one of the app's own secrets as `${secret:NAME}` (owner decision: the app's
env secrets, not a second secret store and not a platform-signed token). The
reference is substituted only in HTTP header values; anywhere else it is a
validation error. A contract references at most
`api.VerificationContractMaxSecretRefs` (8) distinct names.

Only secretd opens app secrets, and it trusts a node only for apps it owns,
holds a grant for, or builds. Activation runs in whichever imaged takes the
deployment's notification, so before opening anything imaged writes a
`deployment_verification_leases` row naming itself, valid for the contract's
budget plus `api.VerificationLeaseGrace`, and deletes it when the run ends.
The new secretd RPC `OpenVerificationSecrets` opens a key only when:

- the caller is the active node named on the deployment's unexpired lease;
- the deployment is still a candidate (pending, building, imaging or
  snapshotting), so a live or finished deployment opens nothing;
- the app's stored contract references the key;
- the secret is read from `app_secrets` in the deployment's own scope, its
  envelope bound to the app's resource account.

Every open is recorded in `secret_open_audit` (via `grant`, like a build's)
and counts against the node's distinct-app budget. A key the scope does not
hold comes back as missing, and the check fails with `verify_secret_missing`,
naming the secret: that is the customer's configuration, so it blocks like a
failed assertion. secretd being unreachable, or a node without a secretd
client, is unavailable evidence and is retried through the hosting
verification recovery window.

The value goes only to the candidate, which already holds it as env, through
the candidate-pinned session; reports, receipts and logs carry the name,
never the value. Like the lease rows the registry path trusts
(`deployment_image_preparations`), the lease is written by compute; a
compromised node could name itself on a candidate of another app, but could
open only secrets that app's contract references, and every such open is
audited and budgeted.

Rollout: the migration and secretd ship before imaged. An imaged that calls
a secretd without the RPC gets Unimplemented, which is unavailable evidence,
so a deployment whose contract uses references waits rather than promoting
unverified.
