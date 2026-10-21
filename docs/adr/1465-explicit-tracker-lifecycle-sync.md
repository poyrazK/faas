# ADR 1465: Explicit tracker lifecycle synchronization

Status: Accepted

## Decision

Add `gregale issues tickets sync` for one stored GitHub or Linear reference at a time. Require a direction: `push` treats Gregale as authority, and `pull` treats the provider as authority. Preview by default; `--apply` selects a write. Keep provider credentials in environment variables on the CLI machine. Native organization OAuth connections, provider callbacks and continuous reconciliation remain separate increments.

Map GitHub open to Gregale open, and closed/completed (including legacy empty close reasons) to resolved. Do not map not-planned or unfamiliar states. Map Linear backlog, unstarted and started workflow types to open, and completed to resolved. Refuse canceled and archived issues and archived workflow states. Ignored Gregale issues require manual triage. For Linear push, operators choose a workflow state UUID; verify that it belongs to the linked issue's team, is active and maps to the desired lifecycle rather than guessing by display name.

Read the app, issue group and paginated stored references before accessing the provider. Bind provider observations to the exact canonical URL and identity. Reject pull requests, different repositories/workspaces, invalid identifiers and incomplete responses. Recheck the current Gregale canonical group, lifecycle and reference membership before a write. Recheck the provider snapshot before changing either side. An unchanged lifecycle is a no-op. A pulled resolution requires the explicit fixing deployment and uses the existing Gregale resolve action so release attribution, regressions, activity and notifications retain their existing behavior.

Use absolute provider state updates, verify the response identity and resulting state, and sanitize transport/provider errors. The CLI performs no automatic retry. After an uncertain response, preview again: a confirmed desired state becomes a no-op instead of repeating the lifecycle action. No creation journal is needed because sync never creates tickets and never deletes references.

## Consequences

Operators can synchronize lifecycle in either direction with explicit authority and previewed changes. This is manual CLI synchronization, not a background integration or webhook loop. Credentials need provider read access for preview and write access for push; pulling needs Gregale's normal mutation permission and MFA posture. The provider and Gregale APIs do not share an atomic compare-and-swap or transaction: checks detect observed changes, but another writer can still race between a final read and mutation. Use one authority per operation and rerun the preview after concurrent edits or uncertain outcomes. No distributed locking or exactly-once lifecycle delivery is claimed.
