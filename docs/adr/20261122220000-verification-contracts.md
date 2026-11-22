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
- Not in v1: queue round trips, DLQ checks and Realtime echo; contracts for
  deployments created without the CLI (GitHub pushes, environment
  promotions) run the app's stored contract, but nothing uploads a changed
  file for them; public OpenAPI and SDK clients for the new routes.
