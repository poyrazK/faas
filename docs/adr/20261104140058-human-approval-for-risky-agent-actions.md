# ADR-20261104140058: Human approval for risky agent actions

Date: 2026-11-04
Status: Accepted
Amends: ADR-20261029085359 (agent keys), ADR-20261101225122 (org agent policy)
Related: ADR-1242 (browser promotion approval), ADR-20261104085712 (project limits),
ADR-20261030133644 (connected agents)

## Context

An agent connection can do anything its key's scopes allow: delete an app or
a database, deploy to production, roll back, rotate credentials or change a
plan. Customers want an agent to work on its own but ask a person before it
does something that is hard to undo or reaches production traffic.

Human review exists for one action: a protected environment promotion
(ADR-1242). That review lives in the MCP bridge, so an agent that holds its
key and a shell can call the API directly and skip it.

## Decision

### 1. apid gates agent keys, for every client

The gate applies to the keys of agent connections: `agent`-scope keys and the
`mcp-login` keys of hosted sign-ins. It runs in apid after authentication,
so it holds for MCP, the CLI and raw API calls alike. Other keys, sessions
and deploy tokens are not gated.

### 2. Four categories

| Category | Requests |
|---|---|
| `delete` | Every `DELETE` route, and the `POST` routes that destroy data, except a short list of non-destructive ones (an agent disconnecting itself, aborting its own upload). |
| `production` | Deploying, rolling back, restarting, recovering a rollout or changing traffic of a production app (one that is not a preview or developer environment), by app or by deployment. |
| `credentials` | Minting or rotating API keys and deploy tokens, rotating secrets and webhook signing secrets. |
| `billing` | Plan, subscription and payment-method changes, for the account or an organization. |

Protected project environments keep their own review (ADR-1242), so a
promotion is not gated twice.

A table in `pkg/agentapproval` classifies routes by their `ServeMux` pattern.
A coverage test checks every mutating operation of the OpenAPI spec: each
`DELETE` is gated unless it is explicitly exempt, and every listed pattern
exists. A new `DELETE` route is therefore gated by default.

### 3. Which categories apply

- A connection carries its own set, chosen when its key is minted
  (`approval_categories` on key create, `--approvals` in the CLI). Without a
  choice it gets all four. An empty set means the connection may act alone.
- An organization's agent policy carries a floor (`approval_categories`):
  categories that always need approval for agents acting in it. A connection
  cannot loosen the floor.
- The effective set is the connection's set plus the floor of its
  organization. Existing agent keys have no stored set, so they get all four.

Storage: `api_key_approval_categories (key_id PK → api_keys ON DELETE
CASCADE, categories text[])`; `org_agent_policies.approval_categories text[]`.

### 4. The request flow

1. A gated request with no matching approval creates (or reuses) a pending
   approval request and answers `428 approval_required`. The problem carries
   `agent_approval: {id, category, review_url, expires_at}` and says to ask
   a person to open the link, then repeat the same request.
2. A person opens the review link, which renders on the session-authenticated
   `/cli-auth` surface like ADR-1242's promotion review. It shows the agent
   (client name or key label), the member who connected it, the category, the
   method and path, and whether the request body is bound. It never shows the
   body. Approve and Deny are CSRF-protected form posts.
3. The agent repeats the request. If an approved, unused, unexpired request
   for the same key, method, path and body exists, apid marks it used in the
   same statement that matches it and lets the request through. One approval
   allows exactly one request.

The binding is the key, the method, the path with its query, and the SHA-256
of the body. Bodies above 1 MiB and multipart uploads are bound without their
content; the review page says so. The approval then allows one such request
to that path. A request expires 30 minutes after it was created, approved or
not.

`GET /v1/agent-approvals/{id}` lets the agent poll its own request.
`GET /v1/agent-approvals` lists the pending requests a person may decide.

### 5. Who approves

Only a person, through a signed-in dashboard session. The decision routes
accept no API key, so an agent cannot approve its own request with any
credential. The approver is the member who connected the agent (the key's
account) or an owner or admin of the organization the key acts in. Sign-in,
MFA and session revocation apply as for every dashboard page. Each decision
is written to the audit log.

### 6. Clients

- The CLI prints the review link from the problem, and waits and retries with
  `--wait-for-approval`.
- The MCP bridge returns the review link to the agent as a tool error with an
  instruction to retry the same call after approval. Amended 2026-11-09: the
  error carries the request in `approval` with an `await_approval` recovery
  naming `gregale_wait_for_approval`, which waits for the decision and answers
  `retry_original` or `stop`; `gregale_agent_activity` lists the agent's own
  write requests (ADR-20261107220000).
- `gregale key create --approvals`, `gregale org agent policy set --approvals`
  and the agent connection list show and set the categories.

## Consequences

- Existing agent connections start asking for approval before deletes,
  production deploys, credential changes and billing changes. Customers who
  want the old behavior mint a key with `--approvals none` (still subject to
  their organization's floor).
- A production deploy through an upload binds the path but not the archive:
  the approval allows one deploy of that app within 30 minutes, not a
  particular archive.
- Every gated request costs one indexed read. A pending request costs one
  insert.

## Alternatives considered

- **Gate in the MCP bridge only.** Smaller, but an agent with its key and a
  shell bypasses it.
- **An approval token header.** The agent would have to carry a token from
  the approval to the retry; every client would need to learn it. Matching
  the repeated request needs no client change.
- **Approval by API key.** Any key an agent can read could approve its own
  request.

## Amendment (2026-11-10): project routes are production writes

The production list named only app routes, so an agent key with
`deploy:write` could deploy a whole project, promote an environment,
activate, deactivate or roll back a release set, or create, change, rebind
or adopt an environment's GitOps source without a person (deploy review
2026-11-10, D3). These routes, including their `/v1/orgs/{organization}`
forms, are now production writes in `pkg/agentapproval`: `projects/apply-plan`,
`projects/safe-deploy`, the organization project deploy, `environments/{}/promote`,
promotion actions (abort, recover), promotion rollback and migration
recovery, `release-sets` (publish, activate, deactivate, rollback) and
`gitops/source`, `source/rebind`, `adopt`, `overrides` and
`revisions/approve`. Plans, previews and verification runs stay ungated.

A project environment records no production flag, so these ask on every
environment (`TargetNone`), staging included; a protected environment keeps
its own approval token on top. The promotion action route is registered as
`promotions/{promotion}/{action}` while the spec lists `/abort` and
`/recover`, so it sits in a separate route-only table that the spec coverage
test checks does not overlap the spec. Validation: `TestClassify`,
`TestClassificationCoversTheSpec`, `TestHandlers_AgentProjectWritesNeedApproval`.
