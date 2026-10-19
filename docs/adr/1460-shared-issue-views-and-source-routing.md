# ADR 1460: Shared issue views and source/tag ownership routing

Status: Accepted

## Decision

Persist up to 20 shared named inbox views per app. Stable keys contain at most 48 lowercase letters, digits, or hyphens; names contain at most 64 UTF-8 bytes without control characters. Store only known state, environment, assignee, sort, customer-threshold, and operational-tag filters. App access controls still govern every read; saving or deleting requires the existing deployment-write permission and session MFA. An explicit UUID owner filter must refer to an eligible app owner or active organization member when saved. The `me` token remains relative to the authenticated reader.

Dashboard links address a view by its stable key and load its current definition on every request. Opening a view starts a fresh page. New issue-list cursors bind the entire normalized filter set, including the resolved owner, to prevent reuse across changed views or readers. Historical cursors remain accepted for compatibility. Views describe live queries, not frozen issue snapshots. Existing keys replace their filters under the app lock; quota checks and deletion use the same lock. These app-scoped inbox preferences are operational metadata and are not copied by workload cloning.

Extend the existing ordered ownership rules with a local source-path prefix and one exact allowlisted operational `key=value` tag matcher. Match all populated criteria together. Source paths match at a path-segment boundary against any application frame in the exception or its causes; dependency frames do not match. Normalize backslashes and lexical dot segments, reject parent traversal and URLs, and perform no glob evaluation or filesystem access. Symbolication runs before routing, so opted-in mapped frames can match original source paths. Tags are sanitized reported evidence and do not supply verified customer identity.

Preserve prospective assignment: evaluate only new groups, select the first matching rule with an eligible target, and retain normal assignment activity/webhooks. Recurrences, regressions, and manual assignments do not trigger reassignment. The migration refuses rollback while saved views or extended matchers exist, preventing accidental preference loss or broadened routing when matcher columns disappear.

## Consequences

Teams can share live inboxes and route new failures by component or source location without changing grouping identity. Names may repeat; stable keys distinguish views. Shared settings use the existing replacement semantics, so collaborators should coordinate edits to the same key. CODEOWNERS parsing, glob policies, team objects, provider integrations, and crash-free session instrumentation remain separate increments.
