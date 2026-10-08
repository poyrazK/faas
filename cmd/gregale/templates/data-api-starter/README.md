# Data API starter

Versioned PostgreSQL migrations, a typed notes client, two-user row-level
security, a migration-to-client workflow and application CI.

```sh
gregale init --template data-api-starter --path notes
cd notes
```

The root deploys an ordinary migration app. Its release task applies SQL before
activation; its serving process only answers `/healthz` and never opens a
database connection. The generated Data API is a separate app. The client,
owner tools and CI files are excluded from the migration app's upload.

## Install the client

Use Node.js 22 or newer. `@gregale/data` is not published to npm. Build its
tarball from an authorized Gregale checkout:

```sh
# In /path/to/faas/sdk/data:
npm ci --ignore-scripts
npm run build
npm pack
# Back in the initialized notes directory:
node tools/install-sdk.mjs /path/to/faas/sdk/data/gregale-data-0.1.0.tgz
npm run typecheck --prefix client
npm test --prefix client
```

The installer copies the artifact to `client/vendor/gregale-data.tgz`, installs
it without lifecycle scripts and updates the client lockfile. Include that
authorized artifact and the lockfile in your application repository, or arrange
for CI to fetch the same artifact before `npm ci`. Public SDK distribution
remains subject to Gregale's SDK publishing policy. This starter does not
publish a package. Root migration dependencies have their own lockfile.

## Configure and deploy

Use a ready, qualified managed database such as `notes-db`, an account/region
enabled for Data API and release tasks, and a plan with capacity for both apps
and PostgreSQL egress. Use an isolated database: the first migration creates
`api.notes` and does not adopt an existing table. Use your application's HTTPS JWT issuer, JWKS endpoint
and audience. No identity provider or database administrator key is bundled.
Keep credentials in your environment or secret store.

Reserve the migration app and attach its release-only connection:

```sh
gregale deploy --create-only --path . --dockerfile --name notes-migrations
gregale postgres attach notes-db notes-migrations --access migration --env MIGRATION_DATABASE_URL
# Use the binding ID from the attachment receipt. Wait for state "ready".
gregale postgres bindings get <binding-id>
gregale app notes-migrations egress-ports add 5432
gregale deploy --name notes-migrations --dockerfile --wait --timeout 15m
```

Create the API on the same database, then export its actual schema and validate
the client. Substitute your issuer, JWKS URL and audience:

```sh
gregale data-api create notes-data --database notes-db \
  --issuer https://identity.example.com/ \
  --jwks-url https://identity.example.com/.well-known/jwks.json \
  --audience notes
gregale data-api sync notes-data --config data-api.json
```

If you rename the migration app, update `data-api.json`. Its migration command
deploys the current working tree and waits for release success and deployment
readiness. Sync then verifies a fresh
API restart, writes `client/src/database.types.ts` atomically and runs the client
type check. The committed types are generated from this starter's migrations;
the first sync replaces them with your live database contract.

## Use application sessions

`client/src/notes.ts` provides typed list, create, update and delete operations:

```ts
import { notesClient } from './notes.js'

const notes = notesClient({
  url: 'https://notes-data.gregale.dev',
  subject: session.user.id,
  accessToken: () => session.getAccessToken(),
})
await notes.create('Hello', 1)
const { data, error } = await notes.list()
```

The token getter runs on every request. Use the identity provider's exact `sub`
as `subject`. PostgreSQL checks the token subject on every read and write;
supplying a different subject cannot grant access to someone else's data. API
tokens require `sub`, `exp`, the configured issuer/audience, and RS256 or ES256.
Owner keys are for management and type export, never for the application client.

For the runnable example, configure `DATA_API_URL`, `DATA_API_SUBJECT` and
`DATA_API_TOKEN` in your environment, then run:

```sh
npm run example --prefix client
```

For a two-user check, configure `DATA_API_URL`, `DATA_API_USER_A_SUBJECT`,
`DATA_API_USER_B_SUBJECT`, `DATA_API_USER_A_TOKEN` and `DATA_API_USER_B_TOKEN`.
Use two distinct, unexpired application sessions in your preview environment:

```sh
npm run test:authorization --prefix client
```

This check performs both users' CRUD operations, rejects cross-user access and
cleans up only notes whose IDs it created. Missing sessions fail the check;
they do not silently skip it.

## Evolve the schema

Add `migrations/sql/0003_description.sql` and run sync again. Numbered SQL files
are applied under a transaction and database advisory lock. The private
`gregale_migrations.applied` ledger records versions and SHA-256 checksums;
already-applied SQL cannot be edited, renamed, removed or inserted out of order.
Use plain SQL without transaction-control statements; the runner owns the
transaction, so operations requiring execution outside a transaction need a
separate migration approach. All pending migrations commit together. Failed
release tasks keep the previous deployment serving, but committed database
changes are not undone by rollback.
Use changes compatible with existing clients and serialize migration/sync jobs
for each database so refresh and export observe the same schema.

`api.notes` has RLS enabled before its transaction commits. Its policy checks
the original JWT `sub` for both visibility and writes. The managed Data API
login receives current/future table grants from the stable migration owner; the
ledger schema receives no Data API grants. Do not move the ledger into `api`.

## Application CI

`gregale init` writes two workflows into `.github/workflows`:

- `data-api-client.yml` compiles positive/negative type assertions and runs
  client tests on pull requests and main pushes. It uses the locked SDK tarball
  and needs no account credential or application JWT.
- `data-api-preview.yml` is manually dispatched from main. It runs the client
  checks, verifies live types with `data-api types --check`, and tests two-user
  authorization. It never runs migrations, sync or refresh.

Configure the `data-api-preview` GitHub environment with required reviewers and
deployment branches restricted to main. Add environment variables:

| Variable | Value |
| --- | --- |
| `GREGALE_CLI_VERSION` | Pinned CLI release tag with Data API commands |
| `GREGALE_API` | Preview management API URL |
| `DATA_API_APP` | Data API app slug, such as `notes-data` |
| `DATA_API_URL` | Public HTTPS Data API URL |
| `DATA_API_USER_A_SUBJECT`, `DATA_API_USER_B_SUBJECT` | Two distinct JWT subjects |

Add secrets `GREGALE_OWNER_TOKEN`, `DATA_API_USER_A_TOKEN` and
`DATA_API_USER_B_TOKEN` to that protected environment. The owner key is passed
only to the drift-check step; the RLS step receives application sessions. Keep
sessions current and use an isolated preview database. The CLI installer pins
the requested release and verifies its published archive checksum. If the
needed CLI has not been released yet, run these commands with a locally built
CLI until packaging is available.

The visible `ci/` files are the embedded workflow sources; the initialized
`.github/workflows/` copies are your application workflows. See the
[Data API guide](https://gregale.dev/docs/data-api) for preview enablement,
runtime bounds and staging qualification.
