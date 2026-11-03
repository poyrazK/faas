# ADR 20261103094355 · Scale stamps keep the app list, the floor sweep reads on admission, one observation statement

- **Status:** proposed.
- **Date:** 2026-11-03
- **Amends:** the app list snapshot (`pkg/sched/app_snapshot.go`, introduced
  with the per-second poller load fix), ADR-071 / ADR-072 (the floor
  reconciler's per-deployment sweep), the scheduler scaling-policy
  convergence watermark (`app_scaling_policy_scheduler_status`)
- **Related:** ADR 20261102205122, ADR 20261102160622 (earlier read-volume
  rounds), ADR-590 (environment scale stamps)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A sixth review of `pkg/sched` found:

1. **Scale stamps emptied the app list cache.** schedd's scale-up, targets
   and floor triggers, its reaper and its scaling-policy observer share one
   app list that is reread only when its version changes. The version
   summed every listed row's `xmin`. Every ordinary wake stamps
   `last_scale_out_at` and every idle park or freeze stamps
   `last_scale_in_at`; for production these land on the `apps` row. On a
   node with about a thousand apps that is several writes a second, so the
   list (every app with its manifest) was reread on almost every call and
   deep-copied for every caller.
2. **The floor sweep read four rows per floored deployment every second.**
   For each live deployment of an app that can carry a floor it read the app,
   the project migration fence, the project workload spec and the account's
   plan, before the in-memory check that the floor is already met, which it
   almost always is.
3. **The scaling-policy observer wrote every app every 30 s, on the main
   loop.** `scaling_policy_revision` defaults to 1, so every app qualified;
   the upsert's `>=` rewrote unchanged rows; and the statements ran one per
   app on the select loop that also runs the one-second triggers.
4. **The reaper scanned its snapshot for each instance it parked or
   evicted**, so a mass idle park was quadratic in the node's instances.

## Decision

1. **The list version follows runtime generations.** It is the listed apps'
   count, the sum of their `app_runtime_generations.generation`, and the sum
   of their ID hashes. The apps trigger bumps the generation on every change
   except to the scale stamps, `last_deploy_failed_email_at`,
   `purge_claimed_at` and `preview_destroy_commented_at`, so a stamp no
   longer forces a reread; the count and the ID hashes catch an app joining
   or leaving the list. No migration.
   - Those columns can be older in a reused list than in the row. Before an
     admission a cooldown could hold, the targets trigger reads the app's
     scale-out stamp (`AppScaleOutStamp`); the floor sweep reads the app
     again (below). Reaper rows with a deployment already take their stamps
     from the deployment's scaling state; only legacy rows without one use
     the list's.
2. **The floor sweep decides from the list and reads on admission.** It
   copies only the floored apps from the list (`AppsByID`), reuses a
   deployment's resolved settings while its listed row and scope are
   unchanged, and an account's plan, each for up to 10 s, and resolves the
   app, settings and plan again from the store before it admits. A met floor
   costs no read; a raised floor in a workload spec or a plan change is seen
   within 10 s.
3. **The periodic observation is one statement** over every owned app. It
   writes a row for a new revision or a new scheduler at once, and rewrites
   an unchanged one when it is 45 s old by the database clock, so with the
   30 s pass a row is rewritten about every 60 s, inside the 90 s freshness
   the status read trusts. The `app_changed` path still records one app at
   once.
4. **The reaper looks instances up in a map** built once per pass.

## Consequences

- At steady state the per-second pollers read the version row only; the
  list is reread when an app's settings, deployment or account plan change.
- An app's scale stamps in a reused list can lag the row; every consumer
  that acts on one reads it fresh first.
- The floor sweep can take up to 10 s longer to act on a raised floor that
  comes from a project workload spec or a plan change; an app row change is
  seen on the next tick, as before.
- Observation writes drop from one per app per 30 s to about one per app per
  minute, in one statement per pass.
