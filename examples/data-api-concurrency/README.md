# Concurrent document edits

Run `gregale data-api dev --port 0` here to generate `database.types.ts` and start
a disposable API. Install `@gregale/data` in your application and use `client.ts`
as the typed edit/delete workflow. The Alice identity owns a team-a document;
the other identity has the same subject and a different tenant.

Read `id`, `body` and `version` before editing. The update filters by both the
complete primary key and the version you read, and selects the committed row.
A database trigger advances the version atomically for every update, including
ordinary REST writes. Two clients using the same version cannot both succeed.

`optimisticMutation` returns `applied` with the new row, `conflict` when no row
matches, or `error` with the original transport/server error. For a conflict,
reload the visible document and let the user review or merge their edits.
Missing, deleted and tenant-hidden rows produce the same conflict outcome.
Automatic retries are disabled; a transport failure is an uncertain outcome,
not proof of a conflict or rollback. Re-read before retrying.

Always include every primary-key column in the mutation filters. The helper
classifies results; it does not enforce SQL predicates or roll back an overly
broad mutation. Inserts use generated IDs so a deleted document's ID is never
reused by a client. The version is a positive integer; overflow rejects an
update instead of wrapping and reusing a stale version.

For hosted deployment, apply this migration through the owner migration app
before refreshing the API. RLS and restricted binding permissions remain the
authorization boundary. Conditional edits are an application workflow; ordinary
unconditional writes remain available.
