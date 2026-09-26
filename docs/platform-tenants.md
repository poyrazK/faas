# Platform tenants

A platform tenant represents one of your customers across multiple Gregale apps. It complements the app-local API consumer and tenant-surface resources; it does not replace either one.

Create the account-level identity once:

```http
POST /v1/account/platform-tenants
Content-Type: application/json

{"external_ref":"customer-42","name":"Customer 42"}
```

Repeating that request with the same name returns the existing tenant. Link each app's existing consumer with `POST /v1/account/platform-tenants/{id}/consumers` and `{"consumer_id":"…"}`. Link an existing tenant surface with `POST /v1/account/platform-tenants/{id}/surfaces` and `{"surface_id":"…"}`. A consumer or surface cannot belong to two platform tenants, and cross-account IDs return 404. Unlinked resources keep their current behavior.

## Onboard a customer across apps

Use one additive, retry-safe operation to register a customer, create or reuse its app-local consumer identities, and create or attach its hostname surfaces:

```http
POST /v1/account/platform-tenants/apply
Content-Type: application/json

{
  "external_ref": "customer-42",
  "name": "Customer 42",
  "dry_run": true,
  "consumers": [
    {"app_id": "<api-app-uuid>", "external_ref": "customer-42", "name": "Customer 42"},
    {"app_id": "<worker-app-uuid>", "external_ref": "customer-42", "name": "Customer 42"}
  ],
  "surfaces": [
    {"app_id":"<api-app-uuid>", "name":"Customer 42 web", "hostnames":["customer42.example.com"]}
  ],
  "surface_ids": ["<optional-existing-surface-uuid>"]
}
```

The response reports `create`, `link`, or `unchanged` for each resource, including hostnames. New hostnames return a TXT record name (`_faas-verify.<hostname>`) and challenge token to publish in DNS. A dry run checks ownership, quota, names, and conflicts without writes; a token for a planned hostname is withheld because it is not yet durable. Remove `dry_run` (or set it to `false`) to apply the entire local database bundle atomically; replaying it returns the same IDs and tokens with `unchanged` actions. A different name, revoked consumer, hostname already claimed by another surface, or resource owned by another tenant returns 409 without partial writes. Missing or cross-account app/surface IDs return 404. Omitted resources are **not** detached or revoked, and a suspended tenant is not silently resumed. Surface declarations require the tenant-surfaces feature flag and a plan that includes surfaces; this flow currently supports `per_host_san` certificates.

DNS verification and certificate issuance are asynchronous; the apply operation does not claim they are ready or issue consumer keys. `GET /v1/account/platform-tenants/{id}/activation` reports whether routing is enabled, each hostname is verified, the certificate is issued and unexpired, and every linked surface is active. `ready` is true only when all these conditions hold for at least one surface and the platform tenant is active. Certificate and hostname errors remain visible for diagnosis. The CLI equivalent is `gregale platform-tenants apply --file customer.json --dry-run`, then repeat without `--dry-run` after inspecting the plan. Use `gregale platform-tenants activation --id <uuid>` for a snapshot or add `--wait --timeout 10m` to poll until ready. Use `--json` for machine-readable output.

## Issue and rotate customer credentials

After onboarding, use `POST /v1/account/platform-tenants/{id}/credentials/apply` to issue keys to linked consumers across apps. Generate each `ck_` credential locally with a cryptographically secure generator (or `api.PreparePlatformTenantCredential` in the Go client), save its plaintext in your secret store **before** calling Gregale, and submit only its eight-character prefix and hex SHA-256 digest. Gregale never receives or returns the plaintext in this flow. For example:

```json
{
  "dry_run": true,
  "keys": [{"consumer_id":"<linked-consumer-uuid>","name":"customer-42-v1","prefix":"d34db33f","hash":"<64-hex-character-sha256>","scopes":["read"]}],
  "revoke_key_ids": []
}
```

