# Platform tenants

A platform tenant represents one of your customers across multiple Gregale apps. It complements the app-local API consumer and tenant-surface resources; it does not replace either one.

Create the account-level identity once:

```http
POST /v1/account/platform-tenants
Content-Type: application/json

{"external_ref":"customer-42","name":"Customer 42"}
```

Repeating that request with the same name returns the existing tenant. Link each app's existing consumer with `POST /v1/account/platform-tenants/{id}/consumers` and `{"consumer_id":"…"}`. Link an existing tenant surface with `POST /v1/account/platform-tenants/{id}/surfaces` and `{"surface_id":"…"}`. A consumer or surface cannot belong to two platform tenants, and cross-account IDs return 404. Unlinked resources keep their current behavior.

`GET /v1/account/platform-tenants/{id}` shows the linked consumers and surfaces. `GET /v1/account/platform-tenants?limit=100&offset=0` pages the registry. `GET /v1/account/platform-tenants/{id}/usage?since=…&until=…` sums the existing durable request, error, and billable-unit facts across linked consumers and groups the output by UTC day, app, and consumer. This is raw usage, not an invoice or a cross-app price quote.

To temporarily stop the linked credential and hostname paths, send `PATCH /v1/account/platform-tenants/{id}` with `{"status":"suspended"}`. New keys cannot be issued for its linked consumers while suspended. Linked hostnames are blocked when tenant-surface routing is enabled. Send `{"status":"active"}` to resume. Existing keys are not revoked or rotated by either transition.

The CLI provides the same lifecycle with `gregale platform-tenants add|list|info|link-consumer|link-surface|usage|suspend|resume`.

Suspension does not block anonymous traffic, independent JWT authentication, or domains and credentials that are not linked to the tenant. Configure those separately if you need a complete customer access ban. Reads and writes require the same MFA-gated account scopes as API consumer management; Free plans do not expose this feature.
