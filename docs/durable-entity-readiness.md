# Durable entity release readiness

`GET /v1/apps/{slug}/entities/readiness` returns a read-only, schema-versioned
snapshot for an owned app. Use the normal API credential with read scope and MFA
requirements. The response is `Cache-Control: no-store`. An app outside the
durable allowlist can still inspect its blockers. It exposes no bucket names,
credentials, entity payloads, or other tenants' usage.

`configuration_ready` requires a configured conditional-storage manager, the app
allowlist entry, and shared tenant quotas. Optional capabilities have individual
`configured` or `disabled` checks. These values describe **the serving node**;
the storage manager passed startup checks, but this request does not recheck
bucket health or establish fleet consistency.

`release_ready` remains false because this endpoint has no trusted external
acceptance evidence. `source_ci`, `native_acceptance`, `quota_migration`, and
`fleet_configuration` remain `unverified`. Configuration alone must never enable
production. Review green CI for the exact candidate source and independent native
acceptance for that source and deployment configuration. Inventory/backfill old
entity reservations or use fresh scopes before quota enablement. Verify matching
quota mode, backend placement, bucket, limits and clocks across nodes, and drain
older connections. See [tenant quotas](durable-entity-tenant-quotas.md) and the
[acceptance handoff](durable-entity-release-qualification.md).

The endpoint does not probe or mutate bucket state, reserve capacity, invoke a
guest, or modify release gates. A 200 response means the report was available;
inspect its fields and checks before drawing readiness conclusions. A trusted,
source/configuration-bound external evidence verifier remains future work.
