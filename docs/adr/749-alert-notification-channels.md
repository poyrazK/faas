# ADR-749 · Alert notification channels: Slack, PagerDuty, email

- **Status:** proposed
- **Date:** 2026-10-10
- **Decision:** Add account-level notification channels that alert rules
  deliver to, alongside or instead of the signed webhook:
  1. **Channels** (`notification_channels`, written by apid): `slack` (an
     incoming-webhook URL on `hooks.slack.com`), `pagerduty` (an Events API
     v2 routing key, US or EU service region), and `email` (the account's
     own verified address). Slack URLs and routing keys are sealed at rest
     like alert webhook secrets.
  2. **Binding:** a rule lists up to `MaxChannelsPerAlertRule` channels. A
     rule with at least one channel no longer needs a webhook URL; existing
     webhook rules are unchanged.
  3. **Delivery** (meterd): on a fire, after the existing claim and cooldown,
     the evaluator sends a formatted message to each channel; on recovery
     (firing → ok) it sends a resolve. PagerDuty uses the rule id as
     `dedup_key`, so a fire opens one incident and the recovery closes it.
     Each channel records its last success or error for the dashboard.
  4. **Verification:** `POST /v1/notification-channels/{id}/test` sends a
     clearly marked test message from apid so customers can confirm a
     channel before a real incident.
- **Why:** Every alert Gregale can raise reaches a person only through a
  signed webhook the customer must receive, verify, and forward. Slack,
  PagerDuty, and email are where on-call actually happens, and external
  monitoring tools deliver to them natively. Without them, alerts that exist
  are not seen.
- **Consequences:**
  - **No new SSRF surface.** Destinations are fixed hosts: a Slack URL must
    be `https://hooks.slack.com/services/…`; PagerDuty posts only to
    `events.pagerduty.com` or `events.eu.pagerduty.com`; email goes through
    the existing mail sender. The customer supplies a token or path, never a
    host.
  - **Email cannot be a relay.** An email channel may only address the
    account's own email. Sending to other addresses needs a confirm-by-link
    flow and is a follow-up.
  - **Resolve messages are new behaviour for channels only.** The signed
    webhook contract keeps its fire-only semantics so existing receivers see
    no change.
  - **Delivery is best effort per channel** with the dispatcher's bounded
    retries. A failing Slack channel never blocks PagerDuty, the webhook, or a
    rollback action; failures are visible on the channel and in
    `meterd_alert_channel_deliveries_total{kind,outcome}`.
  - **Ownership:** apid owns channel definitions and bindings; meterd writes
    only the delivery-status columns, as it already writes alert rule state.
  - **Plans:** channels follow alert rules: any plan that can create alert
    rules can create channels, at most `MaxNotificationChannelsPerAccount`.
- **Rejected alternatives:**
  - *Slack and PagerDuty apps with OAuth.* Better installation UX, but each
    needs a published app, token refresh, and revocation handling; incoming
    webhooks and routing keys are what both vendors document for
    integrations of this size.
  - *Arbitrary email recipients now.* An unverified address list turns alert
    email into a spam relay.
  - *Formatting Slack messages in the customer's webhook receiver.* That is
    the status quo this ADR removes.

## Slices

1. Channels: table, CRUD and test endpoint, Slack/PagerDuty/email senders,
   `gregale channels` CLI.
2. Rule binding and evaluator fan-out for fires and resolves; rules without
   a webhook.
3. Dashboard (channel status, rule bindings) and docs.
