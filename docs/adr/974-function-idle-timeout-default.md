# ADR-974: 30-second default idle timeout for functions

- **Status:** accepted
- **Date:** 2026-10-10
- **Related:** spec §4.3 (idle reaper), §4.7 (billing), ADR-005 (cold boot
  always works), ADR-009 (snapshot reuse)

## Context

Spec §4.3 gives every app the plan's idle timeout (Free 60 s, Hobby 60 s,
Pro 300 s, Scale 600 s) unless it configures one between 10 s and twice the
plan default. Billing (§4.7) charges plan RAM + 8 MB for every second an
instance runs, so an app called once pays for its wake plus the whole idle
timeout: on Scale, ten minutes of resident RAM for one request.

That posture suits apps, which hold connections, caches and background
work. Functions are request handlers. For them the trailing idle window is
mostly paid, unused residency, and it is the main reason a rarely called
function costs more than it should.

Parking a function is cheap. When the deployment's init snapshot already
exists, `captureInitOrReuse` only destroys the VM; nothing is written. The
next request restores that snapshot, and the runner keeps the handler process
warm inside it (Node, Python, and Go handlers on the persistent protocol). On
the internal test node the restored guest answers the first proxied request in
42 ms at p50, measured on 2026-10-10. Production wakes measure about 555 ms
p50 from admission to first byte (2026-10-09).

## Decision

1. **Default.** An app with `type = function` and no configured
   `idle_timeout_s` uses `api.FunctionIdleTimeoutDefaultSeconds` = 30 s on
   every plan, or the plan default if that is lower.
   `Limits.DefaultIdleTimeoutS(appType)` resolves it. Apps keep the plan
   default.

2. **Bounds unchanged.** Functions configure their idle timeout within the
   same bounds as apps: 10 s floor, plan default × 2 ceiling. A function that
   wants the old behaviour sets `idle_timeout_s` explicitly.

3. **One resolver.** Every place that filled in the plan default for an unset
   timeout uses the type-aware default: the schedd idle reaper and running
   explanation, gatewayd's warm-target check, the service TCP session idle
   timeout, and apid's debugger running view. `InstanceInfo` carries the
   app type for this.

4. **Effective park time.** The reaper runs every 10 s and `last_request_at`
   flushes every 15 s, so a function parks roughly 30–55 s after its last
   request.

## Consequences

- A function called a few times an hour accrues tens of seconds of resident
  RAM per call instead of 1–10 minutes. Customer bills and included-hours
  consumption for functions fall; host residency per function falls with
  them.
- Functions wake more often, so more requests pay wake latency. Customers
  for whom that matters set `idle_timeout_s`, `min_instances`, or a warm
  pool.
- The financial model lists the plan idle timeouts per plan. Those values
  still apply to apps. Functions are a deliberate deviation, recorded here.
  The model's resident-concurrency assumptions are conservative for
  functions under this default.
- Existing functions without an explicit value move to the new default on
  the next schedd and gatewayd release. No migration is needed because unset
  stays NULL.
