# Data in your existing application

From the project root, start the local database and authenticated API:

```sh
gregale data-api serve --config gregale-data/data-api.dev.json --port 0 --check
```

The command applies these migrations to a disposable database, generates
`src/gregale-data/database.types.ts` in the selected client, checks its typed
client, and runs the required `notes-rls` scenario before reporting readiness.
It watches new numbered migrations and regenerates types. Ctrl-C removes the
database and local identities. It does not connect to your existing database.

Open the private inspector link to try requests as Alice or Bob. Keep its URL
and the temporary `.gregale/data-api-dev-*.json` session file private: they grant
access to local test identities. No application login provider is installed or
changed. These local identities never authenticate to a hosted API.

Use the generated client from your application, supplying its API URL and
existing application's access-token provider:

```ts
import { createAppData, listNotes } from './gregale-data/client.js'

const data = createAppData({
  url: dataApiUrl,
  accessToken: () => auth.getAccessToken(),
})
const { data: notes, error } = await listNotes(data)
```

The example uses `auth` and `dataApiUrl` supplied by your app. During local
development use the local API URL and matching Alice/Bob token from the private
inspector/session, never a management API key. No credentials are committed.

To verify and exit, removing the disposable services:

```sh
gregale data-api serve --config gregale-data/data-api.dev.json --port 0 --once --check --replay gregale-data/requests.json
```

After committing the generated types, add `--check-types` in CI to reject stale
declarations without rewriting them. Install the pinned SDK and your existing
client dependencies first. Review the source, artifact pin (when using a
bundle), package.json, lockfile, migration and generated types together.

The authorization tests check reads, updates, deletes, forged inserts and
ownership transfers; both users must also be able to access their own data.
They verify the owner's note remains unchanged and clean up their test rows.
Keep these assertions when adapting the example to your domain. Add migrations
instead of editing applied files. Local success does not provision a hosted
database, configure production identity, or qualify deployment availability.
