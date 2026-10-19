# ADR-1497: Native Slack OAuth installation

Date: 2026-10-16

Status: Accepted

## Context

Native alerts and response actions require owners to supply bot tokens and signing
secrets. A managed Slack app should provide workspace consent, channel discovery
and removal handling while preserving current team routing and response rights.

## Decision

Configure one operator-managed Slack app with all five `FAAS_ISSUE_SLACK_*`
variables documented in `docs/issues.md`. Missing configuration disables Connect;
partial or invalid configuration fails startup without printing credentials. Slack
OAuth does not return a signing secret: use the operator's app signing secret,
sealed into each confirmed installation's existing interaction policy. Slack token
rotation and Enterprise Grid organization installations are rejected in this
initial version. Require a concrete workspace and a nonrotating `xoxb-` bot token.

Start consent through the existing owner-only CSRF/MFA/deploy-scope settings POST.
Generate a 256-bit nonce with a secure HttpOnly SameSite=Lax browser cookie. Store
only its SHA256 hash, owner/app and installation revision plus an age-sealed flow
bound to the operator configuration. Keep one flow per app, expiring after ten
minutes, with bounded deletion of expired sealed flows by Slack maintenance.
Atomically claim before code exchange; ambiguous exchange failures require
fresh consent rather than reusing the authorization code. Verify granted scopes,
Slack app, workspace and bot identity using fixed Slack HTTPS endpoints. Do not
accept redirects, arbitrary endpoints, user tokens or rotating grants.

Exchange does not install. Redirect away from the code and show an escaped workspace
confirmation without credentials. Only an explicit CSRF-protected owner POST can
consume the choosing flow, reverify the bot identity, seal the token, and install
under the app lock with the captured installation revision. Replacement or local
disconnect consumes pending flows, revokes links and fences old buttons. Installing
a workspace disables all previous team destinations; the owner explicitly chooses
channels again. No old queued notice is replayed to a replacement workspace.

Discover at most five pages of 200 public/private channels, keeping active channels
the bot has joined. Permit another channel ID outside those pages, but verify exact
channel identity, membership and archive status with Slack before enabling. Recheck
installation and destination revisions transactionally after network verification.
Use the existing queue, encrypted token namespace, thread revisions and sender
preflight. Manual credentials remain available when OAuth is disconnected; they
cannot overwrite a connected installation or its managed interaction policy.

Use `/integrations/slack/actions` for the managed Slack app. Verify the configured
signing secret before extracting the delivery identity, then resolve a current
OAuth-bound destination and reuse the existing policy verification, exact-message
binding, account-link flow, live permission checks and transactional actions.

Use signed `/integrations/slack/events` for URL verification, `app_uninstalled` and
`tokens_revoked`. Bind app and workspace, and bot identity for token revocation.
Disable all affected installations, clear their installation/destination tokens,
cancel outstanding deliveries, release leases and invalidate response links/buttons.
Compare the original event time against installation time to ignore delayed events
from before a reconnect. Slack supplies whole seconds; events cover that entire
second and may conservatively disable a reconnect in the same second. Retrying a
processed event is idempotent. A sender's `invalid_auth` result also revokes its
current matching installation, without affecting a replacement credential.

Local Disconnect removes this Gregale app's access and does not call `auth.revoke`:
Slack may share the workspace bot installation across several Gregale apps.
Uninstalling Gregale in Slack revokes all affected connections. This distinction
is explicit in the dashboard. Already issued network requests cannot be recalled.

## Consequences

Owners no longer paste bot tokens or signing secrets for the managed app. The bot
must be invited to each desired channel. Operator signing-secret changes require
reconnection; automatic token rotation, Enterprise Grid organization grants,
Slack Connect identity mapping and per-rule channel routing remain separate work.
There is no live Slack installation or messaging in automated acceptance.

## Validation

Fake-provider config/scope/identity/rotation checks, channel membership and bounded
paging, redirect/body-size limits; PostgreSQL owner/state/revision/expiry fences,
concurrent confirmations, disconnect during pending consent, encrypted credentials,
shared-workspace revocation and stale-event handling; full HTTP consent/confirmation
CSRF, nonce-cookie binding, signed managed actions and uninstall callbacks. Existing
native response/actions and Slack delivery regressions, SQLC and spec checks.
