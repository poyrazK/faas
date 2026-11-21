# ADR-20261027090016 · The agent capability gateway: one policy, budget and audit for models, tools, web and data

- **Status:** proposed (owner decision D19, 2026-10-19). §6 amended the same day: where audit records and counters are stored. Amendment 1 (2026-11-19): what is already built, and the first slice. Amendment 2 (2026-11-19): run approvals without a freeze state.
- **Date:** 2026-10-19
- **Amends (when accepted):**
  - ADR-20261027090010 §7: model calls join one per-run capability policy and budget;
  - ADR-20261027090014 §4: tier 1 web tools become MCP tools behind this gateway.
- **Builds on:**
  - ADR-989 (guest relay), ADR-249 and ADR-253 (platform-held and customer-sealed credentials), ADR-250 (workload identity);
  - ADR-988, ADR-990 and ADR-1120 (AI gateway and budgets);
  - ADR-1237–1242 (management MCP), ADR-1244–1246 (MCP catalog);
  - ADR-226 (platform tenants), ADR-20261027090012 (pausing while waiting);
  - ADR-143 and `pkg/logarchive` (the log pipeline and archive to object storage);
  - ADR-1520 (rate-limit token leasing) and ADR-1521 (batched journal writes).
- **Related:**
  - ADR-20261027090017 (tenant connections) and ADR-20261027090018 (web capabilities), which plug into this gateway;
  - ADR-20261027090013 (sessions).
- **Decision:**
  1. **outboundd serves an agent capability mode** behind the existing relay. It is not a new daemon. Inside every agent run and sandbox session VM, one loopback origin (`GREGALE_OUTBOUND_URL`, `127.0.0.1:2774`) reaches:
     - model providers, through the AI gateway, unchanged;
     - an **MCP endpoint** (`/mcp`, Streamable HTTP) that lists every tool the run may use.
  2. **One capability policy per agent version, narrowed per tenant,** says which tools, web destinations, data sources and models a run may use, which need approval, and the budgets.
  3. **One per-run budget and one audit trail** cover model spend, tool calls, web fetches, data reads and bytes out.
  4. **Taint.** Once a run has read untrusted content, tools marked sensitive need approval or are refused, as the policy says.
  5. **Approval is a tool.** A call that needs approval freezes the run, unbilled, until the seller decides.
  6. **Credentials never enter the VM,** for any capability.
- **Why:**
  - **Capabilities are spread across five places today:** the relay, outboundd integrations, the AI gateway, the MCP gateway and the egress tiers. Each has its own rules. Sellers need one place to say what an agent may do, and one bill and one log of what it did.
  - **MCP is the tool protocol harnesses already speak.** Claude Code, Codex, OpenCode and the agent SDKs all consume MCP. One endpoint gives every harness every capability without per-harness glue.
  - **Prompt injection is the main agent risk.** A boundary outside the VM that knows what the run has read, and what each tool can do, is the only place to enforce limits that a compromised agent cannot switch off.
  - **outboundd already holds what this needs:** credential custody, workload identity, fixed-origin dialing, admission, budgets, retries and circuit breakers. ADR-988 rejected a separate daemon for the same reasons.
- **Consequences:**
  - outboundd gains an MCP server, a tool registry and a policy evaluator. It also gains in-memory counters with leases, and an audit spool that the log archive ships to object storage (§6).
  - vmmd's relay assertion gains run, agent version and tenant claims, minted from placement data and never from the guest.
  - apid gains capability-policy intents, and agentd gains approval handling.
  - Every tool call pays one extra hop inside the region (target: under 20 ms at p50).
- **Rejected alternatives:**
  - **A separate capability daemon:** it duplicates credential custody and identity (ADR-988).
  - **Tools running inside the VM with credentials:** the agent could read the credentials, and injected content could use them freely.
  - **Per-harness tool adapters:** they don't scale past the presets, and seller harnesses would get nothing.
  - **Precise information-flow tracking:** not practical across opaque harnesses. Run-level taint is coarse but enforceable, and is described here as a guardrail, not a guarantee.

## Context

On 2026-10-19 the owner adopted a capability roadmap (D19). It starts with this gateway, then tenant connections (ADR-20261027090017) and web capabilities (ADR-20261027090018). Contexts, approvals with sub-agents, version operations, and memory with event triggers follow. Each later item plugs into this gateway, so it comes first.

## Decision

### 1. One endpoint inside the VM

