# ADR-20261107140000: Agent approval notifications and outcomes

Date: 2026-11-07
Status: Accepted
Amends: ADR-20261104140058 (human approval for risky agent actions)
Related: ADR-749 (notification channels), ADR-224 (account release webhooks),
ADR-076 (webhook dispatcher), ADR-20261030133644 (connected agents)

## Context

ADR-20261104140058 makes an agent key's risky requests wait for a person:
apid answers `428 approval_required` with a review link, a person approves on
the dashboard, and the agent's identical retry goes through once. A request
expires 30 minutes after it was created.

The only way a person learns about a request is the agent passing on the
link. An unattended agent (CI, a scheduled run, a hosted session nobody is
watching) has nobody to pass it to, so its request expires unseen. When a
person denies a request, or it expires, the agent's retry simply opens a new
request and gets another 428, so the agent cannot tell "wait" from "no".

## Decision

### 1. Who is told

The **recipients** of a request are the member who connected the agent (the
key's account). When there is no such member, because the key is an org key
created by an admin, or the member has left the organization, they are the
owners and admins of the organization the key acts in. These are the same
people who may decide it (ADR-20261104140058 §5).

### 2. Email

Each recipient gets a transactional email with the agent, the category, the
method and path, the expiry and the review link. It never contains the
request body.

Email is capped per recipient and key: at most one email every 10 minutes.
A request that arrives inside the window is not lost: the next email lists
every request of that key still pending for the recipient. A looping agent
(at most `MaxPendingAgentApprovals`, 20, pending per key) therefore sends a
person at most one email per 10 minutes. The cap is
`AgentApprovalEmailInterval` in `pkg/api/limits.go`.

Email goes to the recipient's verified account email. Accounts that turned
off product email still get it: like a sign-in code, it is about an action
taken with their credentials.

### 3. Notification channels

An account may route approval notifications to its ADR-749 notification
channels (Slack, PagerDuty, email):

- `gregale mcp approvals notify set --channels <name,...>` routes requests
  of the account's own agent keys.
- `gregale org agent policy set --approval-channels <name,...>` routes
  requests of agent keys acting in an organization. The channels must belong
  to the owner or admin setting the policy; a binding is dropped when its
  channel is deleted.

Channel messages carry the same fields as the email and no body. The email
cap does not apply to channels; channel delivery follows ADR-749 (bounded
retries, last error recorded on the channel).

### 4. Account webhook events

The account webhook (ADR-224) accepts three new event types:
`agent.approval.requested`, `agent.approval.approved` and
`agent.approval.denied`. apid enqueues them in the same transaction that
creates or decides the request, and schedd's webhook dispatcher (ADR-076)
delivers them. The payload names the request id, key id, category, method,
path, status, expiry and review URL, never the body. Expiry is not an event:
a request's expiry is computed when it is read, not written, and the
listing shows it.

### 5. Where delivery runs

Notification channel targets are sealed to the Alerting domain, which only
meterd may open, and meterd already sends alert email and channel messages.
So:

- apid writes one `agent_approval_notifications` row per new request in the
  transaction that creates it. apid is its only inserter.
- meterd claims due rows (lease, `FOR UPDATE SKIP LOCKED`), resolves the
  recipients and bindings at send time, applies the email cap, sends, and
  records the outcome on the row. A request that is no longer pending at send
  time sends nothing.
- Webhooks do not use this row (§4).

### 6. The agent learns the outcome

A person may add a note of up to 280 characters when denying. When an agent
repeats a request whose most recent matching request (same key, method,
path, body) was denied or expired within the last 30 minutes, apid answers
with the outcome instead of opening a new request:

- `403 approval_denied` with `agent_approval: {id, status, note}`;
- `410 approval_expired` with `agent_approval: {id, status}`.

After 30 minutes, or with a different request, the agent gets a fresh 428 as
before. `GET /v1/agent-approvals/{id}` shows the status and note. The MCP
bridge and the CLI print the outcome as the reason the call failed, so the
agent stops retrying.

## Consequences

- Unattended agents become usable with approvals on: someone hears about
  the request while it can still be approved.
- A person whose agent loops gets at most one email per key every 10
  minutes; channels may be noisier by the customer's choice.
- A denied request stays denied for 30 minutes; repeating it does not
  re-ask the person.
- meterd gains a third delivery loop (alerts, budget alerts, approvals),
  sharing its channel sender and mail transport.

## Alternatives considered

- **Deliver from apid.** apid cannot open channel targets (Alerting
  domain); adding apid as a reader would widen who holds those secrets.
- **Periodic digest only.** Requests expire in 30 minutes, so a digest
  would often arrive after the request was dead.
- **Notify every possible approver every time.** More noise; the connector
  is the person who chose to run the agent and usually the right one.
- **Store expiry and emit `agent.approval.expired`.** Needs a writer for
  expiry and a sweep; left for later if customers ask for it.
