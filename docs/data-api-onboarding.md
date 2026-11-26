# Your first Gregale data application

Start with a working typed application, then replace its domain and policies
with your own. The local journey requires Node.js 22+, npm, a running local
Docker daemon, Gregale and an approved matching CLI/SDK bundle. It needs no
Gregale account or hosted credentials. Bundle creation and verification are
described in [Data API packaging](data-api-packaging.md).

## Create and verify

To keep the verified application running with a same-origin browser example and
local identity selector, add `--start`. It prints one private browser link; no
token copying or second server is required:

```sh
gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json --start
# Start an existing project:
gregale data-api serve --directory notes --port 0 --browser
```

Once the SDK version is published to npm, `--sdk-version 0.1.0` can replace
`--bundle`. It installs that exact public version with lifecycle scripts disabled
and records npm integrity in the client lockfile. It does not fall back to an
unpublished version. Bundle installation and its stronger CLI/SDK pairing remain
available. Node 22+, npm and a local Docker daemon are still required.

```sh
gregale data-api onboard --path notes --bundle /path/to/data-api-bundle.json
cd notes
```

The command creates the notes starter, installs checksum-verified SDK bytes,
generates TypeScript types from a restricted database login, compiles and tests
the client, and exercises CRUD, approved RPCs and Alice/Bob isolation. It uses
a disposable database and removes services when finished. It retains source,
types, an artifact pin and a client lockfile. Review and commit those files.

If a stage fails, the project remains. Fix the underlying prerequisite, artifact
or client error and use the printed recovery commands. A failed onboarding run
does not count as activation. Existing nonempty projects are refused by
`onboard`; use the integration flow below for an existing application.

## Add typed data to an existing TypeScript application

For an existing npm TypeScript project, preview the integration first:

```sh
gregale --json data-api integrate --directory ./my-app \
  --bundle /approved/data-api-bundle.json --dry-run
gregale data-api integrate --directory ./my-app \
  --bundle /approved/data-api-bundle.json
```

Use `--client-directory web` when the npm package is in `my-app/web`. The
flow supports standalone npm packages and npm workspace members, with
TypeScript 5+ declared in the client or workspace root. Other package-manager
lockfiles are refused before any writes. Once the SDK is published, an exact
`--sdk-version` can replace `--bundle`, as for onboarding.

Use `--numeric-mode exact` when integrating a client that handles large integer
IDs or precise decimals. The integration records `"numeric_mode": "exact"` in
`gregale-data/data-api.dev.json`. Generated types carry that mode and
`createAppData` uses it automatically. Local onboarding and type generation in
the default mode warn when the exposed schema includes PostgreSQL `bigint` or
`numeric`. The notes and teams demo clients use ordinary JavaScript numbers.

If installation or verification fails after setup, repeat the same integration
command with `--resume`. The command validates the existing configuration and
required setup files, then retries installation and verification without
rewriting the setup. Keep the original client directory and numeric mode.

For a bundled SDK in an npm workspace, restore the pin with the workspace root
and member selected:

```sh
gregale data-api install --restore --directory ./my-app \
  --client-directory apps/web --workspace-root . --from /path/to/data-api-bundle
```

The preview works offline and shows every new file's content in JSON, the
types destination and the install/verification commands. Applying creates
`gregale-data/` with an example migration, two-user authorization scenarios,
development configuration and instructions. It adds
`src/gregale-data/client.ts` and a separate typecheck configuration inside the
selected client. Existing application code, scripts and TypeScript configuration
are retained. Destination conflicts and symlinked integration paths fail before
installation. The SDK installer updates the client's dependency and the npm
lockfile at the workspace root when applicable. Bundle installs also retain
their artifact pin and vendored archive.

Integration starts a disposable local database and authenticated API, generates
`src/gregale-data/database.types.ts` from the database, type-checks the client,
and verifies that Alice and Bob can access their own notes while cross-user
reads, writes, deletes, forged inserts and ownership transfers fail. It checks
that the owner's data stays unchanged and removes the local services on exit.
Success is reported only after the complete verification passes.

