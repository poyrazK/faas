# AI gateway

Operational owner: the outbound integrations owner (outboundd and the
`pkg/outbound` package), with apid for the customer API. ADRs:
[ADR-991](../adr/991-ai-gateway-hard-caps-providers-visibility.md) (release
gate), [ADR-998](../adr/998-ai-gateway-preview-rollout.md) (preview rollout).
Dashboard: **Gregale AI gateway** (`deploy/grafana/ai-gateway.json`).

## Preview rollout

The gate and the account list are the `ai_gateway` platform flag
([ADR-1700](../adr/1700-platform-flags.md),
[platform flags](../ops/platform-flags.md)). One value in the database is read
by both **apid** (which refuses the API) and **outboundd** (which refuses the
calls), so they can no longer disagree. Every step applies within seconds and
needs no restart. `gregalectl flags show --key ai_gateway` shows whether both
daemons applied the current version.

1. **Internal account.** `gregalectl flags enable --key ai_gateway` and
   `gregalectl flags allow --key ai_gateway --account-id <internal account>`
   (an account on Hobby or above). Run a chat completion, a streamed call, an
   embedding and a fallback route through the app relay. On the dashboard,
   check that settlement writes are all `recorded`, time to first byte is
   close to the provider's, and the request log shows the calls.
2. **Design partners.** `allow` a few customer accounts, or add them to a
   group the flag targets. Watch the two alerts below and the settlement panel
   for a week.
3. **All entitled accounts.** `gregalectl flags default --key ai_gateway
   --value true` (or a `rollout` first). Free accounts stay closed by plan
   (`402` from apid, `403` from outboundd).

Until an operator writes the flag, it falls back to `ai_gateway_enabled` /
`FAAS_AI_GATEWAY_ENABLED` and `ai_gateway_accounts` /
`FAAS_AI_GATEWAY_ACCOUNTS` as each daemon read them at boot.

## Rollback

`gregalectl flags disable --key ai_gateway --reason <incident> --yes`. Within
seconds:

- AI API endpoints return `403 outbound_ai_gateway_unavailable`;
- AI integrations and routes return `503 outbound_ai_gateway_disabled`;
- integrations without an AI provider are unaffected.

Stored settings, usage, captures and replays stay in place;
`gregalectl flags enable` restores service. To withdraw one account instead,
`gregalectl flags deny --key ai_gateway --account-id <uuid>`. outboundd starts
normally while the flag is off; it logs the operator AI integrations that
will return `503`.

## FaasAIGatewaySettlementFailing

`outbound_ai_usage_record_events_total{event=~"state_error|unavailable"}` has
grown for 10 minutes. Calls still succeed, but their usage is not settled:
caps, cost budgets, usage reports and alerts under-count until it recovers.

1. Check outboundd's logs and Postgres reachability from the control plane.
   `FaasOutboundAdmissionUnavailable` firing at the same time means Postgres
   is down for outboundd, and calls are already refused.
2. A settlement makes up to three attempts of 5 s each, keyed by the call's
   lease so a retry never counts it twice (ADR-1120). Growing `retried`
   events with no `state_error` are slow commits that recovered. Lock waits
   on `outbound_admission_state` rows point at one very busy integration:

   ```sql
   SELECT relation::regclass, mode, count(*) FROM pg_locks
    WHERE NOT granted GROUP BY 1, 2 ORDER BY 3 DESC;
   ```

3. If settlement does not recover within the hour, close the gate (rollback
   above): uncapped AI spend on customers' keys is worse than an outage.

## FaasAIGatewayProviderErrors

More than half of AI calls returned provider errors for 15 minutes. The
gateway is usually not the cause:

- check the providers' status pages;
- check `outbound_credential_key_events_total` (keys cooling down after 429s)
  and `outbound_circuit_breaker_events_total` on the dashboard;
- `gregale ai logs <integration-id>` (as the customer, or the request log in
  Postgres) shows the status of recent calls.

Act only if errors come from the gateway itself, such as
`outbound_ai_translation_unsupported` after a release.

## Canaries, log export and moderation

