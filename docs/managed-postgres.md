# Managed PostgreSQL operator preview

Managed PostgreSQL is wired into `apid` as a dark control-plane capability. It
has no customer API or plan entitlement yet.
Keep `provisioning_enabled` false outside an isolated provider qualification
environment. The lifecycle service and background discovery both fail closed
while it is false. Deletion intents are still reconciled, so disabling rollout
cannot strand known paid resources.

The binding catalog, credential saga, and encrypted-secret ownership boundary
are durable. Reserving a binding claims one `(app, scope, environment key)`
target globally, so two databases cannot both own `DATABASE_URL`. A customer
secret write racing that reservation is serialized in PostgreSQL; exactly one
wins. Once claimed, customer PUT, rotate, and DELETE operations return a
conflict until the binding reaches its deleted tombstone. Host-key maintenance
can still re-seal the ciphertext without changing the managed owner.

The binding reconciler issues a deterministic provider identity, builds a
percent-encoded PostgreSQL URL, and seals it under the host age recipient before
marking the binding ready. The catalog stores only the provider's opaque
identity ID and an opaque deterministic secret reference. If `apid` stops after
the provider call or secret write, the expired lease is rediscovered and the
same identity and secret reference are retried. Deletion revokes the upstream
identity first, removes the owned secret, and only then writes the binding
tombstone.

Binding provisioning follows `provisioning_enabled`; binding deletion runs even
while the flag is false. A missing host age recipient or HMAC key leaves the
binding failed with a retry rather than storing plaintext or marking it ready.

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

Each app binding uses a deterministic Neon role. Repeating credential
issuance retrieves the stored role password rather than resetting it. Revoking
a binding deletes that role. The adapter returns credential material only in
memory and provider errors never include response bodies, connection strings,
endpoint hosts, or API keys.

Neon currently supports `read_write` bindings only. The sink prefers a pooled
endpoint and falls back to a direct endpoint. A `read_only` binding requires an
adapter-provided read-only endpoint and therefore fails closed as unsupported
with the initial Neon adapter. Provider-supplied root-certificate PEM also
fails closed until the portable binding contract can deliver a separate sealed
certificate file; it is never silently discarded.

Neon's consumption-history API currently maps cleanly to Gregale's
`compute_unit_seconds` and `egress_bytes` meters. Storage is intentionally not
reported yet: Neon's byte-month metric must not be mislabeled as Gregale
byte-seconds. Storage accounting and spend enforcement therefore remain preview
blockers.

## Usage collection and admission guardrails

The provider-neutral usage ledger is disabled by default. An operator may turn
it on with the `usage` block in the example configuration, after replacing the
zero-valued caps and rates with approved commercial limits. The collector
imports only complete provider windows, records them idempotently by database,
window, and meter, and currently consumes Neon's compute-unit seconds and
egress bytes. Storage byte-months remain deliberately unmetered until they can
be represented without changing Gregale's canonical units.

When enabled, a new database reservation is admitted only if the account has a
fresh usage observation and has not crossed its monthly cost, compute, or
egress ceiling. Missing or stale observations fail closed; an existing named
database remains idempotent and can still be reconciled. This is an operator
safety control, not a customer invoice or plan-entitlement API. The durable
ledger is ready for billing integration, but public plans, invoices, and usage
endpoints remain separate launch work.

The adapter contract is based on Neon's maintained
[v2 OpenAPI specification](https://neon.com/api_spec/release/v2.json). A live
qualification run against an isolated organization is still required before
enabling provisioning.