- **Endpoint.** guest-init's relay already exposes `127.0.0.1:2774` to app instances (ADR-989). Agent runs and sessions get it too: ADR-20261027090010 rollout step 7 closes the gap that job VMs have no relay.
- **Configuration.** Harness presets are configured with the model base URL (unchanged) and an MCP server entry pointing at `${GREGALE_OUTBOUND_URL}/mcp`. Seller harnesses read the same variables.
- **The MCP endpoint lists only the tools the run's policy allows.** It serves them from four sources:

| Source | Examples | Executed by |
|---|---|---|
| Built-in tools | `web.fetch`, `web.search` (ADR-20261027090018); `artifacts.put`/`get`; `approval.request`; later `agents.spawn` and `memory.*` | outboundd, or the owning daemon behind it |
| Gregale platform tools | Read deploys, logs and errors; clone an environment (ADR-590); open a PR | outboundd calls the management MCP (ADR-1237–1242) with a run-scoped, read-only-by-default identity |
| Customer MCP servers | A seller's own tools, from the MCP catalog (ADR-1244–1246) | outboundd calls them with customer-sealed credentials (ADR-253) |
| Tenant connections | GitHub, Slack, Google Drive and others, with the tenant's own token | outboundd, through ADR-20261027090017 |

- **Execution happens outside the VM.** Every tool call goes from outboundd through the agent egress gateway (ADR-20261027090014 §4.4) when it leaves Gregale.

### 2. Identity

- vmmd's relay preamble assertion (ADR-989, audience `gregale:outbound:relay`) gains `run_id` or `session_id`, `agent_version_id` and `platform_tenant_id` claims.
- vmmd takes them from its placement record, written by schedd and agentd. Nothing the guest sends can set or change them.
- outboundd verifies the assertion as today, and evaluates the policy for that run.

### 3. The capability policy

- **Owner.** apid owns `agent_capability_policies` (customer intent): one per agent version, with optional per-tenant narrowing. A tenant policy can only remove permissions.
- **Contents:**

| Field | Meaning |
|---|---|
| `models` | Allowed AI-gateway integrations and models (ADR-20261027090010 §7) |
| `tools` | Allowed tools by name or pattern, each with a mode: `allow`, `approve` or `deny` |
| `sensitive` | Tools that write, deploy, send, pay or spawn. They follow `taint_mode` once the run is tainted |
| `web` | Web tier and destination rules (ADR-20261027090014 §4, ADR-20261027090018) |
| `data` | Allowed connections (ADR-20261027090017), platform sources, customer MCP servers |
| `budgets` | Per run: model spend, tool calls, web fetches, data bytes read, bytes out. Per tenant per day: the same. These join the AI gateway's reservations (ADR-990) |
| `taint_mode` | `approve` (default) or `deny`: what happens to sensitive tools after taint |

- **Defaults.** A policy that names nothing allows nothing but the model. Presets ship with a reviewed default: read-only web, no data, no sensitive tools.

### 4. Taint

- A run becomes **tainted** once a tool returns content from an untrusted origin:
  - any web result;
  - a connection marked untrusted (for example email or issue trackers);
  - a customer MCP server not marked trusted;
  - an inbound webhook payload.
- The run's input can be marked untrusted at create. A PR body from a public repository always is.
- **After taint,** every sensitive tool follows `taint_mode`. Non-sensitive tools keep working, so a tainted agent can still read and think, but cannot act without approval.
- **This is a guardrail, not information-flow security.** It is documented as such. It bounds the damage of an injected agent; it does not prove absence.

### 5. Approval

- **Request.** `approval.request`, or any tool in `approve` mode, returns a pending result, and outboundd records an approval request.
- **Notification.** agentd notifies the seller by webhook (and later Slack). The request carries the tool, an argument summary, the reason (policy or taint) and the run.
- **Freezing.** While it waits, the run is frozen (ADR-1151 frozen state, through agentd and schedd) and the time is not billed (D7, D15).
- **Decision.** `POST /v1/agent-runs/{id}/approvals/{approval_id} {decision}` thaws the run, and the tool proceeds or fails. With no decision within `AgentApprovalTimeoutSec`, the tool fails.

### 6. Budgets, counters and audit

**Volume.** At about 20 capability calls per run, one 4-node agent cell (about 12 million runs a month) makes roughly 8 million call records a day. That is too many for control-plane Postgres rows. Postgres keeps what must be transactional; per-call detail goes to object storage through the log pipeline; counters live in memory.

