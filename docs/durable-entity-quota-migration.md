# Existing-data quota migration

This opt-in workflow backfills conservative tenant reservations for historical
entity state. It uses the same private bucket CAS record as tenant admission.
It never deletes state, releases reservations, raises limits, or invokes guests.

## Preconditions

Keep production ingress disabled while preparing migration. Upgrade **all**
writers to a migration-aware release, use the same private strongly consistent
bucket, placement and quota limits, stop older writers and drain connections.
Older binaries and quota-disabled nodes do not honor the migration hold. Bucket
listing is not a transactional snapshot: external writer quiescence and complete,
strongly consistent delimiter listing remain operator/native acceptance duties.

On the quiesced preview deployment, configure tenant quotas and the positive
per-entity retained-byte cap, then set
`FAAS_DURABLE_ENTITY_QUOTA_MIGRATION_ENABLED=1`. The flag requires tenant quotas
and is off by default. This does not enable production or establish acceptance.

Scope is account/app/immutable environment/verified tenant. The normal entity
selectors (`namespace`, `key`, optional `environment`, `platform_tenant_id`)
resolve that scope; namespace/key do not narrow migration to one object. Every
request uses the normal owned-app API authorization and MFA policy. GET requires
read scope, POST requires deploy-write scope and normal recovery eligibility.

## Read-only planning

`GET /v1/apps/{slug}/entities/quota-migration/plan?<selectors>&cursor=<opaque>`
reads at most 16 entity prefixes and their manifests. It returns aggregate
counts, missing reservations, missing usage metadata, entities above the cap,
projected reservations and byte/entity shortfalls. It returns no entity payloads
or IDs. Follow `next_cursor` until empty, retaining the same scope.

**`page_fits` describes one page, not the whole scope.** Under quiescence, sum
`additional_bytes` and `new_reservations` across distinct pages, and add the
initial `current_reserved_bytes`/`current_reservations`. Compare the totals with
`limits.retained_bytes` and the hard 64-entity reservation ceiling. A changed
configuration or active writer invalidates that planning observation. The apply
path independently checks every page against current budgets.

Unknown usage must first be established using existing fenced per-entity
inventory/maintenance. Data above the entity cap needs a reviewed storage-policy
decision. Legacy identities without an immutable environment ID for the same app
block migration; they require explicit identity reconciliation. Corrupt, missing
or unsupported manifest/listing reads block progress rather than silently skipping
prefixes. Because hashes are bucket-wide, a bad prefix in another scope can also
block a rotation.

## Start, resume and verify

`POST /v1/apps/{slug}/entities/quota-migration/start?<selectors>` accepts:

```json
{"migration_id":"<new UUIDv4>","expected_migration_id":"","expected_revision":0}
```

The first start requires no existing migration. A replacement requires the
current UUID and revision. It preserves all reservations and fences older workers.
Repeating the same migration UUID is idempotent. Live connection leases block
start; expire or drain them first. Start holds invocation, alarm, restore,
connection and grant admission for this scope on migration-aware quota nodes.
The returned `not_before` waits the maximum entity lease plus invocation budget
(currently 5 minutes 25 seconds) before scanning, covering earlier in-flight work.

`GET /v1/apps/{slug}/entities/quota-migration?<selectors>` returns
`{"migration":null}` before start or the persisted checkpoint. Resume with:

```text
POST /v1/apps/{slug}/entities/quota-migration/step?<selectors>
```

```json
{"migration_id":"<current UUID>","expected_revision":1}
```

Each step reads the checkpoint's next page, then uses CAS to add/raise reservations
and advance the cursor **atomically**. It charges no invocation or grant issuance
counter. The first completed rotation switches `backfill` to `verify` and resets
the cursor. A second full rotation requires every observed entity to have usage
at or below its cap and a reservation covering the cap before marking `complete`.
Completion resumes admission on aware nodes. It records an observed coverage pass,
not native acceptance or a fleet acknowledgement; readiness still leaves those
external checks unverified.

Stale UUID/revision returns 409. Missing usage, over-cap data, budget shortfalls
or a verification gap returns 409 without cursor advancement or partial page
charges. Busy/drain waits, storage failures and unknown writes return 503. Inspect
status after an unknown write; it may have committed. Never assume rollback or
invent a new migration UUID to retry blindly. If policy changes or a gap requires
another pass, explicitly replace the migration using the current UUID/revision.
There is no force-complete/cancel endpoint; a blocked scope remains held until
reconciled and verified.

Do not TTL or delete `tenant-quotas/v1/`. Migration fencing, progress and retained
reservations share that authority object. Native acceptance must cover multi-node
holds, stopped legacy writers, restart/lost acknowledgements, listing completeness,
clock boundaries, policy changes and verification gaps before production release.
