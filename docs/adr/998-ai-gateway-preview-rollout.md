# ADR-998 · AI gateway preview: plan entitlement, staged account rollout and qualification

- **Status:** accepted
- **Date:** 2026-10-13
- **Relates to:** ADR-991 (release gate), ADR-996, ADR-997 (phases 6–7), ADR-988 (AI gateway)
- **Decision:** Promote the AI gateway capability from `internal` to `preview`.
  - **Plan entitlement.** It is included on Hobby, Pro and Scale. The registry's plan list is enforced in apid, which refuses Free with `402 plan_feature_gated`, and in outboundd, which refuses a customer AI integration whose account is not entitled with `403`. A downgraded account therefore stops calling at once.
  - **Staged rollout.** `ai_gateway_accounts` (or `FAAS_AI_GATEWAY_ACCOUNTS`) in both daemons limits the gate to listed accounts during the preview; empty or `*` admits every entitled account.
  - **Settlement timeout.** It rises from 2 s to 15 s.
  - **Qualification evidence.** An acceptance test, failure drills, a soak harness, alerts, dashboard panels, a runbook and a customer page ship with the promotion.
- **Why:**
  - Seven phases of the gateway sat behind a gate with no customer traffic. The capability rule (`docs/capabilities.md`) requires evidence, documentation, entitlement, an owner and a rollback procedure to change maturity.
  - The release gate is all-or-nothing per platform. A preview needs a first internal account and a few design partners before every entitled account.
  - The gateway had no plan check: the registry listed no plans while `internal`, and nothing compared an account's plan to the AI surface.
  - The soak found that a settlement could time out at 2 s under contention and drop the call's usage, which leaks the caps the gateway promises.
- **Consequences:**
  - **For customers.** Free accounts lose access when the gate is on; they had none in practice, since the gate was off. An account outside the rollout list sees the capability as unavailable in `GET /v1/capabilities` and gets `403 outbound_ai_gateway_unavailable`.
  - **For operators.** Operator-owned AI integrations are not plan-gated and ignore the account list.
  - **Config consistency.** The account list must match in apid and outboundd. The runbook names the symptom of a mismatch.
  - **Settlement latency.** A settlement now holds the handler up to 15 s when Postgres is slow. The provider's answer has already been written, but the end of the HTTP response waits.
  - **Per-integration throughput.** All calls of one integration serialize on its admission-state row, and each call commits three times. Throughput per integration follows Postgres commit latency, not pool size: on the soak machine, a larger pool was slower. This is a preview limit, documented for customers, and the first rollout step measures it in production.
- **Rejected alternatives:**
  - **Feature flags (`pkg/state/feature_flags`).** That system releases a customer's *application* behavior to the customer's end users. It is not a platform entitlement and cannot reach outboundd.
  - **Gate by plan only, with no account list.** The first production traffic should be our own and a few partners'. A plan switch opens every Hobby account at once.
  - **Retry a failed settlement.** A timed-out commit may still land, so a retry could double-count without an idempotency key. A longer bound plus the `FaasAIGatewaySettlementFailing` alert is safer for the preview. Idempotent settlement keyed by the admission lease is the follow-up if production shows timeouts.
  - **Raise outboundd's Postgres pool.** The soak showed the per-integration row lock, not the pool, bounds throughput.

## Enforcement

| Where | Check | Refusal |
| --- | --- | --- |
| apid, every AI endpoint and integration creation with `ai_provider` | gate on | `403 outbound_ai_gateway_unavailable` |
| | account in the rollout list | `403 outbound_ai_gateway_unavailable` |
| | plan entitled in `pkg/productcap/catalog.json` | `402 plan_feature_gated` |
| outboundd, every call to an AI integration | gate on | `503 outbound_ai_gateway_disabled` |
| | customer integration: account in the list and plan entitled | `403 outbound_ai_gateway_unavailable` |
| `GET /v1/capabilities` | entitlement, then gate and list | `plan_not_entitled` or `runtime_unavailable` |

outboundd reads the account plan with the integration it resolves, so routes and replays are checked per target.

## Qualification evidence

- **Acceptance.** `pkg/outbound/ai_gateway_acceptance_test.go::TestAIGatewayPreviewAcceptance`, against Postgres: a Pro account's app calls through the relay. The test checks redaction, settlement, cost, capture and the daily cap, and that the rollout and the Free plan both close the gateway.
- **Failure drills.** `TestAIGatewayFailureDrills` covers Postgres unreachable at admission, a failing settlement, gate rollback, a stuck replay and a provider outage.
- **Soak.** `TestAIGatewaySoak` is opt-in with `FAAS_AI_SOAK=1`. It checks for errors, settlement drift, leaked leases and goroutine growth.
- **Operations.**
  - New metrics: `outbound_ai_media_total` and `outbound_ai_replays_total`.
  - Four dashboard panels: settlement writes, content findings, media usage and replays.
  - Alerts `FaasAIGatewaySettlementFailing` (page) and `FaasAIGatewayProviderErrors` (warn).
  - The runbook `docs/runbooks/FaasAIGateway.md`, with the rollout steps and the rollback.
- **Documentation.** The customer page `docs/ai-gateway.md`, referenced by the capability registry.
