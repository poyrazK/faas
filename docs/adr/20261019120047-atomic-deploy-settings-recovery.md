# ADR-20261019120047: Atomic deploy settings recovery

- **Status:** Accepted
- **Date:** 2026-10-12
- **Decision:** Add optional deploy-settings preconditions to app PATCH and require them for saved deploy recovery writes.

Saved deploy resume compares pending lifecycle/scaling settings before writing.
Client-side checks cannot protect against another edit or deployment arriving
between that read and PATCH. The request now carries the expected app ID,
deployment ID and observed values of the targeted settings. The allowlist covers
only the settings saved by single-app deploy sessions, excluding environment,
credentials, source and unrelated app configuration. Guarded requests cannot
combine this contract with other app mutations or environment settings updates.

The in-memory store checks and updates under its app mutex. PostgreSQL locks the
app row, checks the latest deployment and targeted settings, then updates within
the same transaction. Deployment creation already serializes on this parent row;
the checked deployment is also read with a share lock. It must remain live and
must belong to the same app and authenticated account. Conflicts return
`409 deploy_settings_conflict` with field names or a deployment/identity reason,
without exposing configuration values. No change notification or activity handoff
is emitted for rejected writes.

The locked current row is used to rebuild and revalidate lifecycle updates.
Copying a manifest prepared before acquiring the lock would otherwise overwrite
unrelated concurrent changes. Existing app-update side effects and activity
recording remain in their owning paths. Ordinary PATCH calls keep their existing
behavior. No deployment is submitted by this operation, and no new global app
revision or database migration is needed.

Resume sends preconditions for every targeted field in a pending step, including
fields already at the desired value. Lost successful replies are still recovered
by observing the desired settings on the next resume. A conflict leaves the step
pending and preserves the other writer's settings. Unsupported guarded requests
fail closed; there is no fallback to an unguarded update. This extends saved deploy
sessions in ADR-20261019120048 and replaces their initial GET/PATCH race limitation.
