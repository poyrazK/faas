# Internal frontend deployment activity

The deployment activity view lists **CI intents recorded by Gregale**. It connects
stable run IDs to retained release and preview observations so an operator can
inspect the right deployment without copying its UUID from GitHub output.
Manual deployments have their existing release/history views. Jobs that fail or
are rejected before Gregale records an intent do not appear here.

## Opt in

All switches default off. Keep main hosting enabled, isolated hosting provider
configuration, the internal operator allowlist, MFA and read scopes. Automation
must also be enabled/configured; disabling it makes activity unavailable.

| Surface | Independent opt-in |
| --- | --- |
| API | Hot `frontend_hosting_activity_enabled`, initially seeded by `FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED=1` |
| CLI | `FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED=1`, plus the main CLI hosting flag |
| Dashboard | `NEXT_PUBLIC_FRONTEND_HOSTING_ACTIVITY_ENABLED=true` at build time, main dashboard hosting and API `activity_enabled` |

Terraform emits `FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED=0`. The API advertises
`activity_page_size: 25` when available. There is no migration or new Worker flag.
Upgrade API binaries before enabling the helper. CI automation/OIDC credentials
cannot list runs; their exact endpoint allowlist remains unchanged.

History and previews have separate controls. With history API admission disabled,
activity omits source labels and reports `source_labels_included: false`. The
dashboard also honors the existing history build flag. Source labels are
caller-provided assertions, not verified GitHub identity, and disappear from this
view if their release is cleaned up. Disabled previews yield an explicit unknown
check; they are not classified as missing. Diagnostics has its own opt-ins.

## Dashboard

Open a site's **Deployment activity** panel. Each row shows the original run,
release and preview IDs, creation time, original preview expiry, stored checks and
optional source context. The states mean:

| State | Interpretation |
| --- | --- |
| Recorded | The selected stored checks have no pending, unknown or blocked observation. This is not a serving or GitHub job success claim. |
| Pending | Stored verification or preview publication is incomplete. This does not identify a failed GitHub job. |
| Unknown | Evidence is disabled, inconsistent or unrecognized. Inspect before deciding how to recover. |
| Blocked | Stored site/run/resource conditions require attention, such as an expired window, suspension, revocation or removed release. Production health is separate. |

**Inspect run** selects the row's exact original UUID in the existing diagnostics
panel when its API/build flags are enabled. **Review release** is offered only
when that release remains verified and is not the current active release. It opens
the existing comparison using current site/release data; it does not promote.
Publishing still needs explicit review and the original revision check.

Use **Older runs** to follow the exclusive cursor and **Newest runs** to return to
the beginning. The current page refreshes; pagination never silently selects a
latest run. Changing pages cancels obsolete requests. Failed reads hide old rows
and their actions; unavailable/changing site details pause activity actions.

## CLI and API

```sh
export FAAS_FRONTEND_HOSTING_ENABLED=1
export FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED=1
gregale sites runs --site SITE_UUID
gregale sites runs --site SITE_UUID --before CURSOR_FROM_PREVIOUS_PAGE
```

Use an existing operator login or credential. The CLI reads one page and emits
JSON with `site_id`, `entries`, optional `next_cursor`, `observed_at`,
`source_labels_included` and `serving_health: "not_checked"`. Entry checks reuse
closed diagnostic states, codes and explanations. Exit 0 means a report was read,
including rows with blockers; it does not certify readiness. Local/API errors exit
nonzero and do not print a partial report. HTTPS and no redirects apply; loopback
HTTP is allowed for local tests through the existing diagnostic client helper.

API: `GET /v1/internal/hosting/sites/SITE_UUID/runs`, optionally
`?before=NEXT_CURSOR`. Omit the parameter for newest; empty, duplicated, unknown,
malformed or oversized query values are rejected. Cursors are opaque to clients,
canonical, at most 128 bytes and scoped by the request's independent ownership
check. Each page contains at most 25 entries in descending creation-time/UUID
order. There is no offset, caller-selected unbounded page size or implicit traversal.

## Retention, cost and evidence limits

The list reads existing metadata. Postgres projects one bounded page with matching
release/preview metadata in a single SQLC statement, without loading manifests,
listing file contents or contacting providers. Ownership and final intent checks
are additional bounded site reads. MemStore uses one projection lock and detached
values. Site/feature changes detected during observation cause conflict/disabled
responses; storage failures never become a successful empty page. Responses are
no-store and should be refreshed before taking an action.

Run identities and original preview expiry survive release/preview GC. Listing
never recreates resources, retries deployment, frees the existing 1,000-run quota
or extends preview lifetime. A removed resource is reported as such only after a
successful enabled observation. Continue using original run IDs when inspecting
uncertain results; see [diagnostics](internal-frontend-hosting-diagnostics.md).

Neither listing nor its tests checks GitHub jobs, DNS, TLS, Access sign-in, R2
availability or customer-facing serving health. A stored publication receipt is
an acknowledgment, not a live health check. Isolated live GitHub/Cloudflare
qualification remains deferred until the pilot inputs are available.
