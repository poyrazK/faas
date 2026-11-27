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
  `flag notify clear`, `flag notify get`.
- `POST .../experiments` takes `notify_channel_ids`; CLI
  `flag experiment create --notify ID`.

Channels are captured when a notification is queued, so a later settings
change never re-sends or retargets a queued one.

### 2. Experiment notifications

An experiment's channels are its own that still exist, else the
environment's; a message says which and how to change them.

apid's experiment reconciler, at most once an hour per running experiment
with channels and under its own timeout, computes the same recommendation
the results endpoint returns. It queues one notification per experiment,
action and variant: `ship_variant` and `keep_control` as a decision,
`fix_allocation` as a problem. The guardrail check queues the `rolled_back`
problem before it stops the experiment, from the breach alone (no results
report); when queueing fails the experiment stays running and the next
pass, which finds the same breach, tries again. The message names the
experiment, flag, environment, the recommendation's summary and its
commands (`gregale flag experiment promote|stop ...`).

A recommendation that flaps (`ship_variant` → `keep_running` → `ship_variant`)
notifies once: the queue key is the experiment, action and variant, and
`flag_notification_keys` keeps every key queued, so a key is never queued
twice, even after its outbox row is swept. Only weekly digest keys, which
never recur, are swept from it.

### 3. Lifecycle notifications

The hourly lifecycle check, for an environment with channels:

- queues a **weekly digest** once per ISO week (UTC) when any flag needs
  attention, listing each flag, its reasons, owner and the command that
  resolves it (`gregale flag delete`, `flag edit --expires-at`,
  `flag edit --owner`);
- queues an **expiry** message for each flag whose `expires_at` has passed,
  once per flag and expiry date.

The `flags.lifecycle_attention` audit events are unchanged.

The check runs two minutes after apid starts and then hourly, and each pass
reads every environment, a page at a time. apid restarts more often than
hourly when releases land: a check that first ran an hour after start, or a
page cursor kept between passes, could go hours without reaching an
environment. Every message and audit event is keyed, so extra passes record
nothing twice.

### 4. Delivery

apid writes `flag_notifications` rows (an outbox: account, the project's
organization, environment, kind, unique key, subject, summary, channel ids,
attempts, next attempt, delivered time). meterd, which holds the channel
credentials, claims due rows under a five-minute lease, sends them through
the alert channel sender and marks them delivered. A notification some
channels refused is retried, with backoff up to
`FlagsNotificationMaxAttempts`, to those channels only. A batch starts no
send in the lease's last 45 s and hands the remaining rows back, and a
settle applies only to the claim that made it, so a row is never sent by
two meterds. Waiting for the release (§5) is not an attempt. Delivered and
abandoned rows are deleted after `FlagsNotificationRetentionDays`. A
channel deleted since queueing is skipped.

### 5. Release

Dark behind the `flag_notifications` platform flag (by plan or
organization: the project's organization is captured on each row), checked
when apid queues and when meterd sends: off, nothing is queued, queued rows wait,
the settings endpoints and an experiment with its own channels answer 403.

## Consequences

- Teams hear about a finished or broken experiment without polling, and
  flag debt shows up weekly where they already read alerts.
- One extra results computation per running experiment per reconciler pass,
  only for experiments with channels.
- A new table pair owned by apid (settings, outbox writes) and meterd
  (delivery state).