Continue developing from the project root:

```sh
gregale data-api serve --config gregale-data/data-api.dev.json --port 0 --check
# Verify and exit; add --check-types in CI once types are committed:
gregale data-api serve --config gregale-data/data-api.dev.json --port 0 \
  --once --check --replay gregale-data/requests.json
```

Supply the generated `createAppData` factory with your Data API URL and existing
application's token provider. The local private inspector/session supplies test
identities for development; no authentication provider or existing database is
changed. Add new numbered migrations to evolve the example. Review and commit
the generated files, types, SDK pin and dependency lockfile together.

If installation or verification fails, generated files remain and the command
prints the exact install/serve commands to continue. Fix the cause and use those
commands; rerunning `integrate` refuses the existing integration. The local
session-file ignore rule is appended to the root `.gitignore`, preserving its
existing entries. Local verification does not provision hosted resources or
change preview availability.

## Add a hosted read-only client

Once `data-api connect` has produced a verified receipt, add its public-schema
client to an existing npm TypeScript application. The selected package must
have a `tsconfig.json`; `--client-directory` selects an npm workspace member.
Write a JSON array of expected rows from the selected relation to a private
assertion file. Choose stable test data and, when possible, an ordering column.
The generated read returns at most 100 rows.

```sh
export NOTES_JWT='APPLICATION_JWT'
gregale data-api integrate --hosted notes-data --receipt .gregale-data-api-notes-data.connect.json \
  --directory ./my-app --client-directory apps/web \
  --bundle /approved/data-api-bundle.json --relation notes --order-by id \
  --assertion /private/path/expected-notes.json --token-env NOTES_JWT --dry-run
# Repeat without --dry-run to install, generate, type-check and verify.
```

Use an exact `--sdk-version` instead of `--bundle` when that SDK is published.
Preview is offline and does not require the JWT to be set; apply requires a
real application JWT, the unchanged verified connection, Node.js and npm.
The assertion file is read locally and its digest is recorded; its contents,
JWT and database credentials are not written into generated source. Protect the
assertion file and any shell history used to set the JWT.

The generated `src/gregale-hosted/client.ts` accepts the API origin and the
application's own token provider. Its single read uses the selected relation,
the receipt's numeric mode and a fingerprint-pinned public-schema route. The
workflow checks the selected relation against the hosted contract, checks the
generated client with a configuration extending the app's own `tsconfig.json`,
then invokes that client with the JWT and compares the response to the supplied
array. In `number` mode, the check also rejects numeric values that would be
rounded by JavaScript; use a connection with `exact` mode for such values.
Passing this check proves that assertion only; it does not establish
complete authorization coverage. The selected package and npm lockfile change
when the SDK is installed; existing application sources and scripts are kept.

If installation, type-checking or the hosted read fails after setup, correct
the cause and rerun the same command with `--resume`. Resume requires the same
connection, selected relation, SDK source and assertion bytes, verifies that
the generated client, type-check configuration and setup metadata were not
edited, and never overwrites different generated
types. A failure during initial scaffold creation can leave empty directories;
inspect those before retrying. A changed deployment or schema requires a new
verified connection and a separately reviewed integration destination.

## Use the application

Start the API in one terminal:

```sh
gregale data-api serve --port 0 --check
```

Open its private inspector link to browse the schema and replay scenarios.
Switch between Alice and Bob to see ownership enforcement. The inspector and
private session file grant access to local test identities; do not share them.

Start the browser example in another terminal, from the project directory:

```sh
npm run build:browser --prefix client
npm run serve:browser --prefix client
```

Open `http://127.0.0.1:3030`. Read the API URL, subject and matching test JWT
from the private session file printed by dev, and enter them in the form.
Save a note with tags, then read it as that user. Switch to the other identity
and confirm it is invisible. The token is cleared after each action. Do not
use an account API key or migration credentials in the browser.

