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

Resources created by this account-owner bundle operation are marked `managed_by_platform_tenant` in responses and tenant inventory. Existing consumers or surfaces that are only linked are not adopted, and existing database rows default to unmanaged. Hostnames created by this bundle on an existing surface are marked managed individually. The field is omitted for unmanaged resources; a dry run may show `action: create` without the marker because nothing has been persisted yet. The provenance is used by the read-only preview below to classify removal candidates; it does not delete or detach anything.

For an existing tenant, preview the complete desired bundle with `POST /v1/account/platform-tenants/{id}/reconciliation-plan`:

```json
{
  "consumers": [{"app_id":"<api-app-uuid>","external_ref":"customer-42","name":"Customer 42"}],
  "surfaces": [{"app_id":"<api-app-uuid>","name":"Customer 42 web","hostnames":["customer42.example.com"]}],
  "surface_ids": ["<optional-existing-surface-uuid>"]
}
```

The read-only response is sorted deterministically and includes a `plan_hash` for the normalized desired bundle and current ownership-aware diff. Desired resources report `create`, `link`, or `keep`; omitted managed resources appear as `remove_candidate`, while omitted unmanaged resources appear as `retain_unmanaged`. Hostnames are compared only for surfaces declared in `surfaces`; a `surface_ids` entry links a surface without declaring its hostname set. Planning never mutates state.

To apply exactly the previewed plan, send the same desired bundle and `expected_plan_hash` to `POST /v1/account/platform-tenants/{id}/reconciliation-plan/apply` with an `Idempotency-Key`. The server recomputes the plan under the write transaction; a changed plan returns `409 platform_tenant_plan_stale` with no writes, so fetch a fresh preview and review it again. On success, managed consumers and surfaces are detached from the tenant but not deleted, omitted managed hostnames declared through `surfaces` are removed, and unmanaged resources are retained. Desired creates/links and those detachments/removals commit atomically. Writes require deploy-write scope and recent MFA. See [ADR-354](adr/354-platform-tenant-confirmed-reconciliation-apply.md).

Every successful apply, including a confirmed no-op, returns a `receipt_id` and `applied_at` and stores an immutable receipt in the same transaction as the resource changes. Use `GET /v1/account/platform-tenants/{id}/reconciliations?page_size=50` to find recent receipts, then `GET /v1/account/platform-tenants/{id}/reconciliations/{receipt_id}` to recover the exact plan hash and applied change list after a lost response. Follow the opaque `next_page_token` cursor to walk older pages safely while new reconciliations are being recorded. Receipts contain resource identifiers and resulting actions, but never the submitted desired bundle or hostname challenge tokens. Reads require the account's normal read scope and recent MFA. Receipts remain available until the tenant or account is deleted. See [ADR-362](adr/362-platform-tenant-reconciliation-receipts.md).

Platform owners can configure the domain boundary for downstream hostname self-service:

```http
PUT /v1/account/platform-tenants/{id}/hostname-policy
Content-Type: application/json

{"allowed_suffixes":["customers.example.com"],"max_hostnames":20}
```

The allowlist is disabled by default. A delegated hostname must equal an allowed suffix or be a subdomain of it, and must still pass DNS ownership verification and the normal account/plan quotas. The tenant-wide cap counts hostnames already attached across its linked surfaces; lowering the cap never removes existing hostnames, but prevents adding more until the count is below the new limit. Use `{"allowed_suffixes":[],"max_hostnames":0}` to disable delegation. This policy does not create or remove any surface or hostname. See [ADR-299](adr/299-platform-tenant-hostname-delegation.md).

DNS verification and certificate issuance are asynchronous; the apply operation does not claim they are ready or issue consumer keys. `GET /v1/account/platform-tenants/{id}/activation` reports whether routing is enabled, each hostname is verified, the certificate is issued and unexpired, and every linked surface is active. `ready` is true only when the feature is enabled, the platform tenant is active, at least one linked surface exists, and every linked surface is ready. Certificate and hostname errors remain visible to the account owner for diagnosis. The CLI equivalent is `gregale platform-tenants apply --file customer.json --dry-run`, then repeat without `--dry-run` after inspecting the plan. Use `gregale platform-tenants activation --id <uuid>` for a snapshot or add `--wait --timeout 10m` to poll until ready. Use `--json` for machine-readable output.

## Issue and rotate customer credentials

Before enabling downstream credential self-service, an account owner can set the maximum consumer-key scopes and active keys per linked consumer:

```http
PUT /v1/account/platform-tenants/{id}/credential-policy
Content-Type: application/json

{"allowed_scopes":["read","write"],"max_keys_per_consumer":5}
```

