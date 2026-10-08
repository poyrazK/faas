# Gregale Data API client

`@gregale/data` wraps PostgREST's typed client for Gregale application Data APIs.
The management SDK and account keys remain separate from application access.
This package is not published to npm. From an authorized checkout, run
`npm ci && npm run build && npm pack`, then install the produced tarball in
your application. The repository's existing SDK license/publication policy applies.

```ts
import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

const db = createDataClient<Database>({
  url: 'https://my-data.gregale.dev',
  accessToken: async () => auth.getAccessToken(),
}).schema('api')

const { data, error } = await db.from('notes').select('id,body').range(0, 19)
await db.from('notes').insert({subject: auth.user.id, body: 'Hello'})
```

Generate `Database` with `gregale data-api types my-data --output
src/database.types.ts`. Use `--check` in CI to fail when your file differs
from the database. Refresh the serving schema cache after migrations with
`gregale data-api refresh my-data` and verify restart completion.

The token getter runs on each request. Requests omit browser cookies and refuse
redirects so access tokens stay at the configured API. The client preserves
PostgREST relation, insert, update, filter, projection and relationship inference.
See [the Data API guide](../../docs/data-api.md).
