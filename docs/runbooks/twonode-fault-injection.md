# Runbook — Two-node fault injection drill

This runbook is the operator-side companion to the
[Workstream B failure-safe test suite](twonode-fault-injection_test.go).
Run it quarterly to keep the operator playbook honest against the
test cases — a runbook step that drifts from the test assertion
is the exact regression the test exists to catch.

## Scope

Two control-plane nodes (`fsn-a`, `fsn-b`) running one schedd +
one vmmd each, sharing one apid (the Tier A7 multi-host-single-
apid pattern from ADR-070). Drill scope: failure-mode coverage,
not load. The goal is to verify the recovery arbiter, drain
handler, and pg_notify fan-out recover cleanly within budget.

## Pre-flight

```bash
# 1. Confirm both schedd + vmmd are healthy
faasctl nodes list --format=tsv | awk '$4=="active"'
# Expected: fsn-a, fsn-b

# 2. Confirm no in-flight drain
faasctl nodes drain --all --status
# Expected: (empty)
```

## Drill 1 — Heartbeat gap → node.unavailable

Step 1: On the disposable drill fleet, pause vmmd on fsn-b long enough to
cross the 90s heartbeat threshold. This exercises the real failure path
without changing database state by hand.

```bash
ssh fsn-b 'sudo systemctl kill -s SIGSTOP faas-vmmd'
# Wait at least 120s, then continue to observation.
```

Step 2: Observe the recovery timeline.

```bash
# Within 90s the row must flip to lifecycle='unavailable'.
# The apid event log must show a `node.failed` row.
faasctl events list --topic=recovery --since=5m
```

Expected outcome: lifecycle='unavailable', event row present,
no customer-facing 5xx on fsn-a traffic.

Resume vmmd after the unavailable transition is visible:

```bash
ssh fsn-b 'sudo systemctl kill -s SIGCONT faas-vmmd'
```

## Drill 2 — Drain cascade

Step 1: Issue the drain.

```bash
faasctl nodes drain fsn-a --wait
# --wait blocks until drained_at lands. The recovery arbiter
# owns the migration; ?wait=1 returns 200 with the timestamp.
```

Step 2: Verify the cascade completed.

```bash
faasctl nodes list --format=tsv | awk '$1=="fsn-a" && $4=="active"'
# Lifecycle back to 'active'. drain_initiated_at + drain_completed_at
# stamped on the row. Per-instance live-migration events present.
```

Expected outcome: fsn-a back to 'active' with zero live
instances, every previously-running app migrated to fsn-b
without customer-visible 5xx.

## Drill 3 — pg_notify fan-out recovery

Step 1: Kill a schedd's pg_notify subscriber.

```bash
systemctl kill -s SIGSTOP schedd-fsn-a.service
# Pauses the scheduler process; the pg_notify consumer stops
# reading from the LISTEN session.
```

Step 2: Observe the recovery path.

```bash
# Within DefaultHeartbeatStaleness (90s) the dead-node reconciler
# picks up the missing heartbeat, flips fsn-a to 'unavailable',
# migrates its live instances to fsn-b. The pg_notify consumer
# backlog is replayed when SIGCONT resumes schedd-fsn-a.
systemctl kill -s SIGCONT schedd-fsn-a.service
```

Expected outcome: recovery completes within budget, no duplicate
ownership (CAS guarantees the row only migrates once), every
event row has its corresponding audit row in `events`.

## Out-of-band: cleanup

```bash
# Whatever state you leave the rows in, the test runbook
# tears down via t.Cleanup. For an operator drill, run:
faasctl nodes reactivate --all
# Resets lifecycle to 'active', clears last_recovery_outcome.
# This is the operator-initiated recovery shortcut; the
# canonical failure-driven recovery is via the arbiter.
```

## Cross-references

- `docs/adr/137-multi-node-failure-safe.md` — design rationale
- `pkg/sched/recovery_arbiter.go` — decision policy
- `cmd/e2e/twonode_failure_safe_metal_test.go` — automated
  acceptance tests (same scenarios, runnable in CI)
- `cmd/e2e/twonode_runbook_test.go` — runs this runbook's bash
  steps as a Go test, asserting each step produces the
  documented outcome