**Budgets and counters**
- **Reserve before executing.** Every capability call reserves against the run's and the tenant's budgets before it executes. An exhausted budget returns a structured error to the harness. The run itself continues unless the policy says `fail_on_budget`.
- **Per-run counters live in memory** on the outboundd replica the run is pinned to: tool calls, fetches, bytes read and out. vmmd pins a run's relay connections to one replica by hashing the run ID. After a failover, the new replica rebuilds the counters from the per-run summary (below) and continues.
- **Shared counters use leases.** Per-tenant daily budgets, per-account limits, and the egress gateway's per-domain politeness caps are shared across replicas. They use ADR-1520's token leasing:
  - a replica takes a lease of tokens from the shared Postgres counter and spends it locally;
  - it renews before the lease runs out;
  - Postgres stays the authority, and sees one write per lease instead of one per call.
- **Model spend** keeps the AI gateway's reservations (ADR-990).
- **Fail closed.** Tokens a crashed replica held count as spent and are never refunded, so a crash cannot let a run exceed a budget.

**Audit**
- **Per-call records** (fields below) are appended by outboundd to a local JSONL spool, partitioned by account and day and fsynced per batch. The log archive (ADR-143, `pkg/logarchive`) ships the spool to object storage on its cadence (5 minutes by default), under `audit/{account}/{yyyy}/{mm}/{dd}/`. Retention is per plan (`AgentAuditRetentionDays`), then the records are purged. Each record holds:
  - run, tenant, version;
  - capability and tool;
  - an argument digest;
  - the decision, with its reason (allowed, approved, denied, budget, taint);
  - bytes in and out, latency, cost.
- **Debug capture.** Full arguments and results are captured only with the seller's debug capture switched on. They go to a separate prefix, kept at most 7 days.
- **Postgres keeps two kinds of rows,** written in batches as ADR-1521 does (one statement per batch of up to 128 records or 100 ms):
  - **per-run summaries** (`agent_run_capability_summaries`): counts by source and decision, bytes, cost, first and last call. These feed tenant statements and the counter rebuild after a failover;
  - **decision rows** for the low-volume events a seller or operator must see at once: approval requests and decisions, denials by policy or taint, budget exhaustion.
- **Reads.**
  - The run timeline (ADR-20261027090010) shows summaries and decision rows from Postgres at once.
  - A live tail of the last calls comes from outboundd's per-run ring buffer (`AgentAuditLiveTailCalls`) through apid while the run is active.
  - Full per-call history comes from the archive once shipped.
  - The seller API reads the same three.
- **Loss bounds.** A lost control-plane disk can lose at most the unshipped spool window of per-call detail. It never loses summaries or decision rows, which are in Postgres.

### 7. Performance and scale

- **Hop overhead:** under 20 ms at p50 inside the region. The tool list is cached per policy revision, and responses stream with no buffering, as ADR-988 does for models.
- **Concurrency:** the per-instance relay limit of 64 concurrent streams (ADR-989) stays; agent presets need far fewer.
- **Scale:** outboundd replicas scale out behind the relay targets.
  - Policy evaluation is in memory, with revisions published on `pg_notify`.
  - Counters are in memory too, with leases for shared ones (§6).
  - The hot path of a call writes nothing to Postgres.

### 8. Security

- **No credential of any kind enters a VM.** Tokens, provider keys and MCP server credentials stay in outboundd (ADR-249, ADR-253, ADR-20261027090017).
- **Server-side identity.** The gateway derives the run's identity from vmmd's assertion. A guest cannot claim another run, tenant or version.
- **Fixed origins.** Every outbound tool call dials a fixed origin through the agent egress gateway. `CONNECT` and redirects are refused, as in ADR-249.
- **Guest root (ADR-20261027090013 §10) changes nothing here,** because enforcement is outside the guest.

### 9. Pricing

The gateway is a software feature and is free (the compute-only principle). Third-party costs reach the seller through their own keys: models, web-data providers, MCP servers. Stateful features built on it get plan limits in their own ADRs.

## Limits (`pkg/api/limits.go`, proposed)

