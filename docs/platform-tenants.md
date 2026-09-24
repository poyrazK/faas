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

`GET /v1/account/platform-tenants/{id}` shows the linked consumers and surfaces. `GET /v1/account/platform-tenants?limit=100&offset=0` pages the registry. `GET /v1/account/platform-tenants/{id}/usage?since=…&until=…` sums durable request, error, and billable-unit facts attributed to that tenant **when each request occurred**, grouped by UTC day, app, and consumer. Linking a consumer later does not import its earlier traffic. Historical rows and requests from older gateways without a tenant claim remain unassigned; Gregale never guesses their owner from the current link. This is raw usage, not an invoice or a cross-app price quote.

New gateways keep unacknowledged usage in a local fsynced outbox and replay it after apid outages or restarts; disabling the optional request debugger no longer disables usage recording. Operators should monitor `gateway_consumer_usage_outbox_pending_records`, `_pending_bytes`, `_failures_total`, and `gateway_consumer_usage_delivery_failures_total`. Do not remove the spool to clear a backlog. This improves delivery after an event reaches the gateway exit funnel, but a crash before that event is fsynced can still miss a served request; see [ADR-234](adr/234-durable-consumer-usage-delivery.md) before using totals for customer invoices.

To temporarily stop the linked credential and hostname paths, send `PATCH /v1/account/platform-tenants/{id}` with `{"status":"suspended"}`. New keys cannot be issued for its linked consumers while suspended. Linked hostnames are blocked when tenant-surface routing is enabled. Send `{"status":"active"}` to resume. Existing keys are not revoked or rotated by either transition.

On requests authenticated with a linked consumer key, Gregale sends `X-Faas-Platform-Tenant-Id` to the guest and records `platform_tenant.id` on its request/forward traces. This is the stable account-level customer ID across apps and key rotations. It is distinct from `X-Faas-Tenant-Id`, which remains the app owner's account ID. Anonymous requests and unlinked consumers receive no platform-tenant claim; incoming copies of the header are stripped. A linked consumer key presented on a hostname bound to a different platform tenant is rejected with the same non-enumerating invalid-key response. Suspension is checked on cached hostname routes as well as cache misses; a custom-domain request may fail closed if the tenant guard's database read is unavailable.

The CLI provides the same lifecycle with `gregale platform-tenants add|apply|list|info|activation|link-consumer|link-surface|usage|suspend|resume`.

Suspension does not block anonymous traffic, independent JWT authentication, or domains and credentials that are not linked to the tenant. Configure those separately if you need a complete customer access ban. Reads and writes require the same MFA-gated account scopes as API consumer management; Free plans do not expose this feature.
