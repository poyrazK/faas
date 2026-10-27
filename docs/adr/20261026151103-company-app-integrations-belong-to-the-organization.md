# ADR: Company-app integrations belong to the organization

Date: 2026-10-26
Status: Accepted
Related: [ADR-202610171904](202610171904-organization-resource-lifecycle.md), [ADR-202610171903](202610171903-organization-application-quota-ownership.md), [ADR 20261025211616](20261025211616-event-routing-company-boundary.md)

## Context

A company app keeps its creator's account as `account_id`
(ADR-202610171904: account IDs are immutable storage namespaces, and manual
reparenting is forbidden). Every row hanging off the app does the same, so
the database cannot tell the organization's integrations from the creator's
own. Security reviews keep finding the same consequence on new surfaces: a
creator who leaves the company still reaches, or still receives, company
data through rows that carry their account.

For integrations that send data out of Gregale it is worse in two ways:

- **The organization cannot govern them.** The alert-rule, log-drain,
  webhook and outbound-binding routes were never reviewed for organization
  access, so appaccess fell back to "the creator only, while an owner or
  admin". Once the creator leaves, nobody in the company can list, change or
  remove where the company app's alerts, logs and deployment events go.
- **Nothing records who chose a destination.** `account_id` names the
  creator whoever made the row, so a departure cannot be tied to the
  destinations that member configured.

## Decision

1. **The organization governs a company app's integrations.** The app
   routes for alert rules, log drains and app webhooks, and listing and
   removing outbound bindings, are reviewed organization routes: members
   (`org.read_app`) read them; owners and admins
   (`org.manage_app_security`) create, change and remove them, because each
   one sends company data to a destination outside Gregale. Binding an
   outbound integration stays with the app's creator: the integration and
   its provider credential are the creator's own.
2. **The creator's own rows stay the creator's.** An organization member
   who is not the creator does not see or reach the creator's account-wide
   alert rules through a company app, and cannot bind the creator's
   notification channels to a company app's rule (a rule they create gives
   a `webhook_url` instead).
3. **Each integration records its author.** `alert_rules`,
   `app_log_drains` and `app_webhooks` gain `created_by_account_id`, the
   member who created the row. A row older than the column has none; only
   the app's creator could create those.
4. **A departure pauses what the departing member configured.** When a
   member leaves an organization, in the transaction that
   removes them, the integrations on its company apps whose author is that
   member (or, for rows older than the column, whose app they created)
   stop:
   - webhook alert rules, log drains and app webhooks are disabled;
   - alert rules that also act (rollback, demote, promote) keep acting but
     lose their webhook, so a protective rollback is not lost;
   - the member's own notification channels are unbound from the company
     apps' rules;
   - drains an application standard manages are the organization's and are
     left alone.

   The removal's entry in the organization's activity feed counts what was
   paused. Pausing rather than deleting keeps the configuration for an admin
   to re-point and re-enable; pausing rather than flagging stops company
   data reaching a destination chosen by someone who no longer works there.
5. **Account-scope fan-outs follow organization access.** An account-scope
   release webhook (deployment and rollout events, and GitOps
   notifications) gets a company app's or project's events only while its
   account is an active member of the organization
   (`account_may_see_org_resources`).

## Consequences

- An admin can now repair a company app's alerting after its creator
  leaves, without support.
- A paused alert does not page until an admin re-enables it. The removal's
  activity entry is what makes that visible.
- A departed member's protective alert rules keep rolling back but stop
  telling anyone; an admin gives them a new webhook.
- The dashboard pages for these integrations still serve the creator only;
  the API is the organization's surface for now.
- MCP readiness alert routes, webhook realtime channels and managed realtime
  endpoint callbacks are not yet reviewed organization routes; they keep the
  creator-only fallback.

## Rejected alternatives

- **Reparenting company apps onto an organization account.** Forbidden by
  ADR-202610171904: account IDs are storage namespaces that billing, usage
  and every row already reference.
- **Silencing every integration whose `account_id` is the departing
  member.** On a company app that is every integration the organization
  has, including those other admins configured after this change.
- **Flagging for review without pausing.** Company data keeps flowing to the
  departed member's destination until someone reviews it.

## Amendment 1 (2026-10-27): credentials a company app issues

A sweep of the app routes that still fall back to "the creator only, while
an owner or admin" found a second kind of row a departure leaves behind:
credentials. Each is shown once to the member who mints or sets it and
keeps working after that member leaves, because it belongs to the app, not
to the member. The member removal revokes deploy tokens and organization
keys, which act as the member; it cannot revoke these without breaking the
app's clients. Until now nobody else in the company could revoke or rotate
them either.

**Decision.** Owners and admins (`org.manage_app_security`) list, rotate and
revoke a company app's credentials, whoever created the app:

- API consumers and their keys: list, read, revoke a consumer; list, issue
  and revoke its keys;
- inbound webhooks: list, read, change (signing secret, delivery path,
  enabled) and delete;
- issue ingest tokens: list, issue and revoke;
- the managed realtime endpoint's client authentication: list and read
  endpoints, change one (static token, JWKS issuer, callback and the rest of
  its settings), and rotate or finalize its static bearer token;
- realtime publisher and worker credentials: list, issue, rotate and revoke;
- AI gateway access tokens: list and revoke. Issuing one stays with the
  app's creator, as binding an outbound integration does: the token spends
  the creator's provider credential.

Developers and viewers do not reach these routes. Each handler already binds
the credential to the addressed app, so the change widens no lookup. The
consequence above about managed realtime endpoint callbacks no longer holds
for changing an endpoint; creating and deleting one keep the creator-only
fallback.

**Swept, unchanged.** The other unreviewed app routes keep the fallback:
they configure or operate the app inside Gregale, and a departed creator
loses them at once. Production forks and dev bridges, the two credentials
that act as their creator outside apid, recheck the creator's standing on
every use (`appaccess.Standing`). Traffic mirrors copy requests only to
another deployment of the same app.
