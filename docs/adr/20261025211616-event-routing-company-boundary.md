# ADR: Event routing keeps the company boundary inside an account

Date: 2026-10-25
Status: Accepted
Amends: [ADR-180](180-internal-event-router-envelope.md) (event envelope and fanout)
Related: [ADR 202610171902](202610171902-organization-offboarding-fences.md), #5321 and #5427 (company access on app and dead-letter routes)

## Context

A company app keeps its rows under the account of the member who created it
(`state.AppResourceAccount` changes the billing subject, not the storage
namespace). Event fanout (`enqueue_event_fanout`, `workflow_event_recipients`)
routes an event to every enabled subscription and workflow in the publishing
account. Inside one account, a creator's personal apps and the company apps
they created therefore exchange events with no company check.

#5321 and #5427 stopped a creator who left or was demoted from acting on the
company's apps through routes that name an app or list its dead letters.
Event routing names no app, which leaves two holes:

- The creator can publish through `/v1/events:publish` (or `:publish-batch`),
  or from a personal app, and invoke the company's subscriptions and
  workflows with any payload.
- A personal app subscribed to `source: "*"` keeps receiving every event the
  company's apps publish (in-guest publishes, `/v1/apps/{slug}/events:publish`,
  bucket notifications, route discovery), after the creator has left.

## Decision

1. **Producers are stamped.** Every server-side publisher records whom the
   event speaks for in a new CloudEvents extension, `producer`:
   - `app:<id>` for an app's own events (vmmd's in-guest publish, the app
     producer route, a bucket's notifications, route discovery);
   - `org:<id>` for the account routes called with a key bound to a company;
   - `personal` for the account routes otherwise.
   The DTOs do not accept it; customers cannot set it.
2. **Same domain is unchanged.** An event reaches a recipient whose app is in
   the producer's company, or in the personal domain when both are personal,
   whatever the creator's standing. A company's own apps keep talking after
   their creator leaves.
3. **Crossing needs a deploying member.** An event crosses between the
   personal domain and a company, or between two companies, only while the
   account's owner is an active owner, admin or developer of every company on
   both sides, in a company not pending deletion. A developer can already
   deploy code that publishes for the company's apps and reads what they
   receive; a viewer, billing member or removed member cannot, so crossing
   would give them that power. This is wider than the owner/admin rule #5321
   applies to unreviewed app routes, on purpose: active developers who created
   company apps see no change.
4. **The check runs in fanout**, in the statement that snapshots recipients
   (`event_recipients_in_producer_reach`), so a membership change applies to
   events accepted after it. Accepted events keep their snapshot.
   Subscription and workflow replays, and their previews, apply the same rule
   at replay time (`event_app_in_producer_reach`): a replay offers an app only
   the retained events fanout would route to it now.
5. **Company keys on account routes.** A key bound to a company publishes on
   `/v1/events:publish` and `:publish-batch` only while its holder is an
   owner, admin or developer there; otherwise the route answers 403
   `org_role_forbidden`.
6. **Unstamped events route as before.** Events from N-1 publishers during a
   rollout, from the platform-tenant route (already scoped to one app and
   tenant) and webhook automations (which snapshot their own recipients) carry
   no producer. An unrecognized producer value fails the publish (SQLSTATE
   23514) rather than routing it.

## Consequences

- A creator who loses deploy access to a company stops reaching the company's
  subscriptions and workflows, and the company's events stop reaching the
  creator's personal apps, in both directions at once.
- Fanout reads `apps`, `orgs` and `org_memberships` per recipient for stamped
  events: indexed primary-key lookups, a few per published event.
- The account read routes apply the same visibility as the app routes
  (`appaccess.VisibleApps`): `/v1/events/receipt`, its attempts and replays
  answer 404 for an event whose producer, or for a recipient whose app, the
  caller may not see; the receipt and `/v1/events/backlog` leave out
  recipients and consumers in hidden apps (aggregate counts still cover the
  account); recipient blockers answer `receipt_unavailable`; and replay jobs
  of a hidden app answer 404.
- `app.<id>` sources are reserved to their app since
  [ADR 20261026140000](20261026140000-event-app-sources-and-publish-rate.md).

## Alternatives

- **Move company apps to an organization storage account.** The real fix for
  the shared-account model, but a data migration across every app table.
- **Drop company recipients from every personal publish.** This breaks
  active creators who publish for their company from a personal session.
- **Owner/admin only for crossing.** Matches #5321's unreviewed-route rule but
  changes routing for every developer who created company apps, with no gain:
  they can bridge the domains by deploying code.