The note form validates fields before sending a request and uses an idempotent
RPC with automatic retries disabled. If a save is uncertain, keep the page open
and use **Resolve pending save** with a fresh token for the same user. Recovery
may complete the original write; retain the original key and payload and honor
the receipt-retention window. This example keeps pending state in memory only.

## Change the schema without duplicating types

While dev is running, add `migrations/sql/0011_note_label.sql`:

```sql
ALTER TABLE api.notes ADD COLUMN label text;
```

This nullable field expands the contract without requiring old clients to send
it. Dev applies the append-only migration, checks permissions and isolation,
reports compatibility, refreshes the runtime, regenerates types and reruns
client checks. The SQL column appears in the generated `Row`, `Insert` and
`Update` types. Explicit client projections still select only their requested
fields; update a projection if the application needs the label.

Stop dev before verification in a separate disposable database:

```sh
gregale data-api serve --port 0 --once --check-types --check --replay data-api.requests.json
```

Commit the migration and regenerated types together. `--check-types` refuses
stale declarations without replacing them. Add authorization and behavior
assertions when changing policies or RPC bodies: type fingerprints do not
encode either. Test receipt snapshots separately if changing idempotent RPC
response shapes; adding a field does not rewrite historical receipts.

To rehearse a breaking change, save the accepted
`.gregale/data-api-contract.json` as `schema.before.json`, then make a new
migration and run with `--baseline schema.before.json --check-breaking`.
An unapproved break fails. Restore the compatible design with another migration
or review a fingerprint-bound approval; an approval never waives stale types.
Never edit or remove an already-applied migration. See
[schema evolution](data-api.md) for hosted expand-and-contract workflows.

## Resolve a failed request

Record the SDK `onResponse` metadata or the response's `X-Request-Id`. The
callback receives request ID, status and duration, without request bodies or
tokens. For a hosted app:

```sh
gregale data-api status notes-data
gregale data-api diagnostics notes-data --request-id REQUEST_UUID
```

For saved logs, use `data-api diagnostics --file request-logs.ndjson`.
Follow the SDK error classification and owner status guidance. For a contract
mismatch, check the deployed runtime, current schema and generated types before
refreshing. For authentication errors, renew the application session. A timeout
or lost write response can occur after commit: reconcile the original operation
before issuing another write. A diagnostics report marked partial cannot prove
that no other failure occurred.

## Move to a customer database

Hosted Data APIs currently use enabled managed PostgreSQL databases with
`data_api` bindings, or `read_only` bindings for the public-schema path below.
This workflow does not connect arbitrary external databases,
create a hosted database or set up an identity provider.

For an existing managed database, `connect` offers a read-only public-schema
preview without an existing app or binding. Sign in to Gregale and run:

```sh
gregale data-api connect notes-data --database notes-db
```

The preview lists up to 1,000 public relations, their kinds, RLS and forced-RLS
flags, view options, policy counts, and table-level SELECT access for the
**provider-managed preview role**. The future API binding may have different
grants and policy behavior, and column-only grants are not captured. Review
the actual policies and view definitions separately. Catalog metadata does not
test authorization or isolation. The preview reads no customer rows and creates
no app or binding, but it may create or refresh a persistent provider-managed
read-only login and wake the database, which can incur compute charges. A
truncated preview exits unsuccessfully; use `gregale --json data-api connect ...`
for a machine-readable report. The account needs data:read and any required MFA.

On an older control plane where the backend cannot run managed preview, use
`--inspect-app notes-worker` with an existing ready binding and local `psql`;
`--inspect-env KEY` selects a binding when needed. That preview reports the
existing binding's role instead. For the apply workflow install Node.js 22 or
newer.

Prepare read checks for two real application identities and known test rows.
Write a visibility matrix such as `connect.matrix.json`, replace the sample
keys with stable test data, and review every expectation before setting its
status to `reviewed`:

```json
{"version":1,"status":"reviewed","relation":"notes","key_column":"id",
 "identities":["alice","bob"],"fixtures":[
  {"name":"alice_note","key":101,"visible_to":["alice"]},
  {"name":"bob_note","key":202,"visible_to":["bob"]}
]}
```

Generate a new request collection locally:

