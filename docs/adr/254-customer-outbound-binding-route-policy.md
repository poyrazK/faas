# ADR-254: Customer narrowing of outbound binding routes

- **Status:** accepted
- **Date:** 2026-09-25
- **Decision:** A customer may set nonempty, canonical HTTP method and path-prefix subsets on an existing app binding with an MFA-gated, deploy-write PATCH. The integration's configured methods and paths remain the maximum. The API validates a requested subset against the current ceiling while holding the integration and binding rows; outboundd applies the intersection again on every request, before admission or credential use. Existing bindings with NULL route arrays retain the full integration ceiling, and idempotent bind does not overwrite an explicit subset.
- **Ownership:** `apid` writes only the customer-owned binding row. `outboundd` retains the integration origin, enabled state, credential source, and maximum route policy. Operator app attachments remain independent; if an app is attached by both routes, the operator attachment grants the full operator ceiling.
- **Limits:** This is explicit gateway routing, not transparent interception. The route guard does not interpret query parameters or request bodies and does not replace provider-side authorization. Customer-created fixed origins are enabled by ADR-256 after destination-publicness and DNS/rebinding defenses in ADR-255. This change does not add cost budgets, caching, or retries.
