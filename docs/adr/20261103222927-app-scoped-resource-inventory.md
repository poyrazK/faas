# ADR-20261103222927: App-scoped resource inventory

Status: accepted

Extends [ADR-20261103211147](20261103211147-native-agent-inspection.md).

## Context

MCP domain inspection fetched account-wide domains. Database inspection fetched
all account databases and then every database's bindings before filtering and
paging locally. App-bound credentials correctly cannot reach those account
endpoints. Storage and database inventory tools also required management
capabilities, preventing a read-only agent from discovering its dependencies.

## Decision

`GET /v1/apps/{slug}/resource-inventory` requires `apps:read` and the native
ReadApp organization action. The caller selects one of domains, crons, buckets,
storage_bindings or databases, with an explicit named environment or default
scope. Domains and crons retain their default/app-wide boundary. App binding,
current membership and resource capability release gates still apply.

This is a separate, reviewed metadata surface. It permits dependency names,
public linkage IDs, configuration references and catalog state for the selected
immutable app and scope. It grants no object access, SQL connections, grants,
credential access or lifecycle permissions. Existing resource APIs retain their
permissions. No new scope or credential defaults are introduced.

The response has a version, exact app/scope/kind and environment identities,
requested limit/offset, scoped total, items, has_more, optional next_offset and a
generation time. Pages default to 20 and cap at 100; offsets cap at 1,000,000.
Explicit field allowlists omit access keys, sealed values, credential references,
provider identities, physical bucket names, challenge tokens, command payloads
and raw errors. State is catalog metadata, not runtime health or connectivity.
Responses are private, no-store. No providers, probes or customer writes run.

PostgreSQL filters and counts in the backend using a single read snapshot and
orders before returning at most limit+1 rows. Count evaluation does not require
materializing resource JSON. Database rows join their selected app's bindings
directly and preserve the binding's database namespace for shared resources.
Bucket clone publication and credential rotation filters retain native behavior.
Memory stores use the same selected-scope ordering; managed PostgreSQL retains
its own registry. Adapters without this read surface fail closed.

The handler rechecks app identity, ownership, project and current ReadApp access
after reading, plus named-environment identity where selected. An incompatible
page is withheld. Metadata queries retain organization access fencing.

All five existing MCP inventory tools use the native page. Inspection no longer
requires storage or database management capabilities. The provision tool group
retains these tools on read-only connections; base app inspection authorizes
metadata, while provisioning retains independent write/resource consent. The
bridge checks response version, targets, row and binding identities, cardinality
and continuation consistency. It never falls back to account inventory. Storage
binding pagination now explicitly describes binding entries, rather than a page
of buckets with variable binding fan-out. The focused core catalog is unchanged.

Offsets describe individual snapshots; concurrent changes can move items between
pages. A selected-app count reveals no unrelated app or environment cardinality.

## Validation

Protocol tests exercise all metadata tools with only app read permission, without
a local CLI, and reject unavailable or cross-app responses without account-wide
fallbacks. Native HTTP tests cover app-bound access, read-only scope, revoked
credentials and account/cross-app denial. Memory and PostgreSQL contracts cover
ordering, scoped counts, last and out-of-range pages, storage credential field
exclusion and database binding joins. The deployed app-agent journey additionally
checks a dedicated app-read-only credential; executing it requires a deployed
target and credentials.

OpenAPI, the typed Go client and public/preview generated SDKs publish the page.
No schema migration, provider transition or VM lifecycle behavior changes.