```sh
gregale data-api connect notes-data --prepare-checks connect.matrix.json \
  --collection connect.requests.json
```

The generator checks both identities against each fixture and emits exact
visible-row and empty-result assertions for the selected key column. Each
fixture and identity needs at least one expected visible row. A shared row can
list both identities in `visible_to`; a matrix without any hidden expectations
does not check cross-user isolation. Use a unique, readable single key column;
the generator cannot establish uniqueness or infer policy behavior. It accepts
2–20 fixtures with distinct keys: safe JSON integers or strings containing
letters, digits, `_` and `-` (up to 128 characters). Results must represent
the key with the same JSON type as the matrix. Composite keys and special string
characters require a manually authored collection. Checks expect HTTP 200 with
an empty array for an RLS-filtered read. Generation runs offline, creates a new
mode-0600 file, and does not test authorization; fixture keys are present in
both files. Prefer synthetic test rows, and review the generated collection
before applying or committing it.

Store only environment variable names in `connect.identities.json`, for example
`{"alice":"ALICE_JWT","bob":"BOB_JWT"}`, and put the two distinct
application JWTs in those environment variables. Then run:

```sh
gregale data-api connect notes-data --database notes-db \
  --apply --issuer HTTPS_ISSUER --jwks-url HTTPS_JWKS --audience AUDIENCE \
  --origins HTTPS_APPLICATION_ORIGIN --output client/src/database.types.ts \
  --collection connect.requests.json --identities connect.identities.json
```

The output directory must exist and the types file must be new. Apply checks
the collection before creating anything, inspects the database, creates a new
read-only API, exports TypeScript types, and replays the pinned collection
against the generated schema fingerprint. GET relation requests only are
allowed in this workflow. The SDK can be installed separately using the
existing application integration flow. Passing these assertions proves only
the scenarios supplied, not complete authorization coverage.

Apply creates a private `.gregale-data-api-NAME.connect.json` receipt in the
working directory. It contains resource identities, artifact paths and digests,
not JWTs, database credentials, identity mappings or request bodies. Use
`--receipt FILE` to choose another path. Keep it as a local recovery artifact.

```sh
gregale data-api connect notes-data --status
gregale data-api connect notes-data --verify \
  --collection connect.requests.json --identities connect.identities.json
gregale data-api connect notes-data --resume \
  --collection connect.requests.json --identities connect.identities.json
```

`--verify` reruns the exact recorded read-only collection with the application
JWTs. It checks the recorded app, database, binding, configuration and deployment
before and after the schema-pinned replay, and refuses changed resources or a
changed collection. It does not update the connection receipt. A changed
deployment requires the separate reviewed requalification below; it is never
adopted automatically. After input preflight, `gregale --json` emits replay events (if
replay starts) followed by a `connect_authorization_verification` event with status, completion time,
resource IDs, schema fingerprint and collection digest. A non-passing run
exits nonzero. Passing covers only the supplied scenarios at the time they ran;
it does not detect every policy, membership or subsequent change.

Status compares the recorded app, database, binding, credential generation,
deployment and app configuration review with the current control plane. A
control plane without the app release-review endpoint cannot supply that check;
resume stops instead of assuming the deployment is unchanged. A
previously passed authorization check is historical; status does not query the
database schema or repeat the assertions. Resume requires the same collection
bytes and rechecks the recorded
resources, regenerates the types, and reruns the two-subject assertions. It
refuses changed types, contracts, bindings or deployments and never redeploys
or reconfigures an app. The app configuration review does not establish that
secret values are unchanged. Remote changes can also race these observations;
coordinate deployments and configuration changes while resuming.

To requalify the same connected app after a deployment, first prepare a plan:

```sh
gregale data-api connect notes-data --requalify-plan candidate.plan.json \
  --receipt .gregale-data-api-notes-data.connect.json \
  --new-receipt candidate.receipt.json --output candidate.types.ts \
  --collection connect.requests.json --identities connect.identities.json
```