Preview with `dry_run`, then submit the same bundle without it. Replaying an identical bundle returns `unchanged` and the same key ID. To rotate, use a new name and key, and put the old key ID in `revoke_key_ids`; both changes commit together. If clients need an overlap window, issue the new key first and revoke the old key in a later call. Omitted keys remain active. A conflicting name/hash or inactive linked consumer returns 409; cross-account or unlinked IDs are not accepted. `GET /v1/account/platform-tenants/{id}/credentials?limit=100&offset=0` lists metadata, including revoked keys, but never plaintext. If you lose the local secret, rotate it—Gregale cannot recover it. See [ADR-236](adr/236-platform-tenant-credential-reconciliation.md) for the security and retry model.

The CLI exposes the hash-only bundle with `gregale platform-tenants credentials-apply --id <uuid> --file keys.json --dry-run` and then without `--dry-run`, and metadata with `credentials-list --id <uuid>`. The file must contain only the request fields shown above; keep plaintext in your own secret store, not in the bundle.

The older app-local key-create endpoint still returns plaintext once, but no longer caches that response for `Idempotency-Key` retries. Repeating that creation with the same name conflicts; use the hash-only tenant flow for retryable multi-app issuance.

`GET /v1/account/platform-tenants/{id}` shows the linked consumers and surfaces. `GET /v1/account/platform-tenants?limit=100&offset=0` pages the registry. `GET /v1/account/platform-tenants/{id}/usage?since=…&until=…` sums durable request, error, and billable-unit facts attributed to that tenant **when each request occurred**, grouped by UTC day and app. Each bucket identifies either a linked `consumer_id` or a verified `surface_id`. Linking a consumer or surface later does not import its earlier traffic. Historical rows and requests from older gateways without a tenant claim remain unassigned; Gregale never guesses their owner from the current link. This is raw usage, not an invoice or a cross-app price quote.

## Let downstream customers inspect their own usage and statements

Platform owners can issue a separate read-only credential to one downstream tenant:

```http
POST /v1/account/platform-tenants/{id}/access-tokens
Content-Type: application/json

{"name":"customer billing portal","scopes":["platform_tenant:usage:read","platform_tenant:statements:read"]}
```

The response contains an `fp_tenant_` bearer exactly once. Save it in the customer's secret manager; Gregale persists only its SHA-256 hash. The default lifetime is 90 days and the maximum is 365 days. Keep names unique among active tokens and issue no more than ten at a time; list metadata with `GET .../{id}/access-tokens` and revoke with `DELETE .../{id}/access-tokens/{token_id}`. Revocation is immediate. These special scopes cannot be added to ordinary account API keys.

The downstream service sends its bearer to `GET /v1/platform-tenant-self/usage?since=…&until=…` or `GET /v1/platform-tenant-self/usage-statements?period_start=…&period_end=…&limit=100&offset=0`. Statement listing returns lightweight summaries of finalized revisions only, newest period/revision first, with `next_offset` when another page exists; `GET /v1/platform-tenant-self/usage-statements/{statement_id}` retrieves one full finalized revision and its line items. Draft, superseded, and other tenants' statements are hidden as not found. Tenant identity comes from the credential, not a caller-supplied tenant ID. There is no write, invoice-handoff, activity, or account-management access through this bearer. See [ADR-247](adr/247-platform-tenant-self-service.md).

## Control customer requests across apps

After every gateway is upgraded, set an optional shared admission budget:

```http
PUT /v1/account/platform-tenants/{id}/request-budget
Content-Type: application/json

{"max_requests_per_minute":1000,"max_requests_per_day":50000}
```

`GET` on the same path returns the ceilings, admitted-request counters for the current UTC minute and day, and their reset times. Both fields are required on `PUT`; zero disables that dimension, and both zero means no active budget. There is no default ceiling. The configured safety maxima are 1,000,000 per minute and 100,000,000 per day. Counts combine linked consumer-key traffic and verified tenant-surface traffic across every app. Requests on unrelated app domains or independent JWT identities without a platform tenant are outside this policy.

One admitted request consumes one unit even if its guest later fails. The gate runs after ordinary app/account limits but before request buffering or waking an instance. At the ceiling, Gregale returns `429 tenant_request_budget_exceeded`, `Retry-After`, and `x-faas-rate-limit-scope: platform-tenant`; rejected requests are not billed. If the authoritative counter cannot be checked, tenant-attributed traffic returns `503 tenant_request_budget_unavailable` rather than relying on a replica-local fallback. This is an admission control, not an exact monetary spend cap or an invoice. See [ADR-240](adr/240-platform-tenant-request-budgets.md).

