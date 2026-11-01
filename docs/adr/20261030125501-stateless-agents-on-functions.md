# ADR-20261030125501 · Stateless agents on functions: event-driven agents that hold nothing while they wait

- **Status:** proposed
- **Date:** 2026-10-30
- **Builds on:** ADR-1322 (realtime channels as function triggers), ADR-1030 (lease-fenced realtime consumer groups), ADR-1039 (per-entity consumer-group ordering), ADR-974 (function idle timeout), ADR-1180 (long async invocations), ADR-989 (outbound relay), ADR-994 (AI gateway end-user limits), ADR-20261027090016 (agent capability gateway).
- **Related:** ADR-20261027090010 (agent runs), ADR-20261027090013 (sandbox sessions), ADR-20261027090015 (agent memory tiering). AWS Infinity and its Reactive Agent Protocol (RAP), released 2026-10-23 as a research release (`hydro-project/infinity`).
- **Decision:**
  - Gregale Agents gains a second shape beside harness runs: a **stateless agent**. Its loop runs as short, event-driven slices. Between events it is only its stored history, with no VM, process or open connection.
  - Gregale supplies the platform around the loop, not the loop itself (owner decision D2):
    - an ordered inbox per conversation thread;
    - one function invocation per inbox message;
    - signed callback URLs so tools answer asynchronously (RAP);
    - delayed messages for timers;
    - model calls through the AI gateway;
    - sandboxed work through agent runs.
  - Sellers bring the loop: Infinity's step mode with Gregale-backed stores, or any loop that keeps the slice contract (§1).
  - Harness runs (Claude Code, Codex) keep their VM path. ADR-20261027090012 and ADR-20261027090015 bring their idle cost down to 0 CPU and about 25 MB.
- **Why:**
  - **Owner goal (2026-10-30):** an idle agent uses 0 CPU and as little RAM as possible, ideally none, and a minimal agent is nearly free.
  - **An unmodified harness CLI keeps its state in process memory.** Its floor without writing memory to disk is the compressed live heap: about 25 MB for Claude Code and 14 MB for Codex (ADR-20261027090015).
  - **A loop that keeps its state in a database has no such floor.** Infinity measured about 100 KB per idle agent, which is the size of its history, on a simulated model with in-memory stores.
  - **Gregale already has the serverless pieces Infinity's AWS deployment uses:**
    - functions scale to zero, and a parked function holds no RAM (invariant 4);
    - realtime consumer groups lease, deduplicate, retry and dead-letter messages, and order them per key (ADR-1039). That is the SQS FIFO message-group contract.
    - ADR-1322 already turns each consumer-group message into one function invocation.
- **Consequences:**
  - **Idle cost.** A stateless agent between events holds no VM and no instance: 0 CPU and 0 RAM. Its history and seen-message IDs stay in the seller's Postgres.
  - **Work is billed as ordinary function time.** A slice is billed as function compute (plan RAM + 8 MB per running second, spec §4.7). A 256 MB function running 20 slices of 5 s costs about 0.007 GB-h, or $0.00007 at $0.01/GB-h, plus the seller's model tokens.
  - **Sellers write their loop to the slice contract.** Claude Code and Codex cannot run as stateless agents. They stay harness runs, and a stateless agent can call one through `gregale.run` (§4).
  - **New public surface:** the callback endpoint (§3) and the platform tools (§4). Both stay behind the `agents` capability until the owner tests them on production.
- **Rejected alternatives:**
  - **Gregale writes its own agent loop:** owner decision D2. Sellers' loops are the product.
  - **An agentd-owned inbox table:** it would rebuild the ordering, leases, deduplication, retries and dead letters that consumer groups already have.
  - **An external queue service (SQS, NATS):** a new control-plane dependency, and it does not run on bare metal.
  - **A VM per conversation, kept warm:** that is harness runs, and its idle floor is the reason for this ADR.

## 1. The slice contract

A stateless agent's handler receives one inbox message and does at most this:

