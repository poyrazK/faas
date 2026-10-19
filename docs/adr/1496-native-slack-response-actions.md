# ADR-1496: Native Slack response actions and account links

Date: 2026-10-16

Status: Accepted

## Context

Native Slack alerts, reminders and escalations link to Gregale response pages.
Responders need acknowledgment and snoozing in Slack with an authenticated Gregale
identity, current response permissions and replay protection. Workspace OAuth
installation is a separate capability.

## Decision

Add an opt-in owner policy with one Slack app/workspace per Gregale app and an
age-sealed Slack signing secret. Configure the same Slack app as the bot tokens
and register the per-app `/integrations/slack/issues/{app_id}` Request URL. Keep
bot-token delivery unchanged. Blank-secret identical saves preserve revisions;
changed settings revoke all identity links and fence existing message buttons.
Never return or log signing secrets. There is no email-based identity inference.

Prepare an immutable per-delivery snapshot before sending native messages. Capture
the interaction-policy revision and current response ID/revision; record the exact
successful message timestamp transactionally with delivery completion. Retries do
not refresh a previous snapshot. Tracker messages retain their existing links.
New enabled native messages include Acknowledge, Snooze 1 hour and Snooze 24 hours.
Old messages are not edited. Buttons carry only a delivery ID; it grants no access.

Accept bounded URL-encoded block-action payloads only after verifying Slack's v0
HMAC over the raw bytes with a five-minute request timestamp window. Bind Slack
app/workspace/user, supported action, channel and exact message timestamp. Accept only the signed response_url on HTTPS hooks.slack.com with an /actions/
path bound to the configured workspace, without ports, userinfo, query or redirects.
Post fixed ephemeral replies there; the HTTP 200 alone only acknowledges Slack.
Use a two-second processing
context and return fixed ephemeral responses. Retry transient store failures;
invalid signatures are unauthorized and stale/unauthorized actions change nothing.

Under the app lock, require a retained successful native message, enabled matching
destination/policies, current source eligibility, an open canonical issue and the
captured response ID/revision. Resolve only a current, active, unheld Gregale
identity link and enforce the shared narrow response scope. Reuse the dashboard's
transactional handling helper, cancellation and actor audit; consume the message
in that same transaction. Duplicate or competing clicks never extend a snooze or
repeat a mutation. A response change makes other old message snapshots stale.

An unlinked signed click creates a ten-minute, random one-use browser challenge,
bound to the app, message, Slack user, policy and request identity. Stable retries
reuse it. A linked account cannot acquire a second Slack identity, and another
account cannot take over an existing Slack identity. Confirmation requires the
existing authenticated CSRF/MFA/scope-protected response page and live response
access. GET never links or handles an issue. Show the workspace/user explicitly
and require the user to confirm that they opened the link themselves from Slack.
The user must click again in Slack after linking; retrying the original unlinked
request cannot execute an action. Keep challenges beyond the signature window.
Use no-store and no-referrer on the confirmation page.

Links are app-scoped, expire after 30 days and grant only existing response rights.
Users can unlink through a response page. Policy changes revoke all app links;
MFA enrollment/requirement changes permanently revoke affected account links using
an account trigger. Current membership, assignment, account status and abuse holds
are checked on actions. A changed account or response cannot inherit old grants.

Cap challenges at 100 and identities at 200 per app, expiring old rows before
admission. Message snapshots cascade with bounded native delivery retention.
Challenge receipts cascade with messages. No new generic activity/webhook event,
public API, SDK, notification queue, workspace OAuth or arbitrary outbound destination.

## Consequences

The linked Slack identity acts as a narrowly scoped response credential without a
new browser session for each click. Linking requires possession of the expiring
Slack confirmation URL and a permitted Gregale account; users must not share the
URL. Old or unconfirmed messages may require opening Gregale. Messages already in
flight cannot be recalled. Slack Connect cross-workspace identities and enterprise
installations without a concrete workspace are outside this initial surface.

## Validation

Signature freshness/body tampering, app/workspace/message binding, safe fixed
buttons, challenge confirmation, identity conflicts, MFA/revocation, stale response
and policy gates, duplicate/concurrent callbacks, transactional rollback and shared
handling audit. HTTP CSRF/settings/linking and signed-callback tests; existing
native response/inbox/reminder/escalation/Slack regressions, SQLC and spec checks.
No VM lifecycle change or live Slack messages are required for acceptance.
