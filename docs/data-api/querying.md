# Querying a Data API

Generate `Database` with `gregale data-api types NAME`, then create a client with
the API URL and an application-session token provider. Keep the token provider
current across session renewal; account management keys do not authenticate rows.

```ts
import { createDataClient } from '@gregale/data'
import type { Database } from './database.types.js'

const db = createDataClient<Database>({
  url: DATA_API_URL,
  accessToken: () => session.accessToken,
}).schema('api')
const { data, error } = await db.from('notes').select('id,body').limit(20)
```

Choose explicit projections and bounded page sizes. For large collections use
the [cursor helpers](../data-api.md#reusable-cursor-pagination), with a unique
non-null final ordering key. A cursor does not establish a database snapshot.

Use [approved RPCs](../data-api.md#atomic-function-calls-rpc) for atomic business
operations, [typed relationships](../data-api.md#typed-relationships) for joins,
and [live reads](../data-api.md#live-reads) for results that stay current. React
`useLiveRead` accepts `identityScope`: change this nonsecret scope on login,
logout or tenant switch, so the old subscription's rows are discarded immediately.
Pass null options on logout. Never use a JWT as a scope value.

For mutations, classify errors and reconcile uncertain outcomes using the
operation's receipt. A timeout or interrupted response can follow a commit.
See [write recovery](../data-api.md#read-only-receipt-recovery).

## Generated Go and Python clients

Generate clients with `data-api types --language go` or `--language python`.
Pagination preserves caller OR filters and conjoins the cursor boundary; it
replaces ordering and removes offset, so reuse the original query prefix when
building the next page. Relation and RPC names are encoded as individual URL
segments. Names containing slashes are explicitly unsupported.

Both transports require an HTTPS origin (HTTP loopback is allowed for local
development) and reject redirects before forwarding credentials or replaying a
write. Supply the API origin without a path, query, fragment or credentials.

Go inserts, updates and RPC arguments expose `SetNull []string`. Nil optional
pointers omit fields and retain defaults; adding a nullable field's exact
database name to SetNull sends explicit JSON null. For required scalar RPC
arguments, a zero value with SetNull also sends null. Supplying a nonzero value
and SetNull for the same field is rejected. GET RPC null arguments are refused;
use POST when explicit null is required. Generated JSON methods preserve legal
PostgreSQL names containing commas, dashes or backticks without changing keys.
Python nullable inputs continue to use dictionaries and `None`.
