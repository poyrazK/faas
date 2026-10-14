# Your first Gregale data application

Start with a working typed application, then replace its domain and policies
with your own. The local journey requires Node.js 22+, npm, a running local
Docker daemon, Gregale and an approved matching CLI/SDK bundle. It needs no
Gregale account or hosted credentials. Bundle creation and verification are
described in [Data API packaging](data-api-packaging.md).

## Create and verify

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
does not count as activation. Existing nonempty projects are refused; use
`data-api install` and `data-api dev` directly to continue.

## Use the application

Start the API in one terminal:

```sh
gregale data-api dev --port 0 --check
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
gregale data-api dev --port 0 --once --check-types --check --replay data-api.requests.json
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

Hosted Data APIs currently use enabled managed PostgreSQL databases and qualified
`data_api` bindings. This command does not connect arbitrary external databases,
create a hosted database or set up an identity provider.

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