1. loads the thread's history and the IDs of messages it has already processed;
2. skips the message if its ID is already processed (delivery is at least once);
3. runs at most one model completion;
4. persists the new turn, before it dispatches anything;
5. dispatches at most one asynchronous tool call, or runs cheap synchronous tools inline and completes again;
6. returns.

A handler that blocks on a tool for longer than its invocation limit fails the slice, and the consumer group retries it.

The invocation limit is the app's request timeout under ADR-1180: Hobby 1 min, Pro 5 min, Scale 15 min. A completion has to finish inside it. Realtime-trigger invocations reach it since rollout step 2 (ADR-1322's 2026-10-31 amendment).

## 2. The inbox

- **Channel and group.** Each stateless agent app gets one realtime channel, `agent-inbox`, and one consumer group, `slices`:
  - `ordering_metadata_key: thread_id`;
  - dead-letter policy `block`, so a poisoned message stops only its own thread;
  - deduplication on `message_id`.
- **Trigger.** A `kind: realtime` trigger (ADR-1322) binds the group to the agent's function. The group never leases a message while an earlier message with the same ordering key is leased or pending (ADR-1039). So one thread's slices run in order, and different threads run in parallel up to the trigger's `max_concurrency`.
- **Threads.** A thread is an ordering key. Spawning a child thread means publishing its first message under a new key. A child's report to its parent is an ordinary message on the parent's key.
- **What arrives as a message:** user input, tool results, subscription events, timers and child reports. Each carries a stable `message_id`, the `thread_id` and a `kind`.

## 3. Callbacks: the RAP callback endpoint

- **Per-thread callback URLs.** apid issues a callback URL for one thread, signed with an HMAC over account, app, thread, expiry and allowed kinds. Example: `POST /v1/agents/callbacks/{token}`. The handler gets it from the platform SDK when it dispatches a tool call.
- **What a post does.** A valid post publishes one message into that thread's lane on `agent-inbox`, through the same path as the realtime publish API.
- **The token is the only credential.** It names one thread and the message kinds it may carry (`tool_result`, `event`). A tool cannot address another thread or another app.
- **Lifetime.**
  - A tool call's token expires after `AgentCallbackTokenTTLSec`.
  - A subscription's token lives until the subscription or thread closes, and closing it revokes it.
  - Following RAP's security guidance, tokens are short-lived where the tool allows.
- **Protocol.** Results follow RAP's tool-result and subscription-event messages, so Infinity's RAP client works unchanged. A tool that speaks plain HTTP only needs to POST JSON.

## 4. Platform tools

The capability gateway (ADR-20261027090016) serves these platform tools over RAP. They acknowledge at once and post their results to the callback URL.

Amendment (2026-11-02): until the gateway exists, apid serves them as a RAP toolset at `/v1/agent-tools` (rollout step 5). RAP tool names allow letters, digits, `_` and `-`, and model APIs refuse dots, so each tool's operation name is written with `_`: `gregale_sleep`, `gregale_run`, `gregale_github_subscribe`.

| Tool | What it does |
|---|---|
| `gregale.run` | Starts an agent run (ADR-20261027090010) with the given harness, prompt and inputs, and posts its result when the run ends. Sandboxed shell, build and test work happens here, with the run's own pause and compression. |
| `gregale.sleep` | Publishes a message to the thread after a delay (delayed tasks). |
| `gregale.github.subscribe` | Turns githubd's `gregale.github` events for a repository into subscription events on the thread. |
| `gregale.web.fetch`, `gregale.web.search` | The tier-1 web tools of ADR-20261027090018. |

Existing MCP servers stay reachable through the capability gateway. A synchronous MCP call runs inside the slice, so it counts against the invocation limit.

## 5. Stores

- **History and seen-message IDs stay in the seller's Postgres:** managed Postgres or the app's own database. Gregale ships no platform history table. The data stays the seller's, under the seller's retention and tenancy (ADR-226).
- **Gregale ships an open-source adapter** for Infinity's conversation store and state store on Postgres, and a template app that wires them to the inbox, the callback endpoint and the platform tools.