| Name | Value |
|---|---|
| `AgentToolCallsPerRunMax` | 1,000 / 5,000 / 20,000 (Hobby / Pro / Scale) |
| `AgentApprovalTimeoutSec` | 86,400 |
| `AgentAuditDebugRetentionDays` | 7 |
| `AgentAuditRetentionDays` | 30 / 90 / 365 (Hobby / Pro / Scale) |
| `AgentAuditLiveTailCalls` | 1,000 per run |
| `AgentCapabilityHopTargetMs` | 20 (an SLO, not a limit) |

## Metrics (§12 additions)

- `outbound_capability_calls_total{source,decision}`
- `outbound_capability_call_seconds{source}`
- `agent_run_tainted_total`
- `agent_approvals_total{outcome}`
- `agent_budget_exhausted_total{kind}`
- `outbound_audit_spool_bytes`
- `outbound_audit_ship_lag_seconds`
- `outbound_counter_leases_total{scope,outcome}`

## Rollout

All steps stay on local branches until the owner asks for PRs, behind the internal `agents` capability.

1. This ADR.
2. Relay claims for runs and sessions (vmmd), with a property test: guest bytes can never change the claims.
3. apid capability-policy intents, and the default preset policies.
4. The outboundd MCP endpoint, with built-in `web.fetch` and `web.search` (ADR-20261027090018) and artifacts.
5. Budgets and audit:
   - in-memory per-run counters, with run pinning in vmmd;
   - ADR-1520 leases for tenant, account and politeness counters;
   - the audit spool shipped by the log archive;
   - batched per-run summaries and decision rows (ADR-1521);
   - the live tail.

   A property test: no interleaving of replica crashes lets a run or tenant exceed a budget.
6. Taint and approval, through agentd freeze and thaw. Acceptance: an injected instruction in a fetched page cannot open a PR without approval.
7. Management MCP tools and customer MCP servers.
8. Connections (ADR-20261027090017).

## Open questions

1. Whether customer MCP servers may be marked trusted, and by whom (proposed: the seller, per server, recorded in the audit).
2. Slack as an approval channel at launch, or webhooks only.
3. Whether `agents.spawn` (sub-agents) lives here or in ADR-20261027090010's workflow step.

## Amendment 1 (2026-11-19) · What is built, and the first slice

The owner chose a foundation slice (2026-11-19): policies, the MCP endpoint
for agent runs, a per-run tool-call budget with decision rows, and `web_fetch`
as the first tool behind a switch. Taint, approval, the audit spool, sessions,
tenant narrowing and the other tool sources come later, in the rollout order
above.

### What already exists

- **Step 2 is built for agent runs** (ADR-20261027090010 §7). vmmd mints the
  relay assertion from agentd's placement data (`AgentRunClaims`:
  `account_id`, `run_id`, `lease_token`, `platform_tenant_id`, `agent_id`,
  `agent_version`), and outboundd verifies it and checks the run's live lease
  on every request (`PostgresAgentRuns.AuthorizeAgentRun`).
  - A version is keyed by `(agent_id, version)`, so the claim this ADR calls
    `agent_version_id` is that pair.
  - Sandbox sessions start the guest relay but have no relay identity yet.
    They get one in a later step.
- **Model spend** already has a per-run cap and ledger in outboundd
  (`agent_run_ai_spend`, `agent_run_ai_reservations`), which §6 keeps.
- **Stateless agents' platform tools** (`gregale_sleep`, `gregale_run`,
  `gregale_github_subscribe`, `gregale_browse`, `gregale_browser`) are a RAP
  toolset in apid (ADR-20261030125501 §4 amendment). They serve function
  agents, whose runtimes speak RAP. This gateway serves VM agents over MCP.
  Neither replaces the other in this slice.

### The slice

1. **The policy lives on the version.** `agent_versions.capability_policy`
   (jsonb, written by apid at version create, immutable like the rest of the
   version). A version without one allows nothing but its models, as today.
   For this slice it holds:
   - `tools`: a list of `{name, mode}`, where `mode` is `allow` or `deny`.
     `approve` is refused until approval ships (step 6).
   - `budgets.tool_calls`: calls per run, at most `AgentToolCallsPerRunMax`
     for the plan. It defaults to that maximum.

   `sensitive`, `taint_mode`, `web`, `data` and per-tenant narrowing are
   refused at create until their steps ship. Refusing them, rather than
   ignoring them, keeps a seller from believing a rule is enforced when it is
   not.