The policy is disabled by default. The only delegable key scopes are `read`, `write`, and `admin`; choose the narrowest set your integration needs. Set `allowed_scopes` to an empty array and the limit to zero to disable tenant-side management. A policy update does not change existing keys. Writes require the account's deploy-write scope and recent MFA, and changes are audited. See [ADR-301](adr/301-platform-tenant-credential-policy.md).

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

## Let downstream customers manage hostnames and inspect their own activation, usage, and statements

Platform owners can issue a separate tenant-bound credential with only the capabilities the downstream integration needs:

```http
POST /v1/account/platform-tenants/{id}/access-tokens
Content-Type: application/json

{"name":"customer portal","scopes":["platform_tenant:activation:read","platform_tenant:usage:read","platform_tenant:statements:read","platform_tenant:hostnames:manage"]}
```

The response contains an `fp_tenant_` bearer exactly once. Save it in the customer's secret manager; Gregale persists only its SHA-256 hash. The default lifetime is 90 days and the maximum is 365 days. Grant only the scopes each integration needs. Keep names unique among active tokens and issue no more than ten at a time; list metadata with `GET .../{id}/access-tokens` and revoke with `DELETE .../{id}/access-tokens/{token_id}`. Revocation is immediate. These special scopes cannot be added to ordinary account API keys.

With `platform_tenant:activation:read`, the downstream service can call `GET /v1/platform-tenant-self/activation` to read the current readiness of its own linked surfaces, hostnames, and certificates. Tenant identity always comes from the credential, not a caller-supplied tenant ID. The redacted snapshot omits upstream app IDs, DNS challenge material, and raw DNS/certificate errors; the owner-only activation endpoint retains those diagnostics. `ready` follows the same all-linked-surfaces contract as the owner snapshot.

With `platform_tenant:hostnames:manage`, the customer can add a hostname to one of the surfaces listed in that snapshot:

```http
POST /v1/platform-tenant-self/hostnames
Content-Type: application/json

{"surface_id":"<surface-id-from-activation>","hostname":"shop.customer.example.com"}
```

Only pre-linked surfaces are eligible. The hostname must match an owner-configured DNS suffix and both tenant-wide and per-surface plan limits apply. The response gives the `_faas-verify.<hostname>` TXT record and challenge token while it remains unverified; publish the token as its value. Repeating the same request safely returns the existing pending challenge, and a verified hostname response no longer includes the token. DNS verification and certificate issuance remain asynchronous. This scope cannot create surfaces, change policy, remove hostnames, or access another tenant's data. See [ADR-300](adr/300-platform-tenant-self-service-hostnames.md).

The downstream service sends its bearer to `GET /v1/platform-tenant-self/usage?since=…&until=…` or `GET /v1/platform-tenant-self/usage-statements?period_start=…&period_end=…&limit=100&offset=0`. Statement listing returns lightweight summaries of finalized revisions only, newest period/revision first, with `next_offset` when another page exists; `GET /v1/platform-tenant-self/usage-statements/{statement_id}` retrieves one full finalized revision and its line items. Draft, superseded, and other tenants' statements are hidden as not found. No other writes, invoice handoff, activity, or account management are exposed by these tenant-bound scopes. See [ADR-247](adr/247-platform-tenant-self-service.md) and [ADR-284](adr/284-platform-tenant-self-activation.md).

## Let downstream customers rotate their own consumer keys

An owner can separately grant `platform_tenant:credentials:read` for linked-consumer and key metadata, and `platform_tenant:credentials:manage` for key creation, rotation, and revocation. The owner must first enable the tenant's [credential delegation policy](adr/288-platform-tenant-credential-policy.md); it restricts key scopes and the active-key ceiling per consumer. The manage scope never overrides that policy, and revocation stays available after issuance is disabled.

`GET /v1/platform-tenant-self/consumers` returns only consumer IDs, external references, names, and statuses for the bearer tenant. `GET /v1/platform-tenant-self/credentials?limit=100&offset=0` returns key metadata without hashes or plaintext. Use `POST /v1/platform-tenant-self/credentials/apply` with the same hash-only bundle format as the owner API. Generate each key locally, store its plaintext in your own secret manager before sending the prefix and SHA-256 hash, and use the returned metadata to confirm the result. The tenant ID comes from the bearer; IDs linked to another tenant are not accepted. See [ADR-317](adr/317-platform-tenant-self-service-credentials.md).

## Let downstream tenants onboard customer identities

