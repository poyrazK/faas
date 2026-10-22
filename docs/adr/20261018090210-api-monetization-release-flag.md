# ADR-20261018090210: API monetization behind a release flag

- **Status:** accepted
- **Date:** 2026-10-18
- **Decision:** API monetization is off until an operator releases it. The hot runtime setting `api_monetization_enabled` (default `false`; `FAAS_API_MONETIZATION_ENABLED` is only its bootstrap value, as for hosting in ADR-202610171453) gates every customer-facing part of it. Monetization means pricing a customer's own consumers and platform tenants and everything built on that price: rate cards, consumer and tenant plans, usage quotes, statements, completeness checks, usage alerts, credits, spend caps, trials, overdue holds, billing connections, invoice exports, billing runs, statement invoices and consumer self-service billing (ADR-238, ADR-849, ADR-949–954, ADR-975, ADR-1306–1325, ADR-1502–1507).
  - **API.** Its routes (`pkg/apid/monetization`) answer the ordinary missing-route 404 before authentication, CORS, method matching and idempotency replay, re-evaluated on every request. The webhook events only it emits are not offered, so a platform tenant webhook must name its events. The consumer key `billing` scope is refused.
  - **Discovery.** The served OpenAPI document and `/docs` leave out its paths, the components only they use and its webhook events; the embedded source spec and SDKs stay complete, as for durable entities (ADR-712). The capability is `internal` with this operator flag. Its customer docs page is no longer published.
  - **CLI.** Without `FAAS_API_MONETIZATION_ENABLED` in the shell, its verbs and flags are absent from help, completions, man pages, the generated reference and suggestions, and invoking them is a usage error, as for application Auth (ADR-1190). The `customer-platform` starter ships without its billing review.
  - **Background work.** Billing runs, statement invoice syncs and overdue digests, spend cap checks and overdue hold syncs pause.
  - **Gateway.** `gatewayd-internal` follows the same setting. Off, it enforces no consumer plan, spend cap or overdue hold, records no usage-threshold alert, serves a held platform tenant, does not hold responses for their usage (ADR-1505) and passes `Gregale-Usage` through untouched (ADR-1318).
- **Why:** Monetization moves customers' money and had not been tested against live providers. It must stay invisible and inert until it is qualified.
- **Consequences:**
  - **Data stays.** Turning it off deletes nothing; cards, statements, holds and schedules resume when it is turned on. A hold placed while it was on lifts only after it is turned on again.
  - **Usage still recorded.** Consumer and tenant request usage keeps flowing to apid without the response hold, so usage reads and later statements stay complete; app-reported units are not recorded while it is off.
  - **Prose.** Descriptions of unrelated schemas in the served spec can still mention statements.
  - **Release.** Set `api_monetization_enabled` in the operator console; apid and gateways apply it within seconds. Then publish the docs page, list the capability and ship the CLI with the variable set.
- **Rejected alternatives:**
  - Removing the code or the spec entries would lose work that only needs qualification.
  - A per-account allowlist would leave the feature discoverable to every other account through the CLI and spec.
