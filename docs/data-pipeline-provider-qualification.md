# Staging data pipeline provider qualification

Run [the qualification runner](../tests/data-pipelines/provider_qualification.py)
to collect real-provider evidence for the PostgreSQL mirror flow. Unit tests and
local native replication acceptance do not qualify Neon. No live-provider result
has been collected in this workspace: staging credentials, identities and
provider fault controls are not configured.

The runner creates a fresh pipeline, exercises the existing signed-read canary,
then tests credential rotation, worker restart, source and destination outages,
capacity admission denial, retention pressure and cleanup. Each phase must pass;
missing phases remain `not_run` and cannot yield a passing report. Every report
keeps `production_ready: false`, `pilot_ready: false` and operator review required.
A report does not change any feature flag, configuration, PR or deployment.

## Prepare an isolated fixture

Use an approved staging account and a disposable PostgreSQL 16 source database.
It must have no publications or replication slots before the run, and only the
one-row `public.pipeline_canary` fixture described in
[data pipelines](data-pipelines.md#live-staging-canary). Its native WAL retention
must be 1 GiB for this drill. Keep unrelated workloads out of the fixture.

Register the source connection and prepare two empty managed mirrors: the main
mirror and a distinct spare for admission-denial checks. Qualify their physical
allocations using the existing capacity configuration. The spare must share an
admission constraint with the main pipeline, such as their shared source scope.
Initially there must be enough capacity for a prospective pipeline on the spare.
The adapter's temporary capacity workload must then consume that headroom through
real admitted reservations. Changing a JSON result or reporting a compatibility
failure does not exercise the atomic admission path.

Deploy the read-only Data API against the main mirror before creating the
pipeline. Configure `data_api_refresh` to refresh that dedicated API after the
initial copy creates its tables; an empty pre-existing PostgREST schema cache
cannot discover the new tables by itself. Supply valid Auth JWTs for a trusted reader and an untrusted subject
with the same issuer/audience. Tokens must remain valid for the run; before each
fault the runner requires at least `timeout + 90 seconds` of remaining lifetime.
Supply a control API bearer token for the same account used by the authenticated
`gregale` CLI. Keep URLs and tokens in user-owned private files (0600), without
symlinks. The original and rotated source URLs must identify the same
host/port/user/database, use `sslmode=verify-full`, and differ in password.

The runner needs the current `gregale` binary (`GREGALE_BINARY` may select it), a
standard PostgreSQL 16 `psql`, and HTTPS/TLS connectivity from the runner and the
native subscriber. Do not use a fixture-specific `psql` wrapper. Credentials are
passed through private files or process environment, never command arguments.

## Configure the deployment fault adapter

Fault controls depend on where the provider and staging workers run. There are
**no default host, systemd, firewall or provider mutation commands**. Supply a
reviewed adapter, or install the supplied
[fixed-argv adapter](../tests/data-pipelines/provider_fault_adapter.py) with an
explicit [command manifest](../tests/data-pipelines/provider_adapter.example.json):

```sh
qualification_dir=$(mktemp -d)
chmod 700 "$qualification_dir"
install -m 700 tests/data-pipelines/provider_fault_adapter.py "$qualification_dir/adapter.py"
install -m 600 tests/data-pipelines/provider_qualification.py "$qualification_dir/provider_qualification.py"
install -m 600 tests/data-pipelines/staging_canary.py "$qualification_dir/staging_canary.py"
install -m 600 tests/data-pipelines/provider_adapter.example.json "$qualification_dir/controls.json"
# Replace placeholder IDs, fingerprint and command paths with reviewed staging values.
export PIPELINE_QUALIFICATION_ADAPTER_CONFIG="$qualification_dir/controls.json"
```

The manifest binds commands to the exact source connection, main mirror, spare
mirror, Data API URL and backend fingerprint. Every action uses fixed argv without implicit
shell expansion. The request is JSON on stdin, containing version 1, run ID,
nonce, action, scoped IDs, backend fingerprint, operation timeout and WAL budget.
Commands must enforce those scopes, close existing sessions when disconnecting,
and return a small JSON object; exit zero means the requested operation completed.
Keep secrets out of argv, stdout and the manifest. Store provider credentials in
the deployment's existing secure mechanism.

The reference adapter returns only typed evidence fields, echoes the request's
run ID/nonce/action/fingerprint, and records a manifest hash. A custom adapter
must implement the same receipt protocol, including `applied: true` and
`control_manifest_sha256` in every receipt. The runner pins the preflight manifest
hash in later requests; the reference adapter rejects a changed manifest before
executing any command. Adapter acknowledgements alone do not
pass the drills: pipeline state, source SQL and signed reads are checked too.

| Action | Required behavior/evidence |
| --- | --- |
| `preflight` | Read-only checks; `staging`, `isolated_fixture`, `shared_admission_scope` true; `source_retention_bytes` 1073741824; `provider_kind` neon; actual `target_postgres_version` in the 160000–169999 range. All restore/audit commands must be configured. |
| `data_api_refresh` | Refresh the dedicated Data API after initial copy using its supported coordinated schema-refresh path. Signed-row checks must succeed afterward. |
| `credential_rotate` / `credential_restore` | Change this source role to the prepared password / restore its original password. The runner proves the new login works, the old login fails, rotates Gregale's sealed credential, forces reconnect and verifies CDC. |
| `worker_stop` / `worker_start` | Stop / restart only the approved staging pipeline workers. Keep the control API and native subscription running. The runner observes monitoring expiry after at least 90 seconds while CDC continues. |
| `source_disconnect` / `source_reconnect` | Disconnect native subscriber and worker source SQL paths / restore them. Keep the runner's source SQL connection and the read API reachable. Disconnect must close existing native sessions, including when used briefly after credential rotation. |
| `destination_disconnect` / `destination_reconnect` | Disconnect the dedicated mirror from workers, native apply and Data API / restore it. Keep source SQL and the control API reachable. The runner requires fresh blocked status and a 502/503/504 or transport outage from signed reads. |
| `capacity_fill` / `capacity_release` | Add / fully remove bounded fixture-only admitted reservations that consume shared capacity. Keep fresh capacity observations available. A direct create request must return 409 `data_pipeline_insufficient_capacity`; an unknown/stale-capacity error fails the drill. |
| `audit_capacity` | Read-only probe returning integer `reservation_count` for the isolated shared scope. The count must remain unchanged across rejected admission. |
| `retention_fill` / `retention_release` | Prevent only native apply from advancing while retaining worker SQL access; generate bounded source WAL until the existing 80% pressure warning appears, then restore apply. Respect `max_retained_wal_bytes` (1 GiB minus 32 MiB), operation timeout and storage envelope. Never drop/invalidate the slot. |
| `audit_cleanup` | Read-only probes returning integer `target_subscription_count`, `pipeline_reservation_count`, `fault_resource_count`, all zero after deletion. Scope subscription and reservation probes to this run; include all generated helper resources in the fault count. |

Implement fault controls with independent bounds and recovery in the staging
infrastructure. The runner attempts the matching restore even if fault entry
fails or times out, and on ordinary interruption. It cannot perform restoration
after a forced process kill or host loss. `active_faults` and `restore_failures`
identify controls requiring manual recovery. Restore commands must be idempotent
for the same run ID and nonce. Do not delete diagnostic resources while a fault
remains active.

## Execute and review

First check local prerequisites. This mode invokes neither the CLI nor adapter,
and performs no network requests:

```sh
export FAAS_ENVIRONMENT=staging
export FAAS_API=https://your-staging-control-api.example
python3 tests/data-pipelines/provider_qualification.py --check-config \
  --name pipeline-canary-unique-run \
  --connection SOURCE_CONNECTION_ID \
  --database EMPTY_MAIN_MIRROR_ID \
  --admission-database EMPTY_SPARE_MIRROR_ID \
  --backend-fingerprint BACKEND_SHA256 \
  --adapter "$qualification_dir/adapter.py" \
  --source-url-file ./source-url.txt \
  --rotated-source-url-file ./rotated-source-url.txt \
  --reader-token-file ./reader.jwt \
  --other-token-file ./other-subject.jwt \
  --control-token-file ./control.token \
  --data-api-url https://your-canary-data-api.example \
  --report ./qualification-config.json
```

Remove `--check-config` and choose a **new report filename** to execute the live
run. Use an unused pipeline name for each run. Output reports are reserved with
mode 0600 before remote actions and cannot overwrite existing files or symlinks.
A provisional report retains the candidate name and mirror identity if the
process terminates unexpectedly. The final report includes phases, blockers,
resource IDs, observed PostgreSQL versions, backend fingerprint, adapter and
manifest hashes, and harness/CLI hashes for review.

A failure preserves resources and records sanitized codes, never remote stderr,
credentials or raw provider responses. If an admission request unexpectedly
creates a pipeline, its returned ID is recorded as `unexpected_pipeline_id`.
Locate a lost creation acknowledgement by the unique candidate name before
retrying. Any unexpected `resync_required` fails the drill; the runner never
requests destructive resync automatically.

Successful cleanup independently checks **all** source slots in the disposable
database (including native `pg_*` table-sync slots) and publications, then checks
the adapter's scoped target subscription and durable reservation counts before
deleting the source connection. The main mirror, spare mirror, Auth app, Data
API and source fixture remain for inspection and separate removal.

Review a passing report against the actual deployment, capacity allocation and
fault-control implementation before deciding on a customer pilot. Keep
production disabled and the existing PR closed during qualification.
