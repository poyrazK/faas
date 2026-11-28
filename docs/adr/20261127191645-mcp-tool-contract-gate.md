# ADR-20261127191645: Tool contract gate for sold MCP servers

- Status: Accepted (dark: API monetization routes)
- Date: 2026-11-27
- Related: ADR-20261126175800 (MCP listing), ADR-20261122220000 (verification
  contracts), ADR-121 (OpenAPI contract gate), ADR-20261124094745
  (self-serve consumers)

## Context

A seller lists an MCP server (ADR-20261126175800) and strangers pay for it.
Nothing stops the seller's next deploy from renaming a tool, adding a
required argument or narrowing an input type. Paying customers' agents then
fail on calls that worked yesterday, and nobody is told. `pkg/mcphosting`
already compares catalog contracts conservatively (`CompareContracts`: input
accepts must not narrow, output promises must not widen, unknown keyword
changes need review), but only the opt-in, local-file `mcp lock` / `mcp diff`
/ `mcp release` path uses it, and a git push or plain `gregale deploy`
skips it.

## Decision

Owner decisions (2026-11-27): the baseline is the contract the seller
published with the listing; the check is a server-side traffic gate that
covers every deploy path; a breaking change is blocked until the seller
passes an audited override; customers learn about changes through a new app
webhook event and a public changelog on the listing page.

1. **Baseline.** `mcp server listing publish` already lists the live
   server's catalog as the seller. It now also sends that catalog's
   format-2 contract (`mcphosting.MarshalContract`), which apid validates
   (`ReadContract` + `Validate`, at most `api.MCPListingContractMaxBytes`)
   and stores on the listing with its SHA-256. The listing's tools are
   still the name/title/description snapshot. The *effective baseline* is
   the candidate contract of the check whose deployment most recently went
   live (`live_at`, item 3) after the listing's publish time, else the
   listing's contract, so a rollback restores the contract it serves.
   Republishing resets it. A listing without a contract (published before
   this ADR) is not gated until it is republished. Tool definitions the MCP
   client rejects are left out on both sides (publish warns), so a tool
   that turns invalid reads as removed. Publish refuses a contract for an
   `external-oauth` server without a probe secret, which the gate could
   never check.
2. **Gate.** imaged runs the check in the hosting verification chain after
   the candidate's smoke and verification contract passed, for a
   production-scope deployment of an app whose listing has a contract. It
   opens the candidate-pinned session (`OpenCandidateSession`), initializes
   the listing endpoint's path statelessly, discovers the full catalog and
   compares it with the effective baseline. For a server that checks
   credentials itself (`external-oauth`), the listing names an app secret
   (`probe_secret`) whose value imaged sends as the bearer token, opened
   through secretd under the deployment's verification lease like a
   verification contract's `${secret:NAME}` header.
   - unchanged, compatible or needs-review: the candidate proceeds; the
     check is recorded. Needs-review changes are shown to the seller, they
     do not block.
   - breaking: the deployment fails with `mcp_contract_breaking` (RFC 7807
     detail lists the breaking changes) and the predecessor keeps serving,
     unless the app holds an unused, unexpired override (item 4), which the
     check consumes in the same transaction that records it.
   - the catalog cannot be discovered (session, probe secret, initialize,
     list, or a catalog over the contract cap): unavailable evidence,
     retried through the hosting verification recovery window like other
     unavailable smoke; when the window ends the deployment fails closed.
     An override also admits it, recorded as `unverified`.
   - A refused check is still recorded (no override, never accepted), so
     the owner view shows what was refused. A check stores at most
     `api.MCPContractChangesMax` changes.
3. **Acceptance.** Checks live in `mcp_contract_checks`, one row per
   deployment (imaged writes, apid reads). A trigger on `deployments` sets
   `live_at` each time the checked deployment becomes `live` and
   `accepted_at` the first time, so a candidate that fails later never
   moves the baseline. On the first acceptance it queues the
   `mcp.contract.changed` app webhook delivery when the check has changes;
   a rollback only moves `live_at`. A refused check is never accepted.
   Every accepted change moves the baseline, compatible additions included,
   so a tool added last week is protected next week.
4. **Override.** `POST /v1/apps/{slug}/mcp-contract/overrides` (owner, MFA,
   deploy-write scope) records a reason (1–512 bytes) and an expiry (at
   most `api.MCPContractOverrideMaxTTL`, default 1 h). It admits exactly
   one breaking or unverified candidate. An override may name a
   `deployment_id`; then only that deployment uses it, and a check prefers
   an override bound to its deployment. `mcp server deploy --allow-breaking
   --reason TEXT` binds one to each candidate deployment it queues, so a
   concurrent deploy cannot take it; for a git push the seller runs
   `mcp server contract allow` first, which admits the app's next breaking
   candidate. Creation is serialized per app so the open cap holds.
   Creating one is audited (`mcp_contract.override_created`); its use is
   recorded on the override (`used_by_deployment`, `used_at`) and on the
   check (`override_id`), since imaged writes no apid audit events.
5. **Visibility.** `GET /v1/apps/{slug}/mcp-contract` (owner) returns the
   effective baseline's SHA-256 and tool names, recent checks with their
   changes, and open overrides; `gregale mcp server contract show` renders
   it. The public listing JSON and page gain a changelog: accepted checks
   with changes since the listing was published, newest first, at most 20,
   each with its date, verdict and changed tool names (no schemas, no
   override reasons).

The gate is part of the API monetization surface: it runs only for apps
with a listing, and listings exist only where monetization is released.

## Consequences

- Sellers cannot silently break paying customers; every break is a
  deliberate, audited act that customers can see and subscribe to.
- A sold server whose catalog cannot be listed from inside the platform
  (credential missing, server down at deploy) cannot deploy without an
  override. That is the intended fail-closed default for paid servers.
- Each production deploy of a listed server makes a few MCP requests to the
  candidate. Tool calls are never made.
- Release order: data (migration) → imaged + apid → CLI. Until imaged is
  released no check runs; until the CLI is released listings carry no
  contract and are not gated.