The plan checks the original recorded types, the same app, database, read-only
binding and credential generation, observes one stable serving deployment, and generates candidate
types. It may wake the app to run a contract task. The plan reports old and
new deployment, configuration, schema, types and collection digests, plus an
identity-mapping digest and plan SHA-256. Review the plan, candidate types,
configuration changes, identity mapping and authorization collection. A different collection is allowed only as the
exact reviewed bytes. Planning does not run authorization checks or create a
receipt. It writes new plan and types files; if plan creation fails after types
are written, inspect the retained types file before retrying.

Apply the exact reviewed plan with its printed digest:

```sh
gregale data-api connect notes-data --requalify-apply candidate.plan.json \
  --plan-sha256 REVIEWED_PLAN_SHA256 \
  --receipt .gregale-data-api-notes-data.connect.json \
  --collection connect.requests.json --identities connect.identities.json
```

Apply checks the original receipt, candidate identity, deployment, configuration,
endpoint, reviewed types, identity mapping and collection again. It regenerates types and runs
the read-only assertions against the candidate schema, then rechecks the
candidate before creating the new receipt. The original receipt and types are
preserved. Changed or unavailable resources, an altered plan or failed checks
stop the process. The new receipt is written only after the checks pass; the
reviewed types remain if receipt creation fails. The result establishes only
the supplied scenarios, and separate reads cannot prevent changes between
observations. Resource observations do not prove which credentials the serving
process used, that secret values are unchanged, or that every replica was tested.
A replaced app or binding, or rotated binding credential, needs
a separate recovery process rather than automatic adoption. JWT values can be
refreshed between plan and apply; the replay verifies the current tokens and
tests only the named scenarios.

If creation fails before a deployment and its binding are recorded, the receipt
reports an uncertain creation outcome and resume refuses to adopt the app by
name. Inspect any retained app and binding before using `data-api create
--resume` or other recovery commands. No resource is rolled back automatically.
A successful local command is not evidence of a separate staging run.

### Review potential write access

To inspect one existing managed database table before considering writes:

```sh
gregale --json data-api write-readiness --database notes-db --table notes \
  --operations insert,update,delete
```

This reports metadata for `public.notes` under the provider-managed preview
role. It does not create an app or binding, read table rows, or enable writes.
The preview may create or refresh a persistent read-only login and wake the
database. The future writable binding's grants, effective write policies,
triggers and side effects remain unknown. The preview role can differ from the
future binding. An incomplete catalog, missing relation or non-table relation
returns a nonzero exit. An observed table is not an authorization approval.

For an **existing** writable `data_api` binding on a deployed Data API, inspect
the task's actual database role and `api` table metadata instead:

```sh
gregale --json data-api write-readiness --database notes-db --app notes-data \
  --binding BINDING_ID --schema api --table notes \
  --operations insert,update,delete
```

The binding must be ready, use `DATABASE_URL`, have a stable credential
generation, and belong to that app and database in the default scope. The app
must have one stable serving deployment. The command starts a bounded app task
using the app's database environment; it requires deployment write and managed
PostgreSQL read access and may wake compute. The platform guest runs the fixed
inspection with a host-provided CA bundle, rather than executing a client from
the app image. The task reads catalog metadata in a read-only transaction. It returns
the observed database role, table and column privileges, RLS and ownership
flags, policy command/applicability metadata, and non-internal trigger metadata.
It does not read table rows, run policy expressions or change grants. At most
128 columns, policies and triggers are supported; a larger or unavailable
inspection fails rather than returning a partial result.

The control plane pins the task, nonce, deployment and binding generation, then
checks the scheduler's execution evidence and compares the observed database
target and trusted CA with the provider's expectation. `binding_identity.status`
is `verified` when those checks match, `mismatch` for an observed different
target, and `unknown` when evidence is unavailable, stale or changed. The
identity result is point-in-time and expires after five minutes. A control-plane
selection mismatch stops the command before starting a task. The selected-binding
command still returns `review_required` and a nonzero exit even when the identity
is verified and the catalog shows no findings: it does not test row authorization.
The report also cannot establish policy behavior,
helper-function or trigger effects, sequence privileges, all replica
configuration or tenant isolation. Review those separately. An inspection result
is not an approval; grant and RLS risks appear in `findings`, and unavailable or
changed observations also exit nonzero. The command does not
compare a local baseline or inspect a not-yet-created binding.

