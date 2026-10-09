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
wrong-audience rejection, authenticated OpenAPI and serving metadata, observed
parking followed by an authenticated wake, RPC permissions and subject isolation,
three sync runs, updated type/REST contracts, SDK/runtime-log correlation, and
credential rotation preserving data and RPC access. Sync verifies fresh restart
completion, readiness and serving/exported fingerprint agreement.

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

## Complete workflow coverage

The fixture bootstraps notes and an opted-in `create_note` RPC through a migration
release before the serving binding exists. RPC execution is revoked from PUBLIC.
After creating the Data API, the gate verifies the RPC returns 404, then changes
the migration release to the starter's canonical owner permission workflow.
The shared catalog/signature parser is copied from the runtime, not maintained
as a second implementation. An unannotated `private_note` function remains
ungranted and absent from RPC routes, generated types and OpenAPI.

The gate runs `data-api sync` three times: initial RPC permission setup, the
priority-column migration, and permission setup for the replacement serving
role after `rotate --wait` completes. Every sync must return a completed wake,
type task/deployment identifiers, a fingerprint matching the generated file and
`contract_verified: true`. The canary also probes the authenticated startup
fingerprint directly, rejects an unauthenticated probe, compiles positive and
negative RPC examples, and exercises RPC-derived subjects and two-user isolation
both before and after rotation. A 30-minute application JWT is supplied only in
the child sync environment for its 20-minute deadline; values never appear in
workflow JSON, argv or evidence. Regular HTTP calls use freshly minted tokens.

Each sync's migration deployment is checked against native builder/source
provenance, even though its deploy receipt is nested inside the workflow. SDK
`onResponse` IDs from an actual read and RPC calls must match safe JSON runtime
records obtained through the normal runtime-log command. Records must belong to
the acknowledged serving deployment and match method, route and status. Logs are
polled briefly for delivery. Missing IDs/records, mismatched metadata, extra
log fields, stale fingerprints and post-rotation RPC failures cannot pass.

Completed rotation must increment the acknowledged binding's credential generation
and retire its pending state; the generations are recorded in `rotation`.
Evidence adds `syncs` (identifiers and fingerprints) and `requests` (request,
deployment and instance identifiers plus method/route/status). Raw runtime lines,
client bodies, tokens and SQL-role names are never persisted. Existing cleanup
and unknown-outcome rules still apply. The harness's simulated complete journey
and its failure tests verify ordering and cleanup locally; real PostgreSQL tests
verify fixture migrations, grants, generated RPC types and replacement roles.
Neither portable result counts as live staging or native provider qualification.