Customer provisioning has its own owner-controlled gate, separate from key-scope delegation. Read or update `/v1/account/platform-tenants/{id}/consumer-provisioning-policy`; updates require recent MFA. The policy is disabled by default. Enabling it requires a per-tenant active-customer cap, and disabling it requires a zero cap. An owner may also mint the tenant-bound `platform_tenant:consumers:manage` capability; it cannot be used as an account-wide API key.

With that capability, call `POST /v1/platform-tenant-self/consumers` with `surface_id`, `external_ref`, and `name`. The selected surface must already be active and linked to the bearer tenant; app and tenant IDs are never accepted. Creation and cap enforcement are atomic. An identical retry returns the existing identity, while an existing unlinked or conflicting identity is not adopted. The response omits app and account metadata. See [ADR-334](adr/334-platform-tenant-consumer-provisioning-policy.md) and [ADR-293](adr/293-platform-tenant-self-service-customers.md).

To offboard customers, call `POST /v1/platform-tenant-self/consumers/revoke` with 1-100 unique `consumer_ids` from that tenant's customer listing. Gregale validates the entire batch before changing anything, then revokes each identity and its active keys atomically. A mixed-tenant, unlinked, or unknown ID returns the same not-found response without partial cleanup. Revocation remains available when new-customer provisioning is disabled or the tenant is suspended; a repeated request is safe and reports zero newly revoked keys. See [ADR-335](adr/335-platform-tenant-self-service-customer-offboarding.md).

For customer onboarding across multiple apps, call `POST /v1/platform-tenant-self/consumers/apply` with one stable `external_ref`, a display `name`, and 1-100 unique `surface_ids` from this tenant's active activation inventory. Gregale derives app IDs from those linked surfaces, and a batch may include at most one surface per app. It creates one app-local customer identity per app in a single all-or-nothing operation; the owner-controlled policy and active-customer cap apply to the batch as a whole. Set `dry_run: true` to check policy, limits, and conflicts and preview `create`/`unchanged` actions without mutation or planned IDs. Exact retries are unchanged and remain valid if the owner later disables new provisioning. This endpoint does not issue keys; apply credentials separately after reviewing the plan. See [ADR-336](adr/336-platform-tenant-self-service-multi-app-onboarding.md).

```http
POST /v1/platform-tenant-self/consumers/apply
Authorization: Bearer <tenant-token>
Content-Type: application/json

{"external_ref":"customer-42","name":"Customer 42","surface_ids":["<surface-a>","<surface-b>"],"dry_run":true}
```

After reviewing the per-surface preview, send the same request with `dry_run` omitted to commit the bundle. The apply rechecks current policy, cap, surface links, and identity conflicts; a changed condition fails without creating a partial set.

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

Omitting `event_filter` preserves the existing `platform_tenant.statement.finalized` subscription. Select `platform_tenant.hostname.verified`, `platform_tenant.surface.certificate.changed`, `platform_tenant.surface.deployment.changed`, `platform_tenant.customer.linked`, `platform_tenant.customer.offboarded`, and/or `platform_tenant.reconciliation.applied` for lifecycle and reconciliation updates; one receiver may subscribe to all seven supported events. The filter is immutable after creation, so create a replacement subscription to change it. Subscriptions use your plan's shared account webhook quota. The target must pass Gregale's HTTPS and egress checks. Store your secret before submitting it; Gregale returns only a masked value. The default `json` format remains available, while `cloudevents` sends a CloudEvents 1.0 structured event whose `source` is `urn:gregale:platform-tenant:<uuid>` and whose `data` contains the event payload and stable tenant `external_ref`.

`platform_tenant.hostname.verified` is emitted only when a hostname on a surface explicitly linked to that platform tenant transitions from unverified to DNS-verified. It includes the tenant and surface IDs, surface name, app ID, hostname ID and name, stable `external_ref`, and verification timestamp. It never includes the DNS challenge token. This records proof of DNS control only; certificate issuance and hostname routing are separate lifecycle steps. Gregale enqueues the event in the same database transaction as the verification transition, once per subscription and hostname. Events are not backfilled for hostnames verified before subscription creation. Statement events likewise enqueue transactionally, once per subscription and statement revision. Webhook delivery is retryable and at-least-once, so deduplicate using the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature` with the configured secret.
`platform_tenant.surface.certificate.changed` is emitted for each certificate-state transition on a surface explicitly linked to that platform tenant. The payload includes the tenant and surface identity, `cert_state`, recorded `cert_not_after` (nullable), and transition time. It excludes provider error text, certificates, and private keys; use the activation snapshot for current diagnostics. A delivery is inserted in the same database transaction as the state transition, once per subscription and transition. There is no backfill for transitions that happened before the receiver was created. This event reports certificate lifecycle only; it does not assert that the tenant is active or that every route is ready. Webhook delivery is retryable and at-least-once, so deduplicate using the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature` with the configured secret.

