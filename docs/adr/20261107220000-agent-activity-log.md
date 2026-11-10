# ADR-20261107220000: Agent activity log

Date: 2026-11-07
Status: Accepted
Related: ADR-20261104140058 (human approval for risky agent actions),
ADR-20261107140000 (agent approval notifications), ADR-20261030133644
(connected agents), ADR-20261104085712 (project-limited agent keys)

## Context

A person who connects an agent to Gregale can see the connection, limit it to
projects and make its risky requests wait for approval. They cannot see what
the agent actually did. The audit log records some events, but it records the
daemon as the actor and not the key, and most writes are not audited at all.
After an agent session, "what did it change?" has no answer short of reading
every resource's history.

## Decision

### 1. What is recorded

apid records every **write request** (POST, PUT, PATCH, DELETE) made with an
agent or mcp-login key (the keys `ProjectLimitable` covers), after the handler
answers:

- time, key, account, organization the key acts in;
- method and URL path (no query string, no body), capped at 512 bytes;
- response status;
- the approval request used, when the request went through on an approval.

Reads are not recorded: they are most of an agent's traffic, change nothing,
and the person's question is what changed. A request the authentication or
approval gate answers itself (401, 403, 428 …) is not recorded either; the
approval request list already shows the gated ones.

Recording is best effort, off the request path: a detached insert with a short
timeout, like `TouchKeyLastUsed`. A failed insert is logged and the request is
unaffected.

### 2. Retention

Rows are kept **7 days** (`AgentActivityRetention` in `pkg/api/limits.go`).
apid deletes older rows in a periodic sweep; reads also filter to the window,
so a late sweep never shows older rows.

### 3. Who can read it

- The member who connected the agent (the key's account):
  `GET /v1/agent-connections/{id}/activity`.
- An owner or admin of the organization the key acts in:
  `GET /v1/orgs/{slug}/agent-connections/{id}/activity`.
- The agent itself, with its own key:
  `GET /v1/agent-connections/self/activity`.

These are the people who can decide its approval requests (ADR-20261104140058
§5), plus the agent, which can check what it already did before repeating
work. Results are newest first, paged by a `before` cursor, at most
`MaxAgentActivityPage` per page.

### 4. Clients

`gregale mcp connection activity <id>` (and `--self`) lists the rows; the
org variant takes `--org`.

## Consequences

- apid writes one row per agent write. Agent write rates are bounded by the
  existing per-key rate limits; 7 days of rows stays small.
- The log answers "what did this agent change" without auditing every
  handler. It does not record request bodies, so it says which resource was
  touched, not to what value.
- The table is new (expand-only migration); release N-1 apid simply does not
  write it.
