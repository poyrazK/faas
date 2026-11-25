# ADR 20261125085913 · Flag notifications

- **Status:** accepted
- **Date:** 2026-11-25
- **Amends:** ADR-20261114150000 (flags usability §4 lifecycle),
  ADR-20261115163113 (flag experiments), ADR 20261116190208 (guardrails and
  recommendations)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

Gregale already decides when an experiment needs a human: `flags.Recommend`
turns results into `ship_variant`, `keep_control`, `fix_allocation` (sample
ratio mismatch) or `rolled_back` (a guardrail stopped it). It already knows
when a flag needs one: the hourly lifecycle check records
`flags.lifecycle_attention` for expired, expiring, stale, never-evaluated and
ownerless flags. Both only show up when someone runs
`gregale flag experiment results` or `gregale flag lifecycle`.

The account's notification channels (Slack, PagerDuty, email; ADR-749)
already carry alerts and saved-insight digests.

The owner chose before this record:

1. **Recipients:** an experiment names its channels when it is created; an
   environment names channels for its flags. An experiment without its own
   channels uses the environment's.
2. **Experiment events:** a decision (`ship_variant`, `keep_control`) and a
   problem (`rolled_back`, `fix_allocation`). Not starts, stops or periodic
   progress.
3. **Lifecycle:** a weekly digest per environment, plus one message when a
   flag expires.
4. **Notify only:** messages carry the commands; nothing ships, stops or
   deletes on its own.

## Decision

### 1. Configuration

- `PUT /v1/projects/{slug}/environments/{environment}/flags/notifications`
  `{ "channel_ids": [...] }` (at most `FlagsMaxNotificationChannels`, the
  account's channels) sets an environment's channels; an empty list turns
  them off. `GET` reads them. CLI: `gregale flag notify set --channel ID...`,
  `flag notify clear`, `flag notify show`.
- `POST .../flags/experiments` takes `notify_channel_ids`; CLI
  `flag experiment create --notify ID`.

Channels are captured when a notification is queued, so a later settings
change never re-sends or retargets a queued one.

### 2. Experiment notifications

apid's experiment reconciler, after each pass over a running experiment whose
channels (its own, else the environment's) are not empty, computes the same
recommendation the results endpoint returns. It queues one notification per
experiment, action and variant: `ship_variant` and `keep_control` as a
decision, `fix_allocation` as a problem. The guardrail rollback queues a
`rolled_back` problem when it stops the experiment. The message names the
experiment, flag, environment, the recommendation's summary and its commands
(`gregale flag experiment promote|stop ...`).

A recommendation that flaps (`ship_variant` → `keep_running` → `ship_variant`)
notifies once: the queue key is the experiment, action and variant.

### 3. Lifecycle notifications

The hourly lifecycle check, for an environment with channels:

- queues a **weekly digest** once per ISO week (UTC) when any flag needs
  attention, listing each flag, its reasons, owner and the command that
  resolves it (`gregale flag delete`, `flag edit --expires-at`,
  `flag edit --owner`);
- queues an **expiry** message for each flag whose `expires_at` has passed,
  once per flag and expiry date.

The `flags.lifecycle_attention` audit events are unchanged.

### 4. Delivery

apid writes `flag_notifications` rows (an outbox: account, environment, kind,
unique key, subject, summary, channel ids, attempts, next attempt, delivered
time). meterd, which holds the channel credentials, claims due rows, sends
them through the alert channel sender, marks them delivered, and retries
failures with backoff up to `FlagsNotificationMaxAttempts`. Delivered and
abandoned rows are deleted after `FlagsNotificationRetentionDays`. A channel
deleted since queueing is skipped.

### 5. Release

Dark behind the `flag_notifications` platform flag (by plan), checked when
apid queues and when meterd sends: off, nothing is queued, queued rows wait,
and the settings endpoints answer 404 like other unreleased routes.

## Consequences

- Teams hear about a finished or broken experiment without polling, and
  flag debt shows up weekly where they already read alerts.
- One extra results computation per running experiment per reconciler pass,
  only for experiments with channels.
- A new table pair owned by apid (settings, outbox writes) and meterd
  (delivery state).
