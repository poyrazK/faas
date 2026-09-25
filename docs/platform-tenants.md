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

## Consolidate usage across apps

Create a statement for an explicit UTC-minute period after configuring each app's versioned API-consumer rate cards:

```http
POST /v1/account/platform-tenants/{id}/usage-statements
Content-Type: application/json

{"period_start":"2026-09-01T00:00:00Z","period_end":"2026-10-01T00:00:00Z"}
```

The draft contains one frozen line per tenant-attributed app/consumer/minute or app/surface/minute with its effective app rate-card ID, price, units, and amount. Exactly one of `consumer_id` and `surface_id` identifies the source. A request on a verified, active, linked customer hostname without a consumer key is surface usage; a linked key is consumer usage and is counted only once. A key belonging to a different or unlinked tenant is rejected on that hostname. Other anonymous app domains and independent JWT traffic without a tenant-surface hostname are not attributed to a platform tenant. Public traffic to a customer hostname may become that customer's billable usage, so configure the app's versioned rate card deliberately and review drafts before handoff. The total is in one currency; mixed-currency apps return 422 rather than an invented converted total. Usage without an effective card remains explicitly unpriced and prevents finalization. Periods are at most 90 days, and one statement is limited to 20,000 minute lines; split larger periods. A period with no new billable usage does not create an empty statement.

Use `GET /v1/account/platform-tenants/{id}/usage-statements?period_start=…&period_end=…` for all revisions, or `GET .../usage-statements/{statement_id}` for one snapshot. `POST .../{statement_id}/finalize` freezes the billable lifecycle once every unit is priced in a single currency. `POST .../{statement_id}/handoff` with `{"external_invoice_id":"your-invoice-123"}` records one provider-neutral receipt for your billing system; `GET` on the same path retrieves it. Gregale does not collect payment. A handoff conflicts if the same app consumer has already had an overlapping app-local statement handed off (or vice versa), and the same external invoice ID cannot be used on both paths.

If rates or usage change while a draft is open, repeating create makes a new snapshot revision and marks the old draft `superseded`; unchanged drafts replay. A superseded draft cannot be finalized or handed off. If more events are delivered **after finalization**, repeat the original create request. Gregale returns the next revision containing **only the new units**, which can be finalized and handed off as an explicit adjustment. Repeating without new units returns the latest existing revision. Prior lines, prices, and external invoice references are never edited. These statements use event-time tenant attribution, not current consumer or surface links. See [ADR-238](adr/238-cross-app-platform-tenant-statements.md) and [ADR-239](adr/239-platform-tenant-surface-usage.md) for the overlap and reconciliation boundaries.

New gateways keep unacknowledged usage in a local fsynced outbox and replay it after apid outages or restarts; disabling the optional request debugger no longer disables usage recording. Operators should monitor `gateway_consumer_usage_outbox_pending_records`, `_pending_bytes`, `_failures_total`, and `gateway_consumer_usage_delivery_failures_total`. Do not remove the spool to clear a backlog. This improves delivery after an event reaches the gateway exit funnel, but a crash before that event is fsynced can still miss a served request; see [ADR-234](adr/234-durable-consumer-usage-delivery.md) before using totals for customer invoices.

To temporarily stop the linked credential and hostname paths, send `PATCH /v1/account/platform-tenants/{id}` with `{"status":"suspended"}`. New keys cannot be issued for its linked consumers while suspended. Linked hostnames are blocked when tenant-surface routing is enabled. Send `{"status":"active"}` to resume. Existing keys are not revoked or rotated by either transition.

On requests authenticated with a linked consumer key, or anonymous requests routed through a verified tenant-surface hostname, Gregale sends `X-Faas-Platform-Tenant-Id` to the guest and records `platform_tenant.id` on its request/forward traces. This is the stable account-level customer ID across apps and key rotations. It is distinct from `X-Faas-Tenant-Id`, which remains the app owner's account ID. Anonymous traffic on other domains receives no claim; incoming copies of the header are stripped. A consumer key presented on a hostname bound to a different platform tenant, including an unlinked key, is rejected with the same non-enumerating invalid-key response. Suspension is checked on cached hostname routes as well as cache misses; a custom-domain request may fail closed if the tenant guard's database read is unavailable.

The CLI provides the same lifecycle with `gregale platform-tenants add|apply|list|info|activation|link-consumer|link-surface|usage|suspend|resume`.

Suspension does not block anonymous traffic on unlinked app domains, independent JWT authentication on those domains, or credentials not linked to the tenant. Configure those separately if you need a complete customer access ban. Reads and writes require the same MFA-gated account scopes as API consumer management; Free plans do not expose this feature.
