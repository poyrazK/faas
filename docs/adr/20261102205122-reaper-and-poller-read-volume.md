# ADR 20261102205122 · Fewer reaper reads per app, quieter running-reason events, a cached poller list

- **Status:** proposed.
- **Date:** 2026-11-02
- **Amends:** ADR 20261102160622 (the reaper's strict compute sweep), ADR
  20261102072403 (the stats poller's node read), the "why is it running"
  debugger's event recorder
- **Related:** ADR-191 (off-loop reaper work), ADR 20261101091815 (missed
  samples keep an instance's charge)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

A fifth review of `pkg/sched` found:

1. **The reaper still read about seven rows per resident app every tick, one
   query at a time.** The strict compute sweep re-read the app row and its
   budget policies for every owned app, even on accounts with no budget. The
   environment policy resolution read each live deployment's row, its
   runtime values (every sealed secret, the project config and sidecars) only
   for the deployment's scope and environment, and its scaling state, which
   resolves the same runtime owner with the same query shape and returns the
   same scope and environment. At about a thousand apps a node that is
   several thousand round trips a tick.
2. **The "why is it running" recorder wrote an event per busy app every
   15 s.** Its change fingerprint held the cause's evidence (last activity,
   idle deadline, request attribution, connection and tail counts, flow
   endpoints), which moves with every request.
3. **The stats poller read its node's resident instances every 200 ms**, at a
   thousand instances a node five thousand rows a second per scheduler.

## Decision

1. **The reaper skips and batches.**
   - The tick reads which owned apps' accounts hold any budget policy
     (`AccountsWithFinancialBudgets`, one query through
     `financial_budget_policies_account_idx`). The sweep skips an app, with
     no lock and no reads, when its account has none, it is not a preview
     (whose stops the sweep also applies), and no preloaded lease of its
     instances ran out. With no policy there is no hold to apply, so the
     outcome is the per-app sweep's.
   - The tick reads its live deployments' rows in one query
     (`DeploymentsByIDs`); a row missing from it is read on its own.
   - Environment policy takes the scope and environment from the scaling
     state read and no longer reads the runtime values.
2. **The running-reason fingerprint holds each cause's kind and instance
   count**, with the running count, floors and idle timeout; an unchanged
   explanation is re-emitted every five minutes so its evidence stays recent.
   A new or removed cause still records after the existing 15 s throttle.
3. **The poller reuses its node's resident list for up to a second.** It
   rereads at once when the node's telemetry reports an instance the list
   does not hold, beyond those it already lacked when read (a VM being
   destroyed), so a new VM is joined on its first sample. A VM that left keeps
   its row for at most a second. The missing-telemetry count runs only on
   ticks that read the list.

## Consequences

- The strict compute sweep costs nothing per app on accounts without
  budgets; environment policy costs a scaling state read, a migration check
  and, for project apps, a workload spec read per live deployment.
- The debugger's latest observation can be up to five minutes old in its
  evidence while its causes are current.
- Measured memory and activity may carry a departed VM for up to a second,
  which only overstates a node's use.
