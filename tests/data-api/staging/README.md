# Data API staging canary

This opt-in gate deploys disposable customer fixtures through the normal Gregale
CLI, remote builder, managed bindings, release tasks and public HTTPS edge.
It does not produce a provider qualification approval. Run it only after the
exact managed PostgreSQL backend (including `data_api_enabled: true`) has passed
the existing qualification and staging provisioning gates.

The target must use an already-qualified native VM fleet. The canary verifies
build provenance against the operator's native builder node allowlist and records
deployment/build/source identities. Native VM host qualification remains the
existing metal gate; this HTTP canary does not attest the host hardware itself.

## Configure an isolated target

Provide an operator-owned JSON manifest. Values are not credentials:

```json
{
  "environment": "staging",
  "api": "https://api.staging.example.com",
  "account_id": "ISOLATED_CANARY_ACCOUNT_ID",
  "region": "eu",
  "builder_node_ids": ["QUALIFIED_NATIVE_BUILDER_NODE_ID"],
  "app_host_suffix": ".staging.example.com"
}
```

Use an account with capacity for three app workloads, one managed database,
PostgreSQL egress, app tasks and migration release tasks. Supply its bearer as
`FAAS_TOKEN` through the secure environment. The account ID must match the
authenticated account before anything is created. There is no default API,
region, account or builder allowlist. An explicit `FAAS_API` must match the
manifest. The manifest must designate staging.

Fixture apps use an open edge: the issuer serves public JWKS, the migration app
serves a credential-free health endpoint, and the Data API verifies application
JWTs in its runtime. Paid-plan owner-key defaults must not intercept those JWTs.

```sh
export FAAS_DATA_API_STAGING_CONFIG=/etc/faas/data-api-staging.json
export FAAS_DATA_API_EVIDENCE=/var/tmp/data-api-canary/evidence.json
make data-api-staging-canary
```

The wrapper first checks configuration without provider calls, builds the CLI
from the clean checkout, and builds/packs the restricted client SDK locally.
It sends customer source to Gregale for image builds; it never runs a local
customer Docker build. No npm publication occurs.

## Evidence and resource ownership

The gate creates a uniquely named database, a public-key-only JWT issuer app,
a migration app, and the Data API app. The JWT signing key stays in controller
memory. Migration credentials stay in release tasks. Generated types are
exported through the CLI task path and compiled with positive and negative
TypeScript examples against an installed SDK tarball.

Checks cover typed CRUD, signed SQL-role override, two-subject RLS reads/writes,
wrong-audience rejection, authenticated OpenAPI, observed parking followed by an
authenticated wake, a second schema migration, completed fresh restart, updated
type/REST contracts, and credential rotation preserving data.
Refresh completion and readiness are verified by `gregale data-api refresh --wait`.

Evidence is atomically written with mode 0600 and contains stable check names,
deployment/build/source and parked/running instance identities, and cleanup
status, without JWTs, DSNs, provider error bodies or child
command output. Wake duration is public request elapsed time, not a snapshot
restore SLO measurement. A failed check or cleanup yields a nonzero exit.

Cleanup is attempted on success, ordinary failures and SIGINT/SIGTERM. Bindings
are retired before their apps and provider database. Only acknowledged
resource identities from this run are deleted. Unknown creation outcomes and
cleanup failures are recorded for operator reconciliation. A killed runner or
host outage can leave resources: use the persisted resource journal to inspect
and remove them through normal app/database commands. Never adopt or delete a
different resource by matching its display name.

## GitHub Actions

`Data API staging canary` is manual and main-only, runs on the dedicated
`gregale-data-api-staging` self-hosted runner, and uses the `data-api-staging`
environment. Configure `GREGALE_DATA_API_STAGING_CONFIG_JSON` as an environment
variable and `GREGALE_DATA_API_STAGING_TOKEN` as its secret. Run it only after
reviewing the target manifest and the existing qualification evidence.

`make data-api-staging-check` exercises the harness refusal, credential handling,
restart completion, ownership and cleanup contracts without staging access.
Portable CI results are not live canary evidence.
