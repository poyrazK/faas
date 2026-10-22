# Durable entity release readiness

`GET /v1/apps/{slug}/entities/readiness` returns a read-only, schema-versioned
snapshot for an owned app. Use the normal API credential with read scope and MFA
requirements. The response is `Cache-Control: no-store`. The preview is hidden
from customers: while `FAAS_DURABLE_ENTITIES_ENABLED` is off, and for apps
outside the durable allowlist, this route (like every `/entities` route) answers
`404 not_found` "no such API route". Operators confirm the allowlist from node
configuration. It exposes no bucket names, credentials, entity payloads, or
other tenants' usage.

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

The optional [quota migration workflow](durable-entity-quota-migration.md) reports
scope progress separately. A completed coverage pass does not verify fleet
quiescence or native acceptance, so this report retains its external blockers.
The optional [quota policy workflow](durable-entity-quota-policy.md) reports its
configuration separately and preserves those external blockers as well.
The optional [retirement workflow](durable-entity-retirement.md) also preserves
those blockers; completed cleanup is not native fleet acceptance.

## Qualification

[CI run 38142483672](https://github.com/poyrazK/faas/actions/runs/38142483672)
passed for source commit `d5f54813de3480452370e42258a023dc5ff23d1d`.
It passed the Node build, 16 focused SDK tests, packed-starter acceptance, four
Go durable packages under the race detector, focused CLI checks, and 21 top-level
API tests under the race detector. The API tests include read-only readiness,
authentication, disabled-app blockers and absence of inferred acceptance.

Two earlier runs ended with exit 143 during cold API compilation, without a test
result. Compilation progress diagnostics showed memory above 14 GiB. The focused
workflow now uses the repository's established API compiler settings: serial
compiler work, disabled debug metadata, and bounded GC, while retaining normal
optimization and race coverage. The qualified API suite completed in 2.560s.

No local tests/builds or native acceptance were run. No PR was created, and no
production gate was enabled. Independent native acceptance, quota migration and
fleet configuration review remain outstanding.
