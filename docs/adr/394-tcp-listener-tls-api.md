# ADR-394 · Customer TCP listener TLS intent

Status: proposed

## Decision

Expose normalized TLS mode and hostname on TCP listener create, read, and update APIs. Termination creation starts disabled. Require a currently verified app-wide domain owned by the listener app on creation, policy changes, and enable actions. An environment-scoped domain cannot authorize an app-wide listener. Accept exactly one policy-change or enable/disable mutation per update; policy changes atomically disable the endpoint in the store.

Keep the canonical OpenAPI schema and embedded copy synchronized. Generate matching Node and Python client types. Adapt only the pinned Python generator's known required-field-only `oneOf` limitation so it retains its existing typed update request; the canonical schema and apid enforce the mutation constraint. Never include certificate material or provider paths in customer intent.

This API must be delivered with public-edge runtime enforcement of the same TLS policy and domain ownership. Keep the extraction unpublished until runtime changes are in its dependency chain. Native Linux/amd64 qualification and certificate provisioning remain separate requirements; persistent guest disks remain excluded.

## Validation

API race tests cover unverified/foreign/revoked domains, disabled creation, invalid and ambiguous updates, enablement, duplicate identity conflicts, and disable-on-policy-change. Domain validation tests cover environment scope and empty/mismatched identities. Schema parity and scoped Go lint pass. Node and Python generated SDK tests exercise creation and policy updates over real local HTTP transport. OpenAPI lint passes with the existing repository warnings.
