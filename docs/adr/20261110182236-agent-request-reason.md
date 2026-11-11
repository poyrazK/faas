# ADR-20261110182236: An agent says why it makes a request

Date: 2026-11-10
Status: Accepted
Related: ADR-20261104140058 (agent approvals), ADR-20261107140000 (approval
notifications), ADR-20261107220000 (agent activity log)

## Context

A person deciding an agent's approval request sees only the method and path,
for example `DELETE /v1/apps/shop-api`. The agent's activity log shows the
same. Neither says what the agent was trying to do, so the person decides
blind or asks the agent out of band.

## Decision

1. **Header:** an agent sends `Gregale-Agent-Reason: <text>` on a write
   request. The header works on every route without changing any request
   body.
2. **Optional:** a request without a reason behaves as today.
3. **Bounded:** apid trims the text, replaces control characters with
   spaces, collapses runs of whitespace and cuts it at 280 characters
   (`api.MaxAgentReasonChars`). An empty result counts as no reason. The
   header is read only for agent and `mcp-login` keys; other credentials'
   headers are ignored.
4. **Stored** on the approval request (`agent_approval_requests.reason`)
   and on the activity row (`agent_activity.reason`). Both columns are
   nullable and CHECKed to 1–280 characters. An approval keeps the reason of
   the request that opened it; repeating the request does not replace it.
5. **Shown:**
   - on the review page, above the method and path;
   - in approval emails, channel notifications and webhook events;
   - in `GET /v1/agent-approvals[/{id}]` (`reason`);
   - in the activity log (`reason`) and in `gregale mcp connection activity`.
   It is shown as untrusted agent text: quoted and labelled as the agent's
   own words. Gregale never renders it as a link (a mail client may still
   autolink a URL in a plain-text email).
6. **Sent by:**
   - **The CLI:** the prefix `--agent-reason TEXT` (before the command, like
     `--org`) or `GREGALE_AGENT_REASON` adds the header to write requests.
     It is not `--reason`, which several commands already take with their
     own meaning.
   - **The SDK:** `api.WithAgentReason(ctx, text)` sets it for a call; the
     client's transport adds it.
   - **The MCP bridge:** forwards a plan's existing reviewable `reason` when
     it applies the plan, so agents on MCP send one without a new tool
     argument, and the tool catalog does not grow.

The reason is the agent's claim, not evidence. It never changes what is
approved: the approval still binds method, path and body, so a misleading
reason cannot widen what the person allowed.

## Consequences

- People see intent next to every gated request and in the activity log.
- One nullable column on each of two tables. The previous release ignores
  the header and the columns.
