# ADR-20261027090016 · The agent capability gateway: one policy, budget and audit for models, tools, web and data

- **Status:** proposed (owner decision D19, 2026-10-19). §6 amended the same day: where audit records and counters are stored.
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