For a local qualification, create a separate Data API project with reviewed
migrations and policies representative of the intended database, a synthetic SQL
fixture and a reviewed version 4 mutation manifest. Export a scaffold from the
local inspector and review its fixtures and expectations as described in
[guided mutation checks](data-api.md#guided-mutation-checks-in-disposable-verification).
For every requested operation, the manifest must include an allowed write and a
denied write by a different local subject against the same named fixture. Both
subjects must also appear in the read baseline. All mutation checks must target
the selected table and requested operations.

```sh
gregale --json data-api write-check --directory ./local-data-api \
  --schema api --table notes --operations insert,update,delete \
  --access-checks data-api.mutations.json --fixture fixture.sql
```

This uses the existing disposable PostgreSQL verifier and its private
before-and-after state observer. It fails on an unreviewed or incomplete
manifest, an unexpected write, changed state after a denial, a stale contract,
or failed cleanup. Reports identify the local schema fingerprint, normalized
manifest and fixture digests, supplied operations and check outcomes; they omit fixture rows
and credentials. The local verifier exposes the `api` schema, so testing a
copy of `public.notes` requires a separately reviewed local equivalent. A local
pass covers only the supplied local scenarios; it does not prove that the
hosted schema, role, grants, policies, identities or side effects match, nor
does it authorize or enable hosted writes. Fixtures and migrations are trusted
local code; use synthetic data and review them before execution.

Follow the starter README to create the managed database, deploy its separate
migration app and configure its migration binding. Choose an identity provider
and configure the issuer, JWKS URL and audience before creating the serving API:

```sh
gregale data-api create notes-data --database DATABASE --issuer HTTPS_ISSUER --jwks-url HTTPS_JWKS --audience AUDIENCE --origins HTTPS_APPLICATION_ORIGIN
```

The supported create workflow exposes `api`; additional-schema exposure requires
the reviewed runtime configuration and schema permissions described in the
[Data API guide](data-api.md). Keep owner credentials in the migration binding
and application JWTs in session management. Apply reviewed RLS policies and
explicit grants. Generate hosted types and run the starter's sync workflow with
an application JWT injected through its configured environment variable.

Replay the collection with two actual application subjects through
`data-api replay`, using an identities file that names token environment
variables. Adapt subject-dependent bodies to the pilot identities first. Local
Alice/Bob tokens do not work on a hosted API. Hosted replay executes writes;
use approved test data. An enabled staging environment is required before
claiming hosted readiness.

## Pilot success criteria

Run the journey with a customer building a small tenant-scoped dashboard.
The [team orders starter](../examples/data-api-teams/README.md) provides a typed
dashboard with authoritative database membership, owner/admin writes, read-only
members and two-team authorization scenarios. Use `--template data-api-teams`
with onboarding. The older orders project remains a subject-isolated example.

| Evidence | What to record |
| --- | --- |
| First useful result | Time from starting setup to a successful authenticated read and write |
| Setup friction | Failed stage, prerequisite and time to recover; no credentials or payloads |
| Access isolation | Two real subjects cannot read, edit or delete each other's data; anonymous access fails |
| Schema evolution | Add a field, regenerate types, compile and check compatibility in CI |
| Recovery | Correlate a failed request; reconcile an uncertain save without duplicating it |
| Hosted qualification | Run the same checks on the enabled provider, region and deployed runtime |

Aim for a first working application within ten minutes once prerequisites and
artifacts are available. This is a pilot target, not a measured result. Record
prerequisite setup separately and count abandoned or failed sessions when
assessing activation. The CLI reports stage durations and overall
`local_activation` duration; it does not send telemetry. Collect consented pilot
observations without copying private inspector links, session files, JWTs,
database credentials, subjects or customer records into reports.