These ADR-1340 features are customer settings; none pages. Two warn when
the platform rather than one customer is at fault:
[FaasAIGatewayCanaryRollbackWave](#faasaigatewaycanaryrollbackwave) and
[FaasAIGatewayModerationFailing](#faasaigatewaymoderationfailing).

- **Canary rollbacks.** `outbound_ai_route_canary_rollbacks_total` rising
  means customers' new models did worse than their current ones. The
  customer sees the reason in `gregale ai routes canary <route-id>`.
  Investigate only if every canary on the platform rolls back at once: that
  points at the gateway, not the models.
- **Moderation errors.** `outbound_ai_moderation_total{result="error"}` counts
  checks that failed. They usually come from the customer's check
  integration: a cap reached, a provider outage, or a deleted integration.
  - With `on_error: block`, the customer's calls get
    `503 outbound_ai_moderation_unavailable`. Point them at the check
    integration's usage and caps.
  - Errors on many unrelated accounts at once point at the in-process path.
    Read the outboundd logs.
- **Export lag.** Lines reach drains through the gateways' drain workers.
  - A customer missing lines: check the app's drain health
    (`gregale log-drains`), as for runtime logs.
  - Lines pile up when no gateway owns the app (an app with no owner node).
    They expire after 24 hours, so a longer outage loses them.

## Batches, access tokens and evaluations

These ADR-1431 features are customer settings; none pages.
[FaasAIGatewayBatchesSettledUnknown](#faasaigatewaybatchessettledunknown)
warns when a batch is charged at its reservation.

- **Batches.** `outbound_ai_batches_settled_total{outcome="unknown"}` rising
  means batches reach 72 hours without the provider ending them, or the
  gateway cannot read their status.
  - Open batches: `SELECT provider, state, checks, created_at FROM
    outbound_ai_batches WHERE state <> 'settled' ORDER BY created_at`.
  - A batch that holds a customer at a cap is released by its settlement.
    Never delete an open row by hand: its usage would then never be recorded.
- **Access tokens.** The public listener is off unless `public_listen_addr`
  is set. A burst of `401 outbound_access_token_invalid` on one host is
  usually a leaked or stale token; the customer revokes it with
  `gregale ai tokens revoke`. To close the public host, unset
  `public_listen_addr` and restart outboundd.
- **Evaluations.** `outbound_ai_evaluations_total{result="failed"}` counts
  evaluations that could not run. Most failures name the customer's setup
  (no captures, a deleted judge integration). Many `outbound_ai_evaluation_interrupted`
  failures point at outboundd restarts during long evaluations.

## Unmetered calls

ADR-1432 refuses writes the gateway cannot meter; none pages.

- **Refusals.** `outbound_rejections_total{reason="outbound_ai_endpoint_unmetered"}`
  counts them. A customer whose SDK needs an endpoint outside the lists (the
  ADR has them) either sets `--allow-unmetered on` or asks for the endpoint to
  be metered or listed as free. Add a free endpoint only when its provider
  documents it as free of charge.
- **Allowed.** `outbound_ai_unmetered_calls_total` counts calls let through.
  Their spend is outside the caps; a customer surprised by a provider bill
  should check this first.

## FaasAIGatewayCanaryRollbackWave

Warn. Three or more model canaries rolled back within 30 minutes across the
platform. One rollback is a customer's new model doing worse than the
route's first target; that is the feature working. Several at once point at
something they share.

1. On the dashboard's *Canary rollbacks* panel, split by reason. `error_rate`
   rollbacks usually mean the canary models' provider is failing; check its
   status page and *AI calls by usage outcome*.
2. List the rollbacks and their routes:

   ```sql
   SELECT c.route_id, c.reason, c.decided_at, t.integration_id, t.model
     FROM outbound_ai_route_canaries c
     JOIN outbound_ai_route_targets t ON t.route_id = c.route_id AND t.position = c.position
    WHERE c.state = 'rolled_back' AND c.decided_at > now() - interval '1 hour'
    ORDER BY c.decided_at;
   ```

   One provider or model family across unrelated routes is a provider
   incident. Customers restart their canaries when it recovers; nothing is
   lost, because a rolled-back route serves its first target.
3. Rollbacks across providers that started with an outboundd release point at
   the gateway's judgement or error accounting. Roll the release back.

## FaasAIGatewayModerationFailing

Warn. More than half of request moderation checks failed for 15 minutes.
Calls with `on_error: block` get `503 outbound_ai_moderation_unavailable`;
with `on_error: allow` they go through unchecked.

Customer-managed integrations share the metric label `customer_managed`, so
first find out whether one account carries the traffic. Check calls run
through the gateway as caller `moderation`:

```sql
SELECT integration_id, status, count(*)
  FROM outbound_ai_request_log
 WHERE caller = 'moderation' AND created_at > now() - interval '30 minutes'
 GROUP BY 1, 2 ORDER BY 3 DESC;
```

- One check integration with `429` or `403` statuses: its cap or key. That
  account's calls are affected and nobody else's; point the customer at
  the check integration's usage and caps.
- Failures across many check integrations, or errors with no check calls at
  all: the in-process path. Read outboundd's logs and roll back a recent
  release.

## FaasAIGatewayResponseChecksFailing

Warn. More than 10% of checked AI responses ended `unchecked` or `error` for
15 minutes (ADR-1435). Neither is released: callers got
`502 outbound_ai_response_unchecked` or `503 outbound_ai_moderation_unavailable`.

- `unchecked` dominates on *Response checks by result*: responses larger
  than the check limit (`OutboundAIResponseCheckMaxBytes`, 4 MiB) or streams
  the gateway could not parse. A new provider response format after a
  provider change parses badly for everyone; compare the start of the rise
  with *Route fallbacks and translations* and the latest release.
- `error` dominates: response moderation is failing; work through
  [FaasAIGatewayModerationFailing](#faasaigatewaymoderationfailing).

## FaasAIGatewayBatchesSettledUnknown

Warn. A provider batch settled as `unknown` in the last 6 hours: it never
ended within 72 hours, or outboundd could not read its status. Its
reservation was recorded as its usage, so the customer was charged an
estimate, not the batch's real usage.

1. Look for `outboundd: AI batches` warnings in outboundd's logs. Status reads
   that fail on every batch point at the provider credential or a changed
   batch API.
2. Find the batch:

   ```sql
   SELECT id, provider, provider_batch_id, checks, created_at, settled_at
     FROM outbound_ai_batches
    WHERE outcome = 'unknown' AND settled_at > now() - interval '6 hours';
   ```

3. Check its status at the provider. A batch that did finish there has real
   usage the gateway never read; tell the customer their charge was the
   reservation.

## Routes, tool fees, tags and caching

ADR-1433 to ADR-1440 add dashboard panels and no alerts:

- **Route strategies** (ADR-1434). *AI route calls by strategy and first
  pick* shows how weighted, latency and cost routes chose their first
  target. `explore` stays near 5% of latency and cost calls; much more means
  most targets lack recent stats.
- **Tool fees** (ADR-1433). *Built-in tool uses* counts web searches, file
  searches and code-interpreter GB. A customer surprised by tool charges
  should see them here and in `gregale ai usage`.
- **Tags** (ADR-1436, ADR-1438). Tag usage lives in Postgres only, because
  tags are customer-defined and would explode metric cardinality.
  `tag_daily_cost_limit` refusals show on *AI budget and guardrail
  rejections*, and tag budget alerts on *AI budget alerts* with
  `budget="tag_daily_cost"`.
- **Prompt caching** (ADR-1437). *Prompt-cache reads per written token*
  below about 0.28 means cache writes cost an integration more than its reads
  save. A customer with `auto_prompt_cache` on and a ratio that stays low
  should turn it off, or send `X-Gregale-AI-Session` only where conversations
  continue.
- **Response cache** (ADR-1440). *Provider cost the response cache avoided*
  is the saving customers see as `cache_saved_millicents`.

## Realtime sessions

ADR 20261019072722 carries OpenAI and Azure OpenAI Realtime sessions, and
ADR 20261020161811 Gemini and Vertex Live sessions; none pages. The
dashboard's *Realtime sessions* panels show them.

- **`closed_cap`** is a customer's cap refusing a response: expected.
- **`closed_unreadable` rising** means providers send messages the gateway
  cannot read: compressed frames (the provider ignored the dropped
  `Sec-WebSocket-Extensions`), messages over 4 MiB, or a changed event format.
  Customers' sessions end with 1008. Check the provider's realtime changelog;
  a format change needs a gateway release. For Live sessions it also counts
  callers whose first message is not a `setup` naming a model, or that send
  none within 10 seconds.
- **`closed_error`** is admission failing mid-session: work through
  `FaasOutboundAdmissionUnavailable`.
- **Active sessions** hold one connection each on outboundd until they end.
  The HTTP drain does not wait for them: a deploy ends them when the old
  process exits, and callers reconnect.

## Failure drills

`pkg/outbound/ai_gateway_drills_test.go` runs these against Postgres on every
CI run (`TestAIGatewayFailureDrills`):

| Drill | Expected |
| --- | --- |
| Postgres unreachable at admission | `503 outbound_admission_unavailable`; nothing reaches the provider |
| Settlement write fails | the caller gets the provider's answer; `state_error` is counted; no tokens settle |
| Gate closed | `503 outbound_ai_gateway_disabled`; nothing reaches the provider |
| Replay stuck mid-run | after 15 minutes the worker fails it as `outbound_replay_interrupted` |
| Provider outage | provider `503`s pass through; after 3 failures the circuit opens (`outbound_circuit_breaker_open`); no tokens settle |

## Soak

`TestAIGatewaySoak` in `pkg/outbound/ai_gateway_soak_test.go` is opt-in:

```sh
FAAS_AI_SOAK=1 FAAS_AI_SOAK_SECONDS=60 FAAS_AI_SOAK_WORKERS=32 FAAS_AI_SOAK_POOL=2 \
  DATABASE_URL=postgres://... go test -run TestAIGatewaySoak -v ./pkg/outbound/
```

Each worker streams a 20-chunk chat completion (100 ms of provider time) for
one integration on a Scale account, using outboundd's production pool size
(2 connections). The test fails on any non-200, any call whose usage did not
settle, any leaked admission lease or goroutine growth.

Results on 2026-10-13 (developer laptop, local Postgres shared with other
work; indicative only):

| Pool | Duration | Calls/s | p50 / p99 overhead | Result |
| --- | --- | --- | --- | --- |
| 2 | 60 s | 25 | 1.08 s / 3.66 s | all settled, no leaks |
| 10 | 30 s | 15 | 1.75 s / 7.47 s | **12 of 438 calls lost usage** (2 s settlement timeout) |
| 10 | 30 s | 46 | 0.47 s / 2.94 s | all settled after raising the timeout to 15 s |

All calls of one integration serialize on its admission-state row. On
2026-10-13 a call took 30.3 Postgres round trips (39.2 with a circuit
breaker), and most of them were spent holding that row.

ADR-1120 pipelines admission and settlement and folds the breaker and
cooldown checks into admission. A call now takes 11 round trips, or 12 with a
breaker; `TestAIGatewayRoundTripBudget` holds that budget. The soak reports
`postgres round trips: N per call`, and `FAAS_AI_SOAK_CIRCUIT=1` turns the
breaker on.

Interleaved runs on 2026-10-14 used the same laptop, the same Postgres, pool 2
and no breaker. Indicative only: other work shared the Postgres lock table, so
a few calls in both builds got `503` from `out of shared memory`.

| Build | Calls/s | p99 overhead |
| --- | --- | --- |
| main (before ADR-1120) | 119–230 | 0.37–3.4 s |
| ADR-1120 | 168–289 | 45–410 ms |

On the production control plane, watch time to first byte against the
provider's and the settlement panel during rollout step 1.

## FaasAIGatewayOnlineEvaluationsFailing

More than half of online evaluations (ADR 20261021171712) got no score for
30 minutes, so quality scores and drift alerts lack data
(ADR 20261027132936).

1. `sum by (instance) (rate(outbound_ai_online_evaluations_total{result="judge_error"}[30m]))`
   shows where they fail.
2. Online evaluations call each account's own judge integration. If one
   account's judge carries most of the traffic, its provider key, quota or
   model is the cause; tell the customer.
3. If many accounts fail at once, check outboundd's logs for the judge calls
   and the providers' status pages.

## FaasAIGatewayAccessTokenLookupsFailing

More than a fifth of public listener access-token lookups failed or found the
lookups saturated for 10 minutes; those callers got
`503 outbound_access_token_unavailable` (ADR 20261027132936).

1. `sum by (result) (rate(outbound_ai_access_token_lookups_total[10m]))`
   splits `busy` from `error`.
2. `busy`: more than `OutboundAIAccessTokenLookupsMax` uncached tokens were
   looked up at once, usually a flood of random tokens at the AI host. Known
   tokens stay cached and keep working. Rate-limit the source at the edge.
3. `error`: the lookups failed; check outboundd's database connection.

## FaasAIGatewayUnpricedCalls

More than a tenth of settled AI calls cost zero for an hour because their
model has no price (ADR 20261027132936). Usage reports undercount them, and
under a cost cap those models are refused with `403 outbound_ai_model_unpriced`.

1. `sum by (integration_id) (rate(outbound_ai_unpriced_calls_total[1h]))`
   shows whether one integration carries them; customer integrations share
   the `customer_managed` label, so look up the models in the request logs.
2. A new model from a major provider: refresh the built-in catalog
   ([operator guide](../ops/outbound-integrations.md#built-in-price-catalog)).
3. One account's custom or OpenAI-compatible model: the customer adds it to
   the integration's price list with `gregale ai prices`.
