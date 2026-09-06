# Managed PostgreSQL operator preview

Managed PostgreSQL is wired into `apid` as a dark control-plane capability. It
has no customer API, plan entitlement, or automatic credential binding yet.
Keep `provisioning_enabled` false outside an isolated provider qualification
environment. The lifecycle service and background discovery both fail closed
while it is false. Deletion intents are still reconciled, so disabling rollout
cannot strand known paid resources.

## Neon backend configuration

Copy `deploy/managed-postgres.example.json` to an operator-owned path, set
`FAAS_MANAGED_POSTGRES_CONFIG` to that path, and expose the API key through the
environment variable named by `secret_env.api-key`. The API key itself must
never appear in the JSON file.

`namespace` is the immutable Neon organization ID. `region` is Gregale's
portable region name; `settings.region_id` is the current Neon placement for
that region. Existing database rows contain a fingerprint of this non-secret
placement. Changing the organization, physical region, or limits fences those
rows instead of silently moving provider operations to a new destination.

The adapter requires explicit `max_storage_bytes` and
`max_restore_window_seconds` settings. Set them to promises supported by the
Neon organization plan. A value of `0` disables the point-in-time-restore
promise. The storage ceiling must be positive.

The initial service-class mapping is:

| Gregale class | Neon autoscaling range |
| --- | ---: |
| `development` | 0.25–1 CU |
| `burstable` | 0.25–2 CU |
| `production` | 1–4 CU |

Only `single_zone` is advertised. Gregale does not claim a portable
high-availability promise merely because Neon storage has internal redundancy.
`scale_to_zero=true` uses a 300-second suspend timeout; false disables compute
suspension. PostgreSQL majors 14 through 18 are enabled.

## Safety boundary

Neon's create-project operation is non-idempotent. The adapter never retries
that POST at the HTTP layer. It assigns a deterministic, hashed project name,
discovers that exact name before creation, and returns the Neon project ID as
soon as the POST is accepted so the durable reconciler can use `Inspect` from
then on. If the create response is lost, it performs discovery without issuing
a second POST. Deletion also searches by Gregale's stable resource ID when the
opaque Neon ID was never persisted, closing the ambiguous-create cleanup path.
Ambiguous duplicate names fail closed.

Each app binding will use a deterministic Neon role. Repeating credential
issuance retrieves the stored role password rather than resetting it. Revoking
a binding deletes that role. The adapter returns credential material only in
memory and provider errors never include response bodies, connection strings,
endpoint hosts, or API keys.

Neon's consumption-history API currently maps cleanly to Gregale's
`compute_unit_seconds` and `egress_bytes` meters. Storage is intentionally not
reported yet: Neon's byte-month metric must not be mislabeled as Gregale
byte-seconds. Storage accounting and spend enforcement therefore remain preview
blockers.

The adapter contract is based on Neon's maintained
[v2 OpenAPI specification](https://neon.com/api_spec/release/v2.json). A live
qualification run against an isolated organization is still required before
enabling provisioning.