## 6. Model access

The slice calls the model through the app's managed AI integration over the relay (ADR-989). So the relay must be on for the app's node: control_plane_service `faas_outboundd_relay_listen` and vmmd_service `faas_vmmd_outbound_relay_tls_managed` (#6005).

- Per-tenant token and cost limits (ADR-994) and spend caps apply as for apps.
- A seller's tenant identity travels with each thread message as metadata, so the AI gateway attributes the call to the tenant.

## 7. Capacity and cost

- **Idle agents** are rows in the seller's database. A node holds none of them.
- **Active slices** are function instances. They are admitted, frozen and parked like any function (ADR-974, ADR-1151). A thread that receives no message parks its function after the idle timeout, so 0 RAM.
- **A slice that waits on a long model stream** holds its function instance for that time, billed as running. Pausing a function while it waits on the relay is open question 2.

## Limits (`pkg/api/limits.go`, proposed)

| Limit | Value | Basis |
|---|---|---|
| `AgentCallbackTokenTTLSec` | 86,400 | A tool call's result window. Subscriptions use their own lifetime. |
| `AgentCallbackPayloadMaxBytes` | 4,096, the realtime message limit | A result is one inbox message |
| `AgentCallbacksPerMinutePerThread` | 600, per apid replica | Stops a runaway tool from flooding one thread |

## Rollout

Each step is one PR behind the `agents` capability.

1. This ADR.
2. Raise the realtime-trigger invocation limit to the app's request timeout for agent inboxes (amends ADR-1322).

   **Built (2026-10-31), for every realtime trigger.** The gateway already gave each trigger record the app's timeout (ADR-1180). The limit was the consumer group's unrenewed claim lease. schedd now renews a realtime delivery's claim while it runs (ADR-1322 amendment), so no agent-inbox marker is needed.
3. The signed callback endpoint in `pkg/apid/agents`, which publishes into a thread's lane, with property tests: a token never publishes outside its thread, an expired or revoked token is refused, and a redelivered `message_id` is delivered once.

   **Built, part one (2026-11-01).**
   - **Routes.** `POST /v1/agent-callbacks` issues a URL for one thread of one of the account's realtime endpoints. `POST /v1/agent-callbacks/{token}` is the token-only route a tool posts to; it is limited per IP like `/v1/hooks/{token}`. The root is not `/v1/agents/callbacks/…` because that pattern conflicts with `/v1/agents/{agent}/runs` in Go's router. Both routes belong to the `agents` capability.
   - **Token.** `agcb1.<payload>.<mac>`: an HMAC-SHA256 over account, endpoint, thread, allowed kinds, issue time and expiry. Its key is derived with HKDF from apid's session key, which every replica shares. Decoding is strict, so a token has one spelling.
   - **When a tool posts.** The token's expiry is checked, then the account's `agents` capability, then that the endpoint still belongs to the account. A failure answers like an unknown route.
   - **Publish.** `data` (JSON, at most `AgentCallbackPayloadMaxBytes`, the realtime message limit) is appended to the endpoint's `agent-inbox` channel with metadata `thread_id`, `kind` and `message_id`, and idempotency key `callback:{thread}:{message_id}`. A retried post lands once and gets the first sequence; the same `message_id` with other data is a 409.
   - **Tests.** Unit and property tests in `pkg/apid/agents` cover the thread scope against tampered tokens, issue and post rules, refusal after issue, and landing once.
   **Built, part two (2026-11-01).**
   - **Revocation.** `POST /v1/agent-callbacks/revocations` with `{endpoint_id, thread_id}` closes a thread to every URL issued for it until then. apid records the time in `agent_callback_revocations`, one row per endpoint and thread. A post is refused like an unknown route when its token was issued at or before that time. A URL issued later works, so a thread can reopen.
   - **Rate limit.** Each apid counts one thread's posts in fixed one-minute windows. Past `AgentCallbacksPerMinutePerThread` (600) it answers `429 agent_callback_rate_limited` with the limit and the count. Each replica counts alone, so N replicas accept at most N × 600: the limit stops a runaway tool, and a post needs a valid token anyway.
   - **Tests.** `TestPropertyRevokedCallbackNeverPublishes`: over random issues, revocations and posts, a URL publishes exactly when it was issued after its thread's latest revocation. Tests also cover the revoke and rate-limit rules and the store's revocation record.
4. The Infinity Postgres store adapter and the template app, and a journey in `tests/journeys`: a two-turn stateless agent that sleeps, wakes on its callback and answers.

   **Built, part one (2026-11-01): what the template needs from the platform.**
   - **The app's own credential.** `POST /v1/agent-callbacks` and `POST /v1/agent-callbacks/revocations` also take a `gregale:agents` workload token (docs/workload-identity.md), so the app holds no account key.
     - The token must come from a live instance of a production deployment of an active account's app, as for queue leases (ADR-1141 amendment 2026-10-27).
     - It reaches only that app's realtime endpoints. An API key with `deploy:write` still reaches the endpoints of its account, but only those whose app the organization check lets it act on (fix, 2026-11-01).
       - These routes name an endpoint, not an app, so apid looks up the endpoint's app and runs the check every app route runs (appaccess).
       - On a company app, a callback URL counts as one of the app's credentials: issuing or revoking one needs owner or admin. A key bound to another organization reaches nothing, and neither does a creator who has been removed from the organization or whose organization is suspended.
       - Test: `TestAgentCallbacksFollowCompanyAppAccess`.
     - A JWT-shaped bearer takes the workload path. No API key, OIDC or hosting token is JWT-shaped, so no key is misrouted.
   - **The inbox in `gregale.yaml`.** A manifest consumer group takes `ordering_metadata_key`, `deduplication_metadata_key` and `dead_letter_policy`, so the app declares §2's `slices` group itself. A deploy still creates a missing group and never changes an existing one.
   - **Tests.** `TestWorkloadCaller` covers the audience, environment, account, app, instance and deployment checks. `TestCallbackAppCallerOwnsOnlyItsEndpoints` covers the app scope. The manifest tests cover the new fields' loading, validation and reconcile.

   **Built, part two (2026-11-01): the template and the journey.**
   - **Template `agent-stateless-node`.** It is unlisted while `agents` is internal, and `gregale deploy --template agent-stateless-node` deploys it.
     - Its `gregale.yaml` declares the `agent` endpoint, the `slices` group on `agent-inbox` (§2's ordering, deduplication and `block` dead-letter policy) and the realtime trigger. The endpoint refuses client connections; the seller's backend publishes.
     - Each slice keeps §1's contract. History and seen-message IDs are rows in the seller's Postgres (`DATABASE_URL`). The turn is persisted before its tool call goes out. A redelivery finishes a dispatch or a revocation the first delivery did not.
     - Each slice opens one Postgres connection and closes it, so a parked agent holds no connection and no snapshot captures a socket.
     - Callback URLs are issued and revoked with the app's own `gregale:agents` workload token. The answer revokes the thread's URLs.
     - The model is scripted, so the starter runs with no provider key. Step 4b replaces it with Infinity's step mode and adds the Infinity store adapter.
   - **Journey `TestStatelessAgent`** (inventory row 167, full tier). It skips while `agents` or `realtime` is hidden or managed Postgres provisioning is closed.
     - It deploys the template with a managed Postgres store and publishes a question.
     - It reads the tool call, parks the agent, and posts the tool's result to the callback URL twice: it lands once.
     - It reads the answer the woken agent wrote, and checks that the answered thread refuses its callback URL.

   **Step 4b, part one (2026-11-01): callback URLs take RAP's own messages**, as §3 decides. This is what lets Infinity's RAP tools post unchanged. RAP is pinned to the spec in `hydro-project/infinity` at 1abc6fcff1 (2026-10-27), the one its 0.2.0 crates implement. That answers half of open question 1; MCP's asynchronous tasks stay open.
   - A body with a `type` is a RAP callback. Its `group_id` must be the URL's thread, and the inbox message's data is the RAP message itself.
   - `tool_result` becomes kind `tool_result`. Its `message_id` is `rap-tool-result-` plus the call ID, or a digest of an ID that is not a valid message ID, so a retried result lands once.
   - `subscription_event` becomes kind `event`. It carries no identity of its own, so each post is a new message, as in Infinity's receiver.
   - `view_update` is acknowledged and not published: it is display state, not agent input.
   - `oauth` and `user_choice` are refused until the platform has a flow for them.
5. The platform tools: `gregale.sleep`, `gregale.run`, then `gregale.github.subscribe`.

   **Step 5, part one (2026-11-02): Gregale's RAP toolset and `gregale_sleep`.** The capability gateway is still proposed, so apid serves the toolset, behind `agents`. The gateway can serve the same toolset later.
   - **A standard RAP tool server.** `GET /v1/agent-tools/.well-known/rap-toolset` returns the toolset, `POST /v1/agent-tools/invoke` takes invocations, and `POST /v1/agent-tools/close_thread` is acknowledged. An Infinity runtime adds `https://api.gregale.dev/v1/agent-tools` to its tool servers.
   - **The callback URL is the credential.** RAP leaves invocation authentication to the implementation.
     - An invocation's `callback_url` must be a callback URL apid issued for the invocation's `group_id`, carrying tool results. It is checked as a post to it would be: signature, expiry, revocation, the account's `agents` capability and the per-thread rate limit.
     - So the toolset needs no key, and a call can only answer its own thread.
     - An invocation apid cannot answer is refused like an unknown route, and counts toward the per-IP failure limit.
   - **Errors are tool results.** As RAP asks, a valid invocation is acknowledged with 200. Bad arguments, an unknown operation or a full schedule budget reach the agent as the call's tool result.
   - **`gregale_sleep`.** It takes `seconds`, as Infinity's own `sleep` tool does, or an RFC 3339 `until`, and an optional `reason`. A sleep reaches at most `AgentSleepMaxSeconds` (30 days) ahead; an `until` that has passed wakes at once.
     - The wake-up is the call's RAP tool result, such as `Slept for 60 seconds.`, held as a realtime schedule on the inbox until it is due. It is the inbox message a RAP tool posting that result would publish. A sleeping thread holds that row and nothing else.
     - The schedule's ID derives from the thread and the call ID, so a retried invocation finds its sleep. A wake-up that fails to publish is retried.
     - Revoking a thread's callback URLs cancels its pending sleeps. A sleep created while a revocation runs checks the revocation again and cancels itself, so a revoked thread never wakes (`TestPropertyRevokedThreadNeverWakes`).
     - Sleeps count against the endpoint's 256 realtime schedules (`state.ManagedRealtimeScheduleLimit`, which also counts finished schedules for a day). That bounds the sleeps of one endpoint in preview; a sleep store of its own is left to the capability gateway.
   - **Thread closure.** RAP's `close_thread` names a thread without a credential, so it changes nothing. A thread closes by revoking its callback URLs.
   - **Next.** The templates do not call it yet. Part two wires `gregale_sleep` into the templates and the journey.
6. Preview on the dogfood account, then the seller beta with harness runs (ADR-20261027090010 milestone B).

## Open questions

1. **RAP is a week-old research protocol.** Pin the spec version Gregale implements, and decide whether MCP's asynchronous tasks are accepted too.
2. **Should a slice that waits on the relay be paused like an agent run** (ADR-20261027090012) and its waiting seconds go unbilled? For functions that would change ADR-1151's semantics, so it needs its own decision.
3. **Should Gregale offer a platform history store** for sellers without a database? It would need retention, tenancy and per-tenant deletion like outputs.
4. **Should one invocation carry several inbox messages?** It would cut wake-ups for chatty threads. ADR-1322 left batching to a separate decision.
