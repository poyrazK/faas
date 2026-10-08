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

Use Node.js 22 or newer, npm, curl and tar. Obtain an approved Data API bundle,
or build one from a committed, authorized Gregale checkout with its pinned Go
toolchain:

```sh
# In /path/to/faas (output must be a new directory outside the checkout):
node scripts/build-data-api-bundle.mjs --version v0.0.0-data-api.local \
  --out-dir /path/to/data-api-bundle
# Back in the initialized notes directory:
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json
npm run typecheck --prefix client
npm test --prefix client
```

The bundle pairs a CLI and SDK from one source commit. The installer verifies
their checksums, installs the CLI at `.gregale-tools/gregale`, installs the SDK
without lifecycle scripts, and updates the client lockfile. Commit
`data-api-artifacts.json` and `client/package-lock.json`. Use the paired CLI for
the commands below, for example `./.gregale-tools/gregale data-api sync ...`.

For automatic client CI, include the authorized SDK tarball at
`client/vendor/gregale-data.tgz` in your application repository. Alternatively,
pin an immutable HTTPS directory holding the bundle artifacts:

```sh
node tools/artifacts.mjs pin /path/to/data-api-bundle/data-api-bundle.json \
  --base-url https://artifacts.example.com/approved/COMMIT/
```

The base URL must have a trailing slash and no embedded credentials, query or
fragment. CI restores the exact SDK checksum and refuses a different lockfile.
Automatic pull-request CI receives no artifact credentials: vendor the SDK
when its download needs authentication. Protected preview CI can use the
optional `GREGALE_ARTIFACT_TOKEN` secret to fetch the paired CLI and SDK. Downloads
require HTTP 200, refuse redirects and never forward the artifact token to
another origin. For a local bundle copy, use
`node tools/artifacts.mjs restore --from /path/to/data-api-bundle --cli` before
`npm ci --prefix client`.

`@gregale/data` is not published to npm. Public SDK distribution remains subject
to Gregale's SDK publishing policy. This workflow builds local files and does
not publish a package. Root migration dependencies have their own lockfile.
`tools/install-sdk.mjs` still accepts a standalone authorized SDK tarball for
local use; bundle-based CI requires the committed bundle pin.

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
| `GREGALE_API` | Preview management API URL |
| `DATA_API_APP` | Data API app slug, such as `notes-data` |
| `DATA_API_URL` | Public HTTPS Data API URL |
| `DATA_API_USER_A_SUBJECT`, `DATA_API_USER_B_SUBJECT` | Two distinct JWT subjects |

Add secrets `GREGALE_OWNER_TOKEN`, `DATA_API_USER_A_TOKEN` and
`DATA_API_USER_B_TOKEN` to that protected environment. If the bundle location
requires bearer authentication, also add `GREGALE_ARTIFACT_TOKEN`. Preview CI
needs the pinned bundle's HTTPS base URL to restore its CLI. The owner key is passed
only to the drift-check step; the RLS step receives application sessions. Keep
sessions current and use an isolated preview database. The CLI and SDK come
from the committed bundle pin; preview CI verifies the CLI's embedded version,
source commit and build timestamp before running its drift check.
Include `linux/amd64` in the bundle's `--targets` for GitHub's Ubuntu runner,
along with your local host target when building on another platform.

The visible `ci/` files are the embedded workflow sources; the initialized
`.github/workflows/` copies are your application workflows. See the
[Data API guide](https://gregale.dev/docs/data-api) for preview enablement,
runtime bounds and staging qualification.