2. **The endpoint.** outboundd serves `/mcp` (MCP Streamable HTTP, stateless)
   on the relay, only to agent-run principals; any other caller gets 404.
   - Every request re-runs the agent-run authorization (live lease, not
     cancelled), so a finished run loses its tools at once.
   - `tools/list` returns the registry's tools that the version's policy
     allows and the operator switched on. The policy is read in the same
     query as the lease check, so it costs no extra round trip.
   - agentd gives the harness `GREGALE_CAPABILITY_MCP_URL`
     (`${GREGALE_OUTBOUND_URL}/mcp`) when the version's policy allows a tool.
     Preset configuration files that point each harness at it come with the
     preset work.
3. **Budget and decisions.**
   - **The tool-call count is in Postgres for now,** one conditional update
     per call on `agent_run_capability_summaries` (counts by tool and
     decision). This deviates from §6, which keeps per-run counters in
     memory on a pinned replica. vmmd does not pin runs to an outboundd
     replica yet, and a count that two replicas can each under-read would not
     fail closed. One write per tool call is small next to the model calls a
     run makes. In-memory counters with pinning replace it in step 5, before
     any volume test.
   - **Decision rows** (`agent_run_capability_decisions`): a call refused by
     policy, by an exhausted budget, or because the tool is off. These are
     the low-volume rows §6 keeps in Postgres. An allowed call writes only
     the summary.
   - outboundd is the only writer of both tables. apid reads them for the
     run timeline in a later PR.
4. **`web_fetch`, the first tool** (ADR-20261027090018 tier 1): GET one URL
   and return its text.
   - It dials only public IPv4 on ports 80 and 443, checking every address
     at connect time, so DNS cannot rebind it to a private one. It follows at
     most `AgentWebFetchRedirectsMax` redirects, re-checking each.
   - It reads at most `AgentWebFetchMaxBytes`, and returns text types only.
   - It is behind `FAAS_OUTBOUND_AGENT_WEB_FETCH` (off). §1 sends tool
     traffic through the agent egress gateway, and the control plane has no
     path into it yet. The switch stays off in production until outboundd's
     fetches leave from an agent address. That routing is its own change,
     after the gateway host exists (docs/runbooks/agent-egress-production-rollout.md).
     With the switch off, the tool is not listed.

### Limits added in this slice

| Name | Value |
|---|---|
| `AgentToolCallsPerRunMax` | 0 / 1,000 / 5,000 / 20,000 (Free / Hobby / Pro / Scale) |
| `AgentCapabilityPolicyToolsMax` | 64 |
| `AgentWebFetchMaxBytes` | 1 MiB |
| `AgentWebFetchTimeoutSec` | 20 |
| `AgentWebFetchRedirectsMax` | 5 |

### Metrics added in this slice

- `outbound_capability_calls_total{source,decision}`
- `outbound_capability_call_seconds{source}`
- `agent_budget_exhausted_total{kind}`

These are the §12 names above, so later steps add labels, not new names.

## Amendment 2 (2026-11-19) · Run approvals, waiting in the model-wait pause

The owner chose approvals as the next gap to close (2026-11-19). This amends
§5: a tool in `approve` mode waits for the seller's decision inside the
call, and the run waits in ADR-20261027090012's model-wait pause instead of
ADR-1151's frozen state. Taint (§4) stays a later step; `approve` is set per
tool for now.

### Why not the frozen state

§5 froze the run through agentd and schedd. ADR-20261027090012 rejected that
state for waits inside a call:
- it costs a database write and a schedd round trip per wait;
- thawing re-admits the reservation, and destroys the VM if it no longer fits;
- its TTL and parking have no meaning for a run.

An approval is a wait inside a call, so the same reasoning applies.

The model-wait pause already gives what §5 asked for. While outboundd holds
the call, the relay connection is open and silent and the guest CPU is idle,
so vmmd quiet-pauses the VM:
- **Unbilled:** the paused seconds are excluded from billing.
- **Timers stop:** the guest's in-process timers stop while it is paused, so
  a harness's MCP request timeout does not run down while a person decides.

