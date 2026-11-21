# ADR-20261118060000: A current-contract activation keeps the no-drain path; only the first node runs the canary

- **Status:** proposed
- **Date:** 2026-11-18
- **Amends:** ADR-20261025095348 (`--activate-prepared` starts at the prepared
  marker), ADR-20261116150000 (the per-node canary)
- **Related:** ADR-20261117140000 (per-variable role hashes), the no-drain
  rollout (`faas_join_skip_drain` in `deploy/ansible/node_join.yml`)

This record has a date-and-time identifier because numbered ADRs collide
when parallel changes pick the next number.

## Context

The production rollout of rc.354 (cd-platform run 44238073134) took about
45 minutes. Two parts of it did not need to happen:

1. **Both nodes drained.** When a node's bootstrap contract is current,
   `--activate-prepared` starts `node_join.yml` at "Verify the prepared
   rollout marker" and skips every earlier task in every play. Two of those
   tasks register results the no-drain decision reads: the compute-node
   lookup (`faas_join_existing_compute_node`) and the active vmmd stat
   (`faas_join_active_vmmd`). Both are read through `default`, so nothing
   failed: the adoption check was skipped, and the drain block's
   `phase == 'activate'` branch drained the node. Each drain waited about
   335 s, and it migrated every live guest. The drain rescue reads the same
   lookup to decide whether to re-activate the node, so a failed drain would
   have left a previously active node drained.

   Before ADR-20261117140000 a release almost always changed a role hash, so
   activation ran from the top and the lookups ran (rc.349 logged
   "Restarting without a drain"). Per-variable hashes made the current
   contract the common case, and with it the drain.

2. **The canary ran on every node.** After each node activates, the canary
   deploys acceptance apps until that node serves one and has built one.
   On rc.354 it took 165 s on fsn-2 and 197 s on fsn-3. On the last node it
   repeats work the fleet acceptance gate does right after.

## Decision

1. After the prepared marker, `node_join.yml` looks up the compute node again
   when the earlier lookup did not run, and reuses the serving vmmd stat (the
   same path) as the active vmmd stat when that did not run. A run from the
   top keeps its earlier results. The no-drain decision and the drain rescue
   therefore see the same inputs in both modes.
   `TestNodeJoinActivatePreparedNeverReadsResultsItSkipped` fails any later
   task that reads a result registered only before the start task, including
   reads through `default`. The one exception is the per-node database login
   block (`faas_compute_db_cert_logins`, off in production). With that switch
   on, a current-contract activation fails on an undefined loop rather than
   changing behaviour silently. Making that block work in a current-contract
   activation is a follow-up.

2. cd-platform runs the canary only on the first node it activates. That leg
   still stops a release that cannot serve or build before any other node
   moves, which was the canary's purpose (rc.301). Later nodes rely on the
   fleet acceptance gate, which requires every active node to host an
   acceptance deployment.

## Consequences

- A release whose roles are unchanged restarts vmmd without a drain when the
  candidate adopts every live guest. That saves about 11 minutes on two nodes,
  and no guest migrates.
- The canary saves about 3 minutes on a two-node fleet, and more per node on a
  larger one.
- A node other than the first gets no build proof of its own. If its builderd
  is broken while the first node's builds succeed, the fleet acceptance can
  still pass, because builds are placed on any builder. The node then reports
  failing builds the way any degraded builder does.
