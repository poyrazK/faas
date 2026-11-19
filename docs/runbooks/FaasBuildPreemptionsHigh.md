# FaasBuildPreemptionsHigh

## Symptom

`FaasBuildPreemptionsHigh` means one node gave more than 6 borrowed builds
back to tenants in the last hour (`builderd_build_preemptions_total`).
Elastic builder slots (ADR-20261118154632) let a node run builds on borrowed
slots in memory its tenant slice is not using. When tenants need that memory,
builderd cancels the newest borrowed build (`actor="builderd"`), or vmmd
destroys it (`actor="vmmd"`). The build goes back to the queue at its place
and restarts.

Tenants are not at risk: protecting them is what the give-back is for. The
cost lands on builds, which restart and wait longer. A node that keeps giving
builds back has a `builder_slots` max too high for its tenant load.

## Check

1. Find the node's give-backs in the builderd log: `giving a borrowed build's
   memory back to tenants` (builderd) or, in the vmmd log, `destroying a
   borrowed builder; tenants need its memory`. Both log the tenant, borrowed
   and available memory they saw.
2. Compare `builderd_build_slots_active{kind="borrowed"}` with
   `builderd_build_slots_limit` and `builderd_build_slots_reserved` on that
   node, and read `vmmd_builder_borrowed_memory_bytes`.
3. On the node, read `memory.current` and `memory.max` of
   `/sys/fs/cgroup/faas.slice/faas-tenant.slice` and `memory.current` of
   `/sys/fs/cgroup/faas.slice/faas-borrowed.slice`.
4. `actor="vmmd"` give-backs mean builderd did not give memory back fast
   enough, or was not running. Check builderd's health on that node.

## Recover

1. Lower that node's `max` in `builder_slots`. Each step down removes one
   borrowed slot:

   ```bash
   gregalectl config show --key builder_slots
   gregalectl config set --key builder_slots --value '{"default":{"reserved":1,"max":2},"nodes":{"<node>":{"reserved":1,"max":2}}}' --reason "FaasBuildPreemptionsHigh on <node>" --yes
   ```

   Running builds are never cancelled by a lower `max`; the node stops
   claiming new borrowed builds until it is below it.
2. If every node is giving builds back, turn elastic slots off:

   ```bash
   gregalectl config set --key builder_elastic_slots --value off --reason "FaasBuildPreemptionsHigh fleet-wide" --yes
   ```

   Borrowed builds already running finish, and both give-backs stay armed
   until none is left.
3. A build given back twice runs only on a reserved slot. If a deployment sits
   in `building` for long, check `builds.preemptions` for its build.
