# ADR-1255: Consent-scoped management MCP argument completion

## Status

Accepted. Extends ADR-1250.

## Decision

Expose standard `completion/complete` for the three Gregale review prompts and
seven resource templates. Complete app, project and environment slugs, plus
exact deployment IDs for diagnosis and rollback prompts. Only registered
references and their declared arguments are accepted. Prefix matching is literal,
case-sensitive, sorted and deduplicated; malformed selectors are rejected.

Reuse native customer API reads and existing immutable app/project/environment
consent checks. Remote project and environment candidates come only from existing
grants and are revalidated. Local connections discover the native environment
registry. Named app suggestions require both project and environment. Deployment
suggestions require an app, read at most one 100-entry history page, filter exact
scope and reject foreign-app history. Recheck target identities after history reads.
No plans, approvals, writes, credential values, logs or background polling occur.

Use the existing request timeout and inventory bounds. Return at most 100 values.
`hasMore` signals truncated values or an unexamined deployment history page;
`total` is omitted when incomplete. Narrowing a prefix searches only that observed
page, not older history. Clients use deployment-history tools for deeper searches.
Suggestions neither grant access nor replace validation when a prompt or resource
is subsequently requested. Client UI support depends on its completion capability.

## Consequences

Clients can populate scoped selectors without guessing identifiers. Incomplete
named selections return no app/deployment suggestions. Existing tools, prompts and
resource catalogs retain their counts and behavior. Discovery does not initiate
completion reads, and no candidate cache outlives native checks.