## Triage one customer's request activity across apps

Use the account-scoped activity view to inspect recent retained debugger evidence for a customer:

```http
GET /v1/account/platform-tenants/{id}/activity?since=24h&status=503&limit=100
```

Optionally filter by `app_id` and continue with the opaque `next_cursor`. The endpoint is gated by the account's debugger plan and clamps its lookback to plan retention. Each row identifies its app and includes safe diagnostic metadata such as route template, status, latency bucket, deployment, and request/trace ID when present. Publisher-collapsed rows include a `count`; page totals weight by that count. This is sampled, retention-bound diagnostic evidence—not a complete request log, billing ledger, or guarantee that every failed request was captured. Request/response bodies, headers, and credentials are never exposed. See [ADR-246](adr/246-platform-tenant-request-activity.md).

The equivalent CLI is `gregale platform-tenants activity --id <tenant-uuid> --since 24h --status 503`; use `--cursor <next_cursor>` to page through results. Add `--json` when a support workflow needs machine-readable output.

## Consolidate usage across apps

Set an optional customer-specific request price that applies across every app attributed to one platform tenant:

```http
POST /v1/account/platform-tenants/{id}/rate-cards
Content-Type: application/json

{"currency":"EUR","price_millicents_per_unit":1500,"effective_from":"2026-10-01T00:00:00Z"}
```

`GET .../{id}/rate-cards` lists the immutable versions in effective-time order. If `effective_from` is omitted, Gregale uses the next UTC minute. Each tenant uses one currency; a new card conflicts if that customer already has a different currency. From the effective minute onward, the tenant price overrides each app's rate card for this customer's billable request units. Earlier minutes continue using their app's rate card, so a period spanning two currencies cannot be finalized. Customers without a tenant rate card keep the existing per-app pricing behavior. Statement lines record either `platform_tenant_rate_card_id` or `rate_card_id` as the exact price source; finalized revisions are never rewritten, and late usage remains an additive adjustment. This prices the amount your customer is charged, not Gregale's compute or infrastructure cost.

Create a statement for an explicit UTC-minute period; when no tenant rate card is effective for a minute, its app's versioned API-consumer rate card is used:

```http
POST /v1/account/platform-tenants/{id}/usage-statements
Content-Type: application/json

{"period_start":"2026-09-01T00:00:00Z","period_end":"2026-10-01T00:00:00Z"}
```

The draft contains one frozen line per tenant-attributed app/consumer/minute or app/surface/minute with its effective app rate-card ID, price, units, and amount. Exactly one of `consumer_id` and `surface_id` identifies the source. A request on a verified, active, linked customer hostname without a consumer key is surface usage; a linked key is consumer usage and is counted only once. A key belonging to a different or unlinked tenant is rejected on that hostname. Other anonymous app domains and independent JWT traffic without a tenant-surface hostname are not attributed to a platform tenant. Public traffic to a customer hostname may become that customer's billable usage, so configure the app's versioned rate card deliberately and review drafts before handoff. The total is in one currency; mixed-currency apps return 422 rather than an invented converted total. Usage without an effective card remains explicitly unpriced and prevents finalization. Periods are at most 90 days, and one statement is limited to 20,000 minute lines; split larger periods. A period with no new billable usage does not create an empty statement.

Use `GET /v1/account/platform-tenants/{id}/usage-statements?period_start=…&period_end=…` for all revisions, or `GET .../usage-statements/{statement_id}` for one snapshot. `POST .../{statement_id}/finalize` freezes the billable lifecycle once every unit is priced in a single currency. `POST .../{statement_id}/handoff` with `{"external_invoice_id":"your-invoice-123"}` records one provider-neutral receipt for your billing system; `GET` on the same path retrieves it. Gregale does not collect payment. A handoff conflicts if the same app consumer has already had an overlapping app-local statement handed off (or vice versa), and the same external invoice ID cannot be used on both paths.

