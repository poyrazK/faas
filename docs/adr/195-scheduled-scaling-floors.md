# ADR-195 · Scheduled scaling floors

- **Status:** accepted
- **Date:** 2026-09-21

## Context

ADR-194 gave an app a list of scaling signals and let the platform combine
them. Every one of those signals is **reactive**: it observes load that has
already arrived and adds capacity afterwards. For an app that parks at zero,
the first request of the morning pays a cold wake, and the first request of
every burst pays a scale-out.

The workloads that suffer most from this are the ones whose shape is known in
advance. An internal tool used 09:00–18:00 on weekdays. A checkout service
before a Monday promotion. A batch consumer that must be warm before an
upstream job starts at 02:00. All of them are expressible as "be warm at this
time", and none of them are expressible with a reactive target.

The platform already has the two pieces this needs, and they are already
shaped correctly:

- `pkg/cronexpr` parses five-field cron with an IANA timezone
  (`Parse(raw, timezone)` wraps robfig with `CRON_TZ=`). Cron is already the
  scheduling DSL customers learn here — cron triggers exist with per-plan
  limits in `pkg/api/limits.go`.
- `App.EffectiveMinInstances()` is the single read-side seam for the warm
  floor. Nine non-test call sites go through it.

That second point is the load-bearing one, and its doc comment records why:

> Pre-#557 the reaper read the legacy column and the sampler read the jsonb; a
> customer who configured the floor via the legacy PATCH got a warm floor they
> were never billed for (revenue-affecting).

A scheduled floor that the scheduler honours and the billing sampler does not
recreates that exact bug, in the exact same place.

## Decision

A scaling policy may declare **schedules**, each of which raises the warm
floor for a window:

```yaml
scaling:
  min_instances: 0
  timezone: Europe/Istanbul
  schedules:
    - cron: "0 8 * * 1-5"
      duration: 12h
      min_instances: 3
```

"From 08:00 Istanbul time on weekdays, keep 3 instances warm for 12 hours."
Outside the window the app returns to `min_instances: 0` and parks.

`scaling_policy` is `jsonb`, so this needs no migration — same as ADR-194.

### A window is a cron fire plus a duration

Cron expresses instants, not intervals. A schedule is therefore `(cron,
duration)`: each fire opens a window that stays open for `duration`.

The alternative — a `days`/`start`/`end` triple — was rejected for two
reasons. It is a second scheduling DSL in a platform that already teaches
cron, and it handles the overnight case badly: `start: "22:00", end: "06:00"`
needs a special "wraps midnight" rule, while `cron: "0 22 * * *", duration:
8h` needs none.

"Am I inside a window now?" is answered without a `Prev()` (robfig has none):
the first fire strictly after `now - duration` is the only fire that could
still have an open window, so the window is open iff that fire is `<= now`.
Windows are half-open, `[fire, fire+duration)`.

### Floors compose by max, and only upward

The effective floor is the maximum of the legacy column, the policy's static
`min_instances`, and every currently-open schedule. Overlapping windows take
the max rather than the last match, so schedule order carries no meaning and
a customer cannot create capacity loss by reordering a list.

Schedules deliberately cannot *lower* a floor or *cap* `max_instances`. A rule
that only adds capacity cannot take an app down; a scheduled cap could throttle
an app during an unforecast spike, which is precisely when a human is least
available to remove it. Cost control at the top end stays `max_instances`,
which the reactive targets already respect.

### Schedule-awareness goes inside EffectiveMinInstances, not at the call sites

`EffectiveMinInstances()` becomes schedule-aware and evaluates against
`time.Now()`. `EffectiveMinInstancesAt(t)` is added for callers that own a
clock — the billing sampler has an injectable `now func() time.Time`, and it
uses it.

This makes a previously time-independent helper depend on the wall clock,
which is a real cost. It is accepted because the alternative is worse: nine
call sites would each have to be found and threaded with a time, and the one
that got missed would be a floor the scheduler honours and billing does not.
Defaulting inside the helper fails safe — a call site nobody updated still
returns the correct number, because in production the injected clock and the
wall clock are the same clock. Threading fails unsafe.

### Plan gating follows the floor, not the field

`min_instances` is a Hobby+ feature; Free is rejected. A schedule raises
`min_instances`, so a Free app declaring `min_instances: 0` with a schedule of
`min_instances: 3` would otherwise buy a warm floor the plan gate exists to
deny. The gate therefore validates the **maximum reachable** floor — the static
value and every schedule's value — not just the static field.

## Consequences

An app declares when it is busy instead of discovering it a cold wake at a
time. Combined with ADR-194 the two halves are complementary and not
alternatives: the schedule sets the floor the app starts the window at, and
the reactive targets scale above it when the day turns out busier than
forecast.

Customers are billed for scheduled warm capacity, because the sampler reads
the same helper the scheduler does. That is the intended behaviour and the
reason the helper — rather than the trigger — is where this lands.

A schedule is not a guarantee of capacity. It raises a floor, and the floor is
still subject to the plan cap, `max_instances`, and the per-node RAM ceiling
(ADR-193). An over-subscribed fleet will not conjure instances at 08:00; it
will report the same capacity problem it reports for any other admission.

Not decided here: schedules that vary `max_instances`, one-off (non-recurring)
windows, and holiday calendars. The first is rejected above on safety grounds;
the other two are additive to this shape and neither has a requester yet.