Where the pause is off (the version opts out, or vmmd's switch is off), the
wait is billed like any other call, and the harness's own tool timeout bounds
it.

### The flow

1. **Request.** The run calls a tool whose policy mode is `approve`. outboundd
   writes an `agent_run_approvals` row in one transaction:
   - the tool;
   - a SHA-256 of the arguments;
   - a bounded summary of the arguments, untrusted agent text, at most
     `AgentRunApprovalSummaryMaxBytes`;
   - an expiry.

   In the same transaction it queues an `agent_run.approval_requested`
   webhook to the account's receivers that subscribe to it.
2. **Wait.** outboundd holds the MCP call open and reads the decision every
   `AgentRunApprovalPollMillis`. It lifts the relay connection's write
   deadline for that request only, up to the approval's expiry plus the
   tool's timeout.
3. **Decision.** The seller calls
   `POST /v1/agent-runs/{id}/approvals/{approval_id}` with
   `{"decision": "approve"|"deny", "note"}`. apid writes an
   `agent_run_approval_decisions` row. Each approval has one decision, which
   cannot be changed, and an expired approval refuses one with 409.
   `GET /v1/agent-runs/{id}/approvals` lists a run's approvals.
4. **Outcome:**
   - **Approved:** the call counts against the tool-call budget and runs as
     any allowed call.
   - **Denied or expired:** the call returns an error result naming the
     outcome and the seller's note. The tool never runs, and the budget is
     not charged.
   - **The harness goes away first:** the approval stays as it is until it
     expires, and a late decision changes nothing.

### Bounds

- **Expiry.** `min(now + AgentRunApprovalTimeoutSec, the run's deadline −
  AgentRunApprovalDeadlineMarginSec)`. A run's deadline keeps running while
  it waits, as in ADR-20261027090012 §3. Pausing the deadline while a person
  decides is a later change to vmmd's run timer.
- **Pending approvals.** At most `AgentRunApprovalsPendingMax` per run. Past
  that, the call is refused at once, as `denied` in the decision rows.
- **Matching.** The arguments digest is shown to the seller and kept with the
  decision. A decision approves exactly the call that asked for it, never a
  later call with other arguments.

### Ownership

- outboundd is the only writer of `agent_run_approvals` and of the webhook
  delivery it queues with it.
- apid is the only writer of `agent_run_approval_decisions`. The decision is
  customer intent.
- outboundd only reads decisions.

### Limits (`pkg/api/limits.go`)

| Name | Value |
|---|---|
| `AgentRunApprovalTimeoutSec` | 3,600 (bounded by the run's deadline) |
| `AgentRunApprovalDeadlineMarginSec` | 30 |
| `AgentRunApprovalsPendingMax` | 8 |
| `AgentRunApprovalSummaryMaxBytes` | 2,048 |
| `AgentRunApprovalPollMillis` | 1,000 |
| `AgentRunApprovalNoteMaxBytes` | 1,024 |

`AgentApprovalTimeoutSec` (86,400) in §5 is replaced by
`AgentRunApprovalTimeoutSec`. No run lasts a day yet. The value grows when
run deadlines pause during a wait.

### Metrics

`agent_approvals_total{outcome}`, the §12 name, where outcome is `approved`,
`denied`, `expired` or `abandoned`.

### Rollout

1. This amendment.
2. Data and API:
   - `approve` mode in policies;
   - the two tables;
   - apid's list and decide routes, with an audit event;
   - the webhook event.
3. outboundd: the request, the held call, the outcome, the metric and the
   ops docs.
4. **Not in this step:**
   - taint (§4);
   - pausing the run deadline while waiting;
   - Slack as an approval channel (open question 2);
   - showing approvals in the dashboard.


## Amendment 3 (2026-11-20) · The run deadline pauses while a person decides

Amendment 2 left the run's deadline running during an approval wait, so a
seller who takes ten minutes to answer can cost a run with a 600 s timeout
its whole budget. The owner chose to close that gap (2026-11-20). This
amendment also corrects amendment 2.

### Correction to amendment 2

Amendment 2 said the guest's timers stop while the model-wait pause holds the
VM. They do not: ADR-20261027090012 §3 records that the guest clock is not
stepped on resume and keeps host time (ADR-906), so a guest timer that would
have expired during the pause expires at resume. Two timers matter:
- **The harness's tool timeout.** agentd's preset wiring (PR #7683) sets it
  to `AgentRunApprovalTimeoutSec + 120` s for codex and claude-code. A seller
  image sets its own.
- **The run's timeout** in the guest and in vmmd. This amendment extends it.

### The rule

A run's deadline is `running_at + timeout_sec + credit`, where **credit** is
the wall time during which at least one of the run's approvals was waiting,
capped at `AgentRunApprovalCreditMaxSec`. Overlapping approvals count once.
An approval waits from `created_at` until the first of:
- its decision;
- its expiry;
- `released_at`, when outboundd stopped holding the call (the harness went
  away, or the call ended for any other reason).

Credit is wall time, paused or not. Where the model-wait pause is off the
wait is billed, as amendment 2 says; the credit only moves the deadline.

### Who enforces it

- **vmmd holds the deadline.** `AgentRunStart.deadline_sec` carries the
  seller's `timeout_sec` beside the request, whose own `timeout_sec` is now
  the guest ceiling. vmmd's host timer and the exchange's context start at
  `deadline_sec` and move to `deadline_sec + credit` when agentd says so,
  never past the ceiling. At the deadline vmmd resumes the guest and sends
  the cancel frame, and it reports the run as `timed_out`, not `cancelled`.
  An older vmmd reads only the request and so gives the run the whole
  ceiling; the release order below keeps that from happening.
- **agentd computes credit.** While a run's version has an `approve` tool,
  agentd reads the run's credit every `AgentRunApprovalCreditPollSec` and,
  when it grew, sends `AgentRunExtend{credit_ms}` on the RunAgent stream. The
  value is the run's total, so a repeated or reordered frame does no harm.
  schedd forwards it to vmmd unchanged. agentd only reads approval rows;
  outboundd still writes them.
- **The guest timer is a ceiling.** The guest kills the harness at the
  request's `timeout_sec`, which it validates against
  `agentrunproto.MaxTimeoutSec`. For a version with an `approve` tool,
  agentd sends `min(timeout_sec + AgentRunApprovalCreditMaxSec,
  MaxTimeoutSec)`, and vmmd enforces the real deadline. `MaxTimeoutSec`
  rises from 3,600 to 7,200 so a Scale run at its 3,600 s plan limit can
  still earn credit.
- **outboundd's expiry** becomes the first of `now +
  AgentRunApprovalTimeoutSec` and `running_at + least(timeout_sec +
  deadline_credit_max_sec, MaxTimeoutSec) − AgentRunApprovalDeadlineMarginSec`.
  While a call waits it earns credit one second per second until the cap, so
  the deadline stays the same distance ahead until the cap is reached; the
  cap's deadline is therefore the last moment a wait can end. With no credit
  (`deadline_credit_max_sec = 0`) this is amendment 2's expiry. An expiry
  that leaves 1 s or less still refuses the call.

The margin (30 s) is more than five times the poll (5 s), so agentd extends
the deadline before vmmd reaches it.

### Data

- `agent_run_approvals.released_at timestamptz NULL`, written by outboundd
  when the held call ends. An older outboundd leaves it null, and the wait
  then ends at the decision or the expiry.
- `agent_runs.deadline_credit_max_sec integer NOT NULL DEFAULT 0`, written by
  agentd when the run starts running: the credit this run may earn, 0 when
  the switch is off or the version has no `approve` tool. outboundd reads it
  for the expiry, so outboundd needs no switch of its own.

Both are expand-only.

### Bounds

- A run outlives its `timeout_sec` by at most `AgentRunApprovalCreditMaxSec`.
- A paused VM keeps its memory on the node for the whole wait. Measured
  admission (ADR-909) already counts it; the cap bounds it.
- Approval waits are the only source of credit. A model call is not.

### Limits (`pkg/api/limits.go`)

| Name | Value |
|---|---|
| `AgentRunApprovalCreditMaxSec` | 3,600 |
| `AgentRunApprovalCreditPollSec` | 5 |
| `agentrunproto.MaxTimeoutSec` | 7,200 (was 3,600) |

### Release order

Units release independently (docs/release-compatibility.md), so each step
works beside the previous release:
1. This amendment.
2. Guest: `MaxTimeoutSec` 7,200. An older agentd never sends more than 3,600.
3. Data and outboundd: both columns, `released_at` writes and the new
   expiry, which is unchanged while every run's credit cap is 0.
4. schedd and vmmd: the `AgentRunExtend` frame and the movable deadline. An
   older schedd or vmmd ignores the frame, as both ignore unknown frames
   today, and the run keeps its plain deadline.
5. agentd: the credit poll and the guest ceiling, behind the switch
   `FAAS_AGENTD_APPROVAL_DEADLINE_CREDIT` (off). It goes on after steps 2–4
   are live on every node, because an older guest refuses a `timeout_sec`
   above 3,600.

### Metrics

`agent_run_deadline_credit_seconds_total`: credit agentd sent to vmmd.