`platform_tenant.surface.deployment.changed` is emitted when a deployment for an explicitly linked surface transitions to `live` or `failed`. The payload includes tenant and surface identity, the deployment revision, outcome, start time, and transition time; it omits app/deployment IDs, source metadata, logs, and raw errors. A failed latest attempt does not mean that an older deployment is not still serving. Events are enqueued transactionally, are not backfilled, and are delivered at-least-once; deduplicate using the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature` with the configured secret.

`platform_tenant.customer.linked` is emitted when an active app-local customer identity is created for or linked to the platform tenant. `platform_tenant.customer.offboarded` is emitted only when a linked identity transitions from active to revoked. Multi-app customers produce one event per app-local consumer; join them with `customer_external_ref` while `consumer_id` and `app_id` identify the specific app identity. Both payloads include the platform tenant ID/reference, customer external reference/name/status, and transition time, but never credentials or hashes. Events are inserted with the identity transition, delivered at-least-once, and are not backfilled; deduplicate with the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature`.

`platform_tenant.reconciliation.applied` is emitted for every successful confirmed reconciliation, including an already-converged no-op. Its payload contains the tenant ID and stable external reference, receipt ID, plan hash, apply timestamp, and change count—not the submitted desired bundle or full change list. Gregale inserts the delivery with the immutable receipt in the apply transaction, so a failed or stale plan cannot emit a success event. Fetch the receipt detail to recover the exact applied changes. Delivery is at-least-once and not backfilled; deduplicate with the CloudEvents `id` / `X-Faas-Delivery-Id` and verify `X-Faas-Webhook-Signature`. See [ADR-363](adr/363-platform-tenant-reconciliation-applied-webhook.md).

Use `GET /v1/account/platform-tenants/{id}/webhooks` to manage subscription IDs, `PATCH` or `DELETE /{webhook_id}` to update or remove one, and `POST /{webhook_id}/rotate-secret` to rotate its signing key. `GET /{webhook_id}/deliveries` lists durable delivery attempts newest first; retry a dead delivery with `POST /{webhook_id}/deliveries/{delivery_id}/retry`. New subscriptions do not backfill statements that were already finalized. See [ADR-245](adr/245-platform-tenant-statement-webhooks.md).

New gateways keep unacknowledged usage in a local fsynced outbox and replay it after apid outages or restarts; disabling the optional request debugger no longer disables usage recording. Operators should monitor `gateway_consumer_usage_outbox_pending_records`, `_pending_bytes`, `_failures_total`, and `gateway_consumer_usage_delivery_failures_total`. Do not remove the spool to clear a backlog. This improves delivery after an event reaches the gateway exit funnel, but a crash before that event is fsynced can still miss a served request; see [ADR-234](adr/234-durable-consumer-usage-delivery.md) before using totals for customer invoices.

To temporarily stop the linked credential and hostname paths, send `PATCH /v1/account/platform-tenants/{id}` with `{"status":"suspended"}`. New keys cannot be issued for its linked consumers while suspended. Linked hostnames are blocked when tenant-surface routing is enabled. Send `{"status":"active"}` to resume. Existing keys are not revoked or rotated by either transition.

On requests authenticated with a linked consumer key, or anonymous requests routed through a verified tenant-surface hostname, Gregale sends `X-Faas-Platform-Tenant-Id` to the guest and records `platform_tenant.id` on its request/forward traces. This is the stable account-level customer ID across apps and key rotations. It is distinct from `X-Faas-Tenant-Id`, which remains the app owner's account ID. Anonymous traffic on other domains receives no claim; incoming copies of the header are stripped. A consumer key presented on a hostname bound to a different platform tenant, including an unlinked key, is rejected with the same non-enumerating invalid-key response. Suspension is checked on cached hostname routes as well as cache misses; a custom-domain request may fail closed if the tenant guard's database read is unavailable.

The CLI provides the same lifecycle with `gregale platform-tenants add|apply|list|info|activation|link-consumer|link-surface|usage|suspend|resume`.

Suspension does not block anonymous traffic on unlinked app domains, independent JWT authentication on those domains, or credentials not linked to the tenant. Configure those separately if you need a complete customer access ban. Account-scoped platform-tenant management requires the same MFA-gated account scopes as API consumer management; tenant-self read tokens are separate and remain limited to the tenant's own usage and finalized statements. Free plans do not expose the feature.
