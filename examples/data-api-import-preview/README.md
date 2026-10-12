# Import preview and review

Run `gregale data-api dev --port 0` in this directory to generate the database
contract and verify read-only preview, validation and tenant isolation. Install
`@gregale/data` in your application and use the typed `previewImport` and
`commitImport` functions from `client.ts`.

The approved SECURITY INVOKER `preview_import` RPC accepts up to 100 JSON rows
per call. Each row has `key` (1–128 characters) and `body` (1–10000 characters).
It returns zero-based indexes, `insert`, `update`, `unchanged` or `invalid` actions,
error codes, row IDs/versions and total/valid counts. Render these codes as actionable messages:

| Error | Suggested correction |
| --- | --- |
| `row_must_be_object` | Supply a row object with key and body. |
| `unknown_fields` | Remove fields other than key and body. |
| `key_requires_1_to_128_characters` | Supply a nonempty string key within the limit. |
| `body_requires_1_to_10000_characters` | Supply a nonempty string body within the limit. |
| `duplicate_input_key` | Resolve repeated keys before committing. |

Both copies of a duplicate input key are invalid, preventing input order from
silently deciding a winner. Existing visible keys are classified as unchanged
or update. A key belonging to another subject or tenant appears as insert, just
like any other absent visible key. Claims determine identity; callers cannot
supply owner or tenant fields. The typed client validates the JSON response
before exposing it to a review screen.

Preview is a STABLE read-only function: it writes no rows and consumes no IDs.
It describes the application rules encoded in this migration, not every future
PostgreSQL constraint. The row limit is an example-specific processing bound,
not a Gregale platform quota. Split larger previews deliberately and check for
duplicates across the entire import before splitting; separate preview windows
do not detect duplicates in other windows.

After user review, call `approveImport(preview)` to snapshot its payload, identity,
row IDs and versions. Pass that approval to `commitImport`. Invalid preview rows
cannot be approved. IDs are decimal strings to preserve bigint precision.

Commit repeats validation, requires the same authenticated subject and tenant,
and applies conditional writes in one transaction. Existing keys must still
have the reviewed row ID and version; expected new keys must still be absent.
Deleted/recreated keys conflict even if their new version is 1. Ordinary REST
updates also advance the database-managed version, and identity/key changes are
rejected. Every accepted existing-row commit advances its version, including an
unchanged body, so concurrent commits cannot both consume the same review.

A changed, disappeared or newly occupied key returns HTTP 409 with `PT409`,
message `import_review_conflict`, and JSON error details identifying the first
conflicting input index/key. All writes in the call roll back, including earlier
successful rows. Fetch a fresh preview and ask the user to review again; do not
silently replace the expected versions. Switching review identity returns 403.
The old unguarded commit RPC signature is removed by migration 0002.

Review does not lock records or reserve keys. Version predicates and unique
constraints protect the write when commit actually runs. New database rules
can still reject it; database constraints remain authoritative. Returned rows
are ordered by key to make multi-row locking consistent. Larger imports split
across calls are not atomic as a whole. If the response is lost, reconcile stored
rows before retrying: the original review may already have been consumed.

These are application-side review expectations, not cryptographically signed
approvals. They protect against stale state, not against an authorized caller
intentionally submitting another permitted edit. RLS remains the authorization
boundary.

For hosted use, apply the owner migration and grant the qualified binding role
execution on `preview_import(jsonb)` and `commit_import(jsonb,jsonb,jsonb)` through the existing
RPC permission workflow. RLS and restricted binding permissions remain mandatory.
No generic SDK validation can replace those database rules.