If rates or usage change while a draft is open, repeating create makes a new snapshot revision and marks the old draft `superseded`; unchanged drafts replay. A superseded draft cannot be finalized or handed off. If more events are delivered **after finalization**, repeat the original create request. Gregale returns the next revision containing **only the new units**, which can be finalized and handed off as an explicit adjustment. Repeating without new units returns the latest existing revision. Prior lines, prices, and external invoice references are never edited. These statements use event-time tenant attribution, not current consumer or surface links. See [ADR-238](adr/238-cross-app-platform-tenant-statements.md) and [ADR-239](adr/239-platform-tenant-surface-usage.md) for the overlap and reconciliation boundaries.

Subscribe your billing service to finalized revisions at the tenant level so a customer spanning apps produces one event, not one callback per app:

```http
POST /v1/account/platform-tenants/{id}/webhooks
Content-Type: application/json

{
  "target_url": "https://billing.example.com/gregale/events",
  "webhook_secret": "<secret-from-your-secret-manager>",
  "delivery_format": "cloudevents"
}
```

The event filter is fixed to `platform_tenant.statement.finalized`, subject to your plan's shared account webhook quota. The target must pass Gregale's HTTPS and egress checks. Store your secret before submitting it; Gregale returns only a masked value. The default `json` format remains available, while `cloudevents` sends a CloudEvents 1.0 structured event whose `source` is `urn:gregale:platform-tenant:<uuid>` and whose `data` contains the full statement snapshot and stable `external_ref`. The delivery is enqueued in the same database transaction that finalizes the statement: one durable row is created per subscription and statement revision. Webhook delivery is retryable and at-least-once, so deduplicate using the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature` with the configured secret.

Use `GET /v1/account/platform-tenants/{id}/webhooks` to manage subscription IDs, `PATCH` or `DELETE /{webhook_id}` to update or remove one, and `POST /{webhook_id}/rotate-secret` to rotate its signing key. `GET /{webhook_id}/deliveries` lists durable delivery attempts newest first; retry a dead delivery with `POST /{webhook_id}/deliveries/{delivery_id}/retry`. New subscriptions do not backfill statements that were already finalized. See [ADR-245](adr/245-platform-tenant-statement-webhooks.md).

New gateways keep unacknowledged usage in a local fsynced outbox and replay it after apid outages or restarts; disabling the optional request debugger no longer disables usage recording. Operators should monitor `gateway_consumer_usage_outbox_pending_records`, `_pending_bytes`, `_failures_total`, and `gateway_consumer_usage_delivery_failures_total`. Do not remove the spool to clear a backlog. This improves delivery after an event reaches the gateway exit funnel, but a crash before that event is fsynced can still miss a served request; see [ADR-234](adr/234-durable-consumer-usage-delivery.md) before using totals for customer invoices.

To temporarily stop the linked credential and hostname paths, send `PATCH /v1/account/platform-tenants/{id}` with `{"status":"suspended"}`. New keys cannot be issued for its linked consumers while suspended. Linked hostnames are blocked when tenant-surface routing is enabled. Send `{"status":"active"}` to resume. Existing keys are not revoked or rotated by either transition.

On requests authenticated with a linked consumer key, or anonymous requests routed through a verified tenant-surface hostname, Gregale sends `X-Faas-Platform-Tenant-Id` to the guest and records `platform_tenant.id` on its request/forward traces. This is the stable account-level customer ID across apps and key rotations. It is distinct from `X-Faas-Tenant-Id`, which remains the app owner's account ID. Anonymous traffic on other domains receives no claim; incoming copies of the header are stripped. A consumer key presented on a hostname bound to a different platform tenant, including an unlinked key, is rejected with the same non-enumerating invalid-key response. Suspension is checked on cached hostname routes as well as cache misses; a custom-domain request may fail closed if the tenant guard's database read is unavailable.

The CLI provides the same lifecycle with `gregale platform-tenants add|apply|list|info|activation|link-consumer|link-surface|usage|suspend|resume`.

Suspension does not block anonymous traffic on unlinked app domains, independent JWT authentication on those domains, or credentials not linked to the tenant. Configure those separately if you need a complete customer access ban. Account-scoped platform-tenant management requires the same MFA-gated account scopes as API consumer management; tenant-self read tokens are separate and remain limited to the tenant's own usage and finalized statements. Free plans do not expose the feature.
