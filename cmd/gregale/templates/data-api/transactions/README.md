# Review the __OP__ transaction starter

This is an editable order/inventory example, not an automatic migration or a
production business model. Copy migration.sql as your next numbered migration,
review table/function names and constraints, then regenerate database.types.ts with runtime validators:

```sh
gregale data-api types YOUR_DATA_API --validators --output database.types.ts
# Or, from your local project:
gregale data-api dev --validators --once
```

The client consumes `rpcValidators.api.__OP__` for schema-owned argument names,
presence and shapes. Its wrapper retains UUID formatting, SKU rules, quantities
and retry-key business requirements, which the SQL function also enforces.
Move client.ts alongside those generated types. No duplicate type definitions
are maintained: Args and RPC result inference come from your schema.

Configure verified identity-provider claims with tenant_id and sub. For local
checks use Alice {subject:"alice",claims:{tenant_id:"alpha"}} and Bob
{subject:"bob",claims:{tenant_id:"beta"}}. fixture.sql contains synthetic data.
Pass access-checks.json to data-api verify --access-checks with that fixture;
your project's existing required authorization scenarios still run first.
Run data-checks.json against a fresh fixture or during migration preview.

Run the normal RPC permission setup as the migration owner. The function is
SECURITY INVOKER with fixed search_path, PUBLIC execution revoked and @gregale:rpc
opt-in. Every REST call runs one PostgreSQL transaction; raised errors roll back
stock, order headers and items. Deterministic row locks prevent concurrent orders
from overselling stock. Same tenant/actor/request_key retries return the original
receipt; reusing the key with another payload returns a conflict. An advisory
lock serializes identical keys, with normal database timeout handling.

RLS reads are tenant-scoped. Write policies also require a transaction-local
scope marker set by the reviewed function, blocking direct REST writes. The
marker is not a boundary against roles with arbitrary SQL execution: audit all
exposed functions and SQL grants. Tenant members share order visibility here;
add real membership/admin/revocation checks for your business model. Trusted
claims must be issued by your identity provider; never derive them from an
unverified client payload. Prices come from locked inventory, never RPC inputs.

The parser validates UUIDs, distinct SKUs, array sizes and quantities before RPC
construction; the database validates again. Adapt these example domain limits.
Keep the same request key when retrying an uncertain commit; retries after receipt
retention changes can create new operations. Do not delete receipts while clients
may retry. This example retains receipts indefinitely. External emails/webhooks
cannot roll back with PostgreSQL; use a transactional outbox for such effects.

Before deployment review migrations, client checks, integrity/access assertions,
query plans and coverage with data-api verify release. No generic SQL endpoint,
client-selected ownership or SECURITY DEFINER function is generated.
